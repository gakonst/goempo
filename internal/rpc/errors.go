package rpc

import (
	"encoding/json"
	"errors"
	"strconv"
)

const (
	CodeParse            = -32700
	CodeInvalidRequest   = -32600
	CodeMethodNotFound   = -32601
	CodeInvalidParams    = -32602
	CodeInternal         = -32603
	CodeServer           = -32000
	CodeResourceNotFound = -32001
	CodeExecution        = 3
	CodeProofWindow      = 4444
)

type Error struct {
	Code    int
	Message string
	Data    []byte
	Status  int
}

func newError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

func (e *Error) Error() string { return strconv.Itoa(e.Code) + ": " + e.Message }

func (e *Error) write(w *writer) {
	w.objOpen()
	w.key("code")
	w.integer(int64(e.Code))
	w.kStr("message", e.Message)
	if e.Data != nil {
		w.key("data")
		w.raw(e.Data)
	}
	w.objClose()
}

func invalidParams(detail string) *Error {
	return &Error{Code: CodeInvalidParams, Message: "Invalid params", Data: appendQuoted(nil, detail)}
}

func badRequest(message string) *Error {
	return &Error{Code: CodeInvalidRequest, Message: message, Status: StatusBadRequest}
}

func methodNotFound() *Error { return newError(CodeMethodNotFound, "Method not found") }

func internalError(message string) *Error { return newError(CodeInternal, message) }

func blockNotFound(id string) *Error {
	return newError(CodeResourceNotFound, "block not found: "+id)
}

func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return internalError(err.Error())
}

func errDetail(err error) string {
	e := asError(err)
	var s string
	if json.Unmarshal(e.Data, &s) != nil {
		return e.Message
	}
	return s
}
