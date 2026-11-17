package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gakonst/goempo/internal/validator"
)

const (
	callerTimeout     = 30 * time.Second
	callerResponseMax = 1 << 20
	hexDigitsPerByte  = 2
)

const _ = uint(callerResponseMax - hexDigitsPerByte*validator.RegistrationCalldataMax)

var errCaller = errors.New("rpc error")

type rpcCaller struct {
	url  string
	http *http.Client
}

func newRPCCaller(url string) *rpcCaller {
	return &rpcCaller{url: url, http: &http.Client{Timeout: callerTimeout}}
}

type callerRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type callerResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	} `json:"error"`
}

func (c *rpcCaller) Call(method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(callerRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Post(c.url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, callerResponseMax))
	if err := errors.Join(err, resp.Body.Close()); err != nil {
		return nil, err
	}
	var out callerResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: http %d: %w", method, resp.StatusCode, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%w: %s: %d %s %s", errCaller, method, out.Error.Code, out.Error.Message, out.Error.Data)
	}
	return out.Result, nil
}
