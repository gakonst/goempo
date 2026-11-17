package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	rpcTimeout       = 60 * time.Second
	rpcResponseMax   = 1 << 28
	rpcBatchMax      = 50
	rpcRetries       = 12
	rpcRetryBackoff  = 2 * time.Second
	rpcBackoffMax    = 60 * time.Second
	mainnetRPC       = "https://rpc.tempo.xyz"
	rpcUserAgent     = "goempo-dev/0"
	jsonRPCVersion   = "2.0"
	hexPrefixLength  = 2
	hexBase          = 16
	uint64Bits       = 64
	mediaTypeJSON    = "application/json"
	blockTagTemplate = "0x%x"
)

const blockJSONMax = 1 << 22

const _ = uint(rpcTimeout - rpcBackoffMax)

const _ = uint(rpcResponseMax - rpcBatchMax*blockJSONMax)

var errRPC = errors.New("rpc: error response")

type rpcClient struct {
	url  string
	http *http.Client
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func newRPCClient(url string) *rpcClient {
	return &rpcClient{url: url, http: &http.Client{Timeout: rpcTimeout}}
}

func (c *rpcClient) batch(reqs []rpcRequest) ([]json.RawMessage, error) {
	if len(reqs) > rpcBatchMax {
		return nil, fmt.Errorf("rpc: batch of %d exceeds %d", len(reqs), rpcBatchMax)
	}
	var lastErr error
	for attempt := 0; attempt < rpcRetries; attempt++ {
		out, err := c.batchOnce(reqs)
		if err == nil {
			return out, nil
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "rpc: attempt %d: %v\n", attempt, err)
		time.Sleep(min(rpcRetryBackoff<<attempt, rpcBackoffMax))
	}
	return nil, lastErr
}

func (c *rpcClient) batchOnce(reqs []rpcRequest) ([]json.RawMessage, error) {
	body, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mediaTypeJSON)
	req.Header.Set("User-Agent", rpcUserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, rpcResponseMax))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpc: http %d", resp.StatusCode)
	}
	return orderResponses(raw, len(reqs))
}

func orderResponses(raw []byte, count int) ([]json.RawMessage, error) {
	var resps []rpcResponse
	if err := json.Unmarshal(raw, &resps); err != nil {
		return nil, err
	}
	if len(resps) != count {
		return nil, fmt.Errorf("rpc: got %d responses for %d requests", len(resps), count)
	}
	out := make([]json.RawMessage, count)
	for i := range resps {
		r := &resps[i]
		if r.Error != nil {
			return nil, fmt.Errorf("%w: %d %s", errRPC, r.Error.Code, r.Error.Message)
		}
		if r.ID < 0 || r.ID >= count {
			return nil, fmt.Errorf("rpc: response id %d out of range", r.ID)
		}
		out[r.ID] = r.Result
	}
	return out, nil
}

func (c *rpcClient) call(method string, params ...any) (json.RawMessage, error) {
	out, err := c.batch([]rpcRequest{{JSONRPC: jsonRPCVersion, ID: 0, Method: method, Params: params}})
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

func blockTag(n uint64) string {
	return fmt.Sprintf(blockTagTemplate, n)
}

func parseQuantity(s string) (uint64, error) {
	if len(s) < hexPrefixLength {
		return 0, fmt.Errorf("quantity %q too short", s)
	}
	return strconv.ParseUint(s[hexPrefixLength:], hexBase, uint64Bits)
}

type blockHeaderJSON struct {
	Number    string `json:"number"`
	Timestamp string `json:"timestamp"`
}

func (c *rpcClient) headNumber() (uint64, error) {
	raw, err := c.call("eth_blockNumber")
	if err != nil {
		return 0, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return parseQuantity(s)
}

func (c *rpcClient) blockTimestamp(n uint64) (uint64, error) {
	raw, err := c.call("eth_getBlockByNumber", blockTag(n), false)
	if err != nil {
		return 0, err
	}
	var h blockHeaderJSON
	if err := json.Unmarshal(raw, &h); err != nil {
		return 0, err
	}
	return parseQuantity(h.Timestamp)
}
