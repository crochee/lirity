package e

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/json-iterator/go"
)

type ErrorCode interface {
	error
	json.Marshaler
	json.Unmarshaler
	StatusCode() int
	Code() int
	Message() string
	Result() interface{}
	WithStatusCode(int) ErrorCode
	WithCode(int) ErrorCode
	WithMessage(string) ErrorCode
	WithResult(interface{}) ErrorCode
}

type InnerError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Result  interface{} `json:"result"`
}

func From(response *http.Response) ErrorCode {
	decoder := jsoniter.ConfigCompatibleWithStandardLibrary.NewDecoder(response.Body)
	decoder.UseNumber()
	var result ErrCode
	if err := decoder.Decode(&result); err != nil {
		return ErrParseContent.WithResult(err)
	}
	return result.WithStatusCode(response.StatusCode)
}

func Froze(code int, message string) ErrorCode {
	return &ErrCode{
		code: code,
		msg:  message,
	}
}

const codeBit = 100000

// ErrCode 规定组成部分为http状态码+5位错误码
type ErrCode struct {
	code   int
	msg    string
	result interface{}
}

func (e *ErrCode) Error() string {
	return fmt.Sprintf("code:%d,message:%s,result:%s", e.Code(), e.Message(), e.Result())
}

func (e *ErrCode) MarshalJSON() ([]byte, error) {
	inner := &InnerError{
		Code:    e.code,
		Message: e.msg,
		Result:  e.result,
	}
	return jsoniter.ConfigCompatibleWithStandardLibrary.Marshal(inner)
}

func (e *ErrCode) UnmarshalJSON(bytes []byte) error {
	var result InnerError
	if err := jsoniter.ConfigCompatibleWithStandardLibrary.Unmarshal(bytes, &result); err != nil {
		return err
	}
	e.code = result.Code
	e.msg = result.Message
	e.result = result.Result
	return nil
}

func (e *ErrCode) StatusCode() int {
	return e.code / codeBit
}

func (e *ErrCode) Code() int {
	return e.code % codeBit
}

func (e *ErrCode) Message() string {
	return e.msg
}

func (e *ErrCode) Result() interface{} {
	return e.result
}

func (e *ErrCode) WithStatusCode(statusCode int) ErrorCode {
	ec := *e
	ec.code = ec.Code() + statusCode*codeBit
	return &ec
}

func (e *ErrCode) WithCode(code int) ErrorCode {
	ec := *e
	ec.code = ec.StatusCode()*codeBit + code
	return &ec
}

func (e *ErrCode) WithMessage(msg string) ErrorCode {
	ec := *e
	ec.msg = msg
	return &ec
}

func (e *ErrCode) WithResult(result interface{}) ErrorCode {
	ec := *e
	ec.result = result
	return &ec
}

var (
	// 00~99为服务级别错误码

	ErrInternalServerError = Froze(50010000, "服务器内部错误")
	ErrInvalidParam        = Froze(40010001, "请求参数不正确")
	ErrNotFound            = Froze(40410002, "资源不存在")
	ErrNotAllowMethod      = Froze(40510003, "不允许此方法")
	ErrParseContent        = Froze(50010004, "解析内容失败")
)

// AddCode business code to codeMessageBox
func AddCode(m map[ErrorCode]struct{}) error {
	temp := make(map[int]string)
	for errorCode := range map[ErrorCode]struct{}{
		ErrInternalServerError: {},
		ErrInvalidParam:        {},
		ErrNotFound:            {},
		ErrNotAllowMethod:      {},
		ErrParseContent:        {},
	} {
		code := errorCode.Code()
		value, ok := temp[code]
		if ok {
			return fmt.Errorf("error code %d(%s) already exists", code, value)
		}
		temp[code] = errorCode.Message()
	}
	for errorCode := range m {
		code := errorCode.Code()
		value, ok := temp[code]
		if ok {
			return fmt.Errorf("error code %d(%s) already exists", code, value)
		}
		temp[code] = errorCode.Message()
	}
	return nil
}
