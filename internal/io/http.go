package io

import (
	"bytes"
	"errors"
	stdio "io"
	"net/http"
	"strconv"
	"time"
)

const responseBytesMax = 256 << 20

var ErrHTTPStatus = errors.New("io: HTTP request failed")

type Caller interface {
	Call(body []byte) ([]byte, error)
}

type HTTPCaller struct {
	URL          string
	TimeoutNanos uint64
	client       *http.Client
}

func NewHTTPCaller(url string, timeoutNanos uint64) *HTTPCaller {
	return &HTTPCaller{URL: url, TimeoutNanos: timeoutNanos, client: &http.Client{Timeout: time.Duration(timeoutNanos)}}
}

func (c *HTTPCaller) Call(body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "goempo")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	data, readErr := stdio.ReadAll(stdio.LimitReader(resp.Body, responseBytesMax))
	closeErr := resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Join(ErrHTTPStatus, errors.New(strconv.Itoa(resp.StatusCode)), readErr, closeErr)
	}
	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	return data, closeErr
}

const _ = uint(responseBytesMax - pipeChunksMax)
