package node

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	goio "github.com/gakonst/goempo/internal/io"
)

const (
	UpstreamBatchMax = 32
	codeUnavailable  = 503
	jsonRPCVersion   = "2.0"
)

var (
	ErrUpstream      = errors.New("node: upstream returned an error")
	ErrUpstreamReply = errors.New("node: upstream reply is malformed")
)

type Upstream struct {
	caller goio.Caller
}

func NewUpstream(caller goio.Caller) *Upstream {
	return &Upstream{caller: caller}
}

type upstreamRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type upstreamError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type upstreamReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *upstreamError  `json:"error"`
}

func quantity(n uint64) string { return "0x" + strconv.FormatUint(n, 16) }

func (u *Upstream) batch(reqs []upstreamRequest) ([]upstreamReply, error) {
	body, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}
	raw, err := u.caller.Call(body)
	if err != nil {
		return nil, err
	}
	var replies []upstreamReply
	if err := json.Unmarshal(raw, &replies); err != nil {
		return nil, errors.Join(ErrUpstreamReply, err)
	}
	if len(replies) != len(reqs) {
		return nil, ErrUpstreamReply
	}
	out := make([]upstreamReply, len(reqs))
	for _, r := range replies {
		if r.ID < 0 || r.ID >= len(reqs) {
			return nil, ErrUpstreamReply
		}
		out[r.ID] = r
	}
	return out, nil
}

func (u *Upstream) Tip() (uint64, error) {
	out, err := u.batch([]upstreamRequest{{JSONRPC: jsonRPCVersion, Method: "eth_blockNumber", Params: []any{}}})
	if err != nil {
		return 0, err
	}
	if out[0].Error != nil {
		return 0, fmt.Errorf("%w: %s", ErrUpstream, out[0].Error.Message)
	}
	var s string
	if err := json.Unmarshal(out[0].Result, &s); err != nil {
		return 0, errors.Join(ErrUpstreamReply, err)
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	if err != nil {
		return 0, errors.Join(ErrUpstreamReply, err)
	}
	return n, nil
}

func (u *Upstream) RawBlocks(from uint64, count int) ([][]byte, error) {
	count = max(1, min(count, UpstreamBatchMax))
	reqs := make([]upstreamRequest, count)
	for i := range reqs {
		reqs[i] = upstreamRequest{JSONRPC: jsonRPCVersion, ID: i, Method: "debug_getRawBlock", Params: []any{quantity(from + uint64(i))}}
	}
	out, err := u.batch(reqs)
	if err != nil {
		return nil, err
	}
	raws := make([][]byte, 0, count)
	for _, r := range out {
		if r.Error != nil || len(r.Result) == 0 || string(r.Result) == "null" {
			break
		}
		raw, err := decodeHexString(r.Result)
		if err != nil {
			return raws, err
		}
		if len(raw) == 0 {
			break
		}
		raws = append(raws, raw)
	}
	if len(raws) == 0 {
		return nil, ErrNotArchived
	}
	return raws, nil
}

type finalizationJSON struct {
	Certificate string `json:"certificate"`
}

func (u *Upstream) Finalizations(from uint64, count int) ([][]byte, error) {
	count = max(1, min(count, UpstreamBatchMax))
	reqs := make([]upstreamRequest, count)
	for i := range reqs {
		reqs[i] = upstreamRequest{JSONRPC: jsonRPCVersion, ID: i, Method: "consensus_getFinalization", Params: []any{map[string]uint64{"height": from + uint64(i)}}}
	}
	out, err := u.batch(reqs)
	if err != nil {
		return nil, err
	}
	certs := make([][]byte, count)
	for i, r := range out {
		if r.Error != nil {
			if r.Error.Code != codeUnavailable {
				return nil, fmt.Errorf("%w: %d %s", ErrUpstream, r.Error.Code, r.Error.Message)
			}
			continue
		}
		var f finalizationJSON
		if err := json.Unmarshal(r.Result, &f); err != nil || f.Certificate == "" {
			continue
		}
		cert, err := hex.DecodeString(strings.TrimPrefix(f.Certificate, "0x"))
		if err != nil {
			return nil, errors.Join(ErrUpstreamReply, err)
		}
		certs[i] = cert
	}
	return certs, nil
}

func decodeHexString(raw json.RawMessage) ([]byte, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.Join(ErrUpstreamReply, err)
	}
	out, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return nil, errors.Join(ErrUpstreamReply, err)
	}
	return out, nil
}
