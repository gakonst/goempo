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

// Call posts body and returns the reply. A 429 or 503 reply (rate limit or
// overload on public RPC gateways) is retried with backoff, honoring
// Retry-After, before it is returned as an error.
func (c *HTTPCaller) Call(body []byte) ([]byte, error) {
	wait := 250 * time.Millisecond
	for attempt := 0; ; attempt++ {
		data, retryAfter, err := c.call(body)
		if retryAfter < 0 || attempt == callRetries {
			return data, err
		}
		time.Sleep(max(wait, retryAfter))
		wait = min(wait*2, 8*time.Second)
	}
}

const callRetries = 12

func (c *HTTPCaller) call(body []byte) ([]byte, time.Duration, error) {
	data, err := c.callOnce(body)
	var se *statusError
	if errors.As(err, &se) && (se.code == http.StatusTooManyRequests || se.code == http.StatusServiceUnavailable) {
		return nil, se.retryAfter, err
	}
	return data, -1, err
}

type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return strconv.Itoa(e.code) }

func (c *HTTPCaller) callOnce(body []byte) ([]byte, error) {
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
		se := &statusError{code: resp.StatusCode}
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			se.retryAfter = time.Duration(min(secs, 30)) * time.Second
		}
		return nil, errors.Join(ErrHTTPStatus, se, readErr, closeErr)
	}
	if readErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	return data, closeErr
}

const _ = uint(responseBytesMax - pipeChunksMax)
