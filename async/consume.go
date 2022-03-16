package async

import (
	"context"

	"github.com/json-iterator/go"
	"github.com/streadway/amqp"
	"go.uber.org/zap"

	"github.com/crochee/lirity/logger"
	"github.com/crochee/lirity/routine"
	"github.com/crochee/lirity/validator"
)

// Consumer async impl
type Consumer interface {
	Register(name string, callback Callback)
	Unregister(name string)
	Subscribe(channel Channel, queueName string) error
}

// NewTaskConsumer gets Consumer
func NewTaskConsumer(ctx context.Context, opts ...Option) Consumer {
	o := &option{
		manager:   NewManager(),
		marshal:   DefaultMarshal{},
		handler:   jsoniter.ConfigCompatibleWithStandardLibrary,
		validator: validator.NewValidator(),
	}

	for _, opt := range opts {
		opt(o)
	}
	return &taskConsumer{
		pool: routine.NewPool(ctx, routine.Recover(func(ctx context.Context, i interface{}) {
			logger.From(ctx).Error("recover", zap.Any("error", i))
		})),
		manager:   o.manager,
		marshal:   o.marshal,
		handler:   o.handler,
		validator: o.validator,
	}
}

type taskConsumer struct {
	pool      *routine.Pool   // goroutine safe run pool
	manager   ManagerCallback // manager executor how to run
	marshal   MarshalAPI      // mq  assemble request or response
	handler   jsoniter.API
	validator validator.Validator
}

// Register registers a Callback with name
func (t *taskConsumer) Register(name string, callback Callback) {
	t.manager.Register(name, callback)
}

// Unregister unregisters a Callback with name
func (t *taskConsumer) Unregister(name string) {
	t.manager.Unregister(name)
}

// Subscribe consume message form Channel with queueName
func (t *taskConsumer) Subscribe(channel Channel, queueName string) error {
	t.pool.Go(func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			deliveries, err := channel.Consume(
				queueName,
				// 用来区分多个消费者
				"consumer."+queueName,
				// 是否自动应答(自动应答确认消息，这里设置为否，在下面手动应答确认)
				false,
				// 是否具有排他性
				false,
				// 如果设置为true，表示不能将同一个connection中发送的消息
				// 传递给同一个connection的消费者
				false,
				// 是否为阻塞
				false,
				nil,
			)
			if err != nil {
				logger.From(ctx).Error("consumer failed", zap.Error(err))
				continue
			}
			t.handleMessage(ctx, deliveries)
		}
	})
	t.pool.Wait()
	return nil
}

func (t *taskConsumer) handleMessage(ctx context.Context, deliveries <-chan amqp.Delivery) {
	for {
		select {
		case <-ctx.Done():
			return
		case v := <-deliveries:
			t.pool.Go(func(ctx context.Context) {
				if err := t.handle(ctx, v); err != nil {
					logger.From(ctx).Error("handle failed", zap.Error(err))
				}
			})
		}
	}
}

func (t *taskConsumer) handle(ctx context.Context, d amqp.Delivery) error {
	msgStruct, err := t.marshal.Unmarshal(&d)
	if err != nil {
		logger.From(ctx).Error("unmarshal failed", zap.Error(err))
		// 当requeue为true时，将该消息排队，以在另一个通道上传递给使用者。
		// 当requeue为false或服务器无法将该消息排队时，它将被丢弃。
		if err = d.Reject(false); err != nil {
			return err
		}
		return nil
	}
	l := logger.From(ctx).With(zap.String("uuid", msgStruct.UUID))
	ctx = logger.With(ctx, l)

	logger.From(ctx).Sugar().Debugf("consume body:%s", msgStruct.Payload)
	param := Get()
	if err = t.handler.Unmarshal(msgStruct.Payload, param); err != nil {
		logger.From(ctx).Error("unmarshal failed", zap.Error(err))
		// 当requeue为true时，将该消息排队，以在另一个通道上传递给使用者。
		// 当requeue为false或服务器无法将该消息排队时，它将被丢弃。
		if err = d.Reject(false); err != nil {
			return err
		}
		return nil
	}
	if err = t.validator.ValidateStruct(param); err != nil {
		logger.From(ctx).Error("validate struct failed", zap.Error(err))
		// 当requeue为true时，将该消息排队，以在另一个通道上传递给使用者。
		// 当requeue为false或服务器无法将该消息排队时，它将被丢弃。
		if err = d.Reject(false); err != nil {
			return err
		}
		return nil
	}
	err = t.manager.Run(ctx, param)
	Put(param)
	if err != nil {
		logger.From(ctx).Error("run failed", zap.Error(err))
		// 当requeue为true时，将该消息排队，以在另一个通道上传递给使用者。
		// 当requeue为false或服务器无法将该消息排队时，它将被丢弃。
		if err = d.Reject(false); err != nil {
			return err
		}
		return nil
	}
	// 手动确认收到本条消息, true表示回复当前信道所有未回复的ack，用于批量确认。
	// false表示回复当前条目
	return d.Ack(false)
}
