package rpc

import (
	"bytes"
	"encoding/json"
)

const (
	StatusOK         = 200
	StatusBadRequest = 400
	batchMax         = 1000
	responseHint     = 256
	idDigitsMax      = 20
)

const _ = uint(batchMax - idDigitsMax)

type Response struct {
	Status int
	Body   []byte
}

type envelope struct {
	JSONRPC json.RawMessage `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  json.RawMessage `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type call struct {
	id     []byte
	method string
	params []byte
	notify bool
}

var (
	nullID     = []byte("null")
	nullBody   = []byte("null")
	versionTwo = []byte("\"2.0\"")
)

func (s *Server) Handle(body []byte) Response {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return s.handleBatch(trimmed)
	}
	var env envelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return errorResponse(nullID, badRequest("invalid request: parse error"))
	}
	c, err := parseCall(&env)
	if err != nil {
		return errorResponse(c.id, asError(err))
	}
	if c.notify {
		s.dispatch(&c)
		return Response{Status: StatusOK, Body: nullBody}
	}
	w := newWriter(responseHint)
	s.writeCall(w, &c)
	return Response{Status: StatusOK, Body: w.bytes()}
}

func (s *Server) handleBatch(body []byte) Response {
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return errorResponse(nullID, badRequest("invalid request: parse error"))
	}
	if len(items) == 0 {
		return errorResponse(nullID, badRequest("invalid request: empty batch"))
	}
	if len(items) > batchMax {
		return errorResponse(nullID, badRequest("invalid request: batch too large"))
	}
	w := newWriter(responseHint * len(items))
	w.arrOpen()
	for _, item := range items {
		s.writeBatchItem(w, item)
	}
	w.arrClose()
	return Response{Status: StatusOK, Body: w.bytes()}
}

func (s *Server) writeBatchItem(w *writer, item json.RawMessage) {
	var env envelope
	if len(item) == 0 || item[0] != '{' || json.Unmarshal(item, &env) != nil {
		writeError(w, nullID, badRequest("invalid request: not a request object"))
		return
	}
	c, err := parseCall(&env)
	if err != nil {
		writeError(w, c.id, asError(err))
		return
	}
	if c.notify {
		s.dispatch(&c)
		return
	}
	s.writeCall(w, &c)
}

func parseCall(env *envelope) (call, error) {
	c := call{id: nullID}
	if env.ID == nil {
		c.notify = true
	} else if !validID(env.ID) {
		return c, badRequest("invalid request: invalid id")
	} else {
		c.id = env.ID
	}
	if len(env.Method) == 0 || env.Method[0] != '"' || json.Unmarshal(env.Method, &c.method) != nil {
		return c, badRequest("invalid request: missing method")
	}
	if !bytes.Equal(env.JSONRPC, versionTwo) {
		return c, newError(CodeInvalidRequest, "Invalid request")
	}
	c.params = env.Params
	return c, nil
}

func validID(id json.RawMessage) bool {
	if bytes.Equal(id, nullID) {
		return true
	}
	if id[0] == '"' {
		var s string
		return json.Unmarshal(id, &s) == nil
	}
	if len(id) > idDigitsMax {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (s *Server) writeCall(w *writer, c *call) {
	result, err := s.dispatch(c)
	if err != nil {
		writeError(w, c.id, asError(err))
		return
	}
	w.objOpen()
	w.kStr("jsonrpc", "2.0")
	w.key("id")
	w.raw(c.id)
	w.key("result")
	w.raw(result)
	w.objClose()
}

func writeError(w *writer, id []byte, e *Error) {
	w.objOpen()
	w.kStr("jsonrpc", "2.0")
	w.key("id")
	w.raw(id)
	w.key("error")
	e.write(w)
	w.objClose()
}

func errorResponse(id []byte, e *Error) Response {
	w := newWriter(responseHint)
	writeError(w, id, e)
	status := e.Status
	if status == 0 {
		status = StatusOK
	}
	return Response{Status: status, Body: w.bytes()}
}
