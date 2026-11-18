package nodetest

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gakonst/goempo/internal/node"
)

const requestBatchMax = 1024

const _ = uint(requestBatchMax - node.UpstreamParallel*node.UpstreamBatchMax)

type LocalUpstream struct {
	Archive     *node.Archive
	Tip         uint64
	Delay       time.Duration
	RejectEvery uint64
	Requests    atomic.Uint64
	Rejected    atomic.Uint64
	Blocks      atomic.Uint64
}

type request struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type replyError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type reply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *replyError     `json:"error,omitempty"`
}

func (u *LocalUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := u.Requests.Add(1)
	if u.Delay > 0 {
		time.Sleep(u.Delay)
	}
	if u.RejectEvery > 0 && n%u.RejectEvery == 0 {
		u.Rejected.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var reqs []request
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(reqs) > requestBatchMax {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	out := make([]reply, len(reqs))
	for i := range min(len(reqs), requestBatchMax) {
		out[i] = u.answer(reqs[i])
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		u.Rejected.Add(0)
	}
}

func (u *LocalUpstream) answer(req request) reply {
	rep := reply{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "eth_blockNumber":
		rep.Result = "0x" + strconv.FormatUint(u.Tip, 16)
	case "debug_getRawBlock":
		number, ok := quantity(req.Params)
		if !ok || number > u.Tip {
			rep.Error = &replyError{Code: -32000, Message: "unknown block"}
			return rep
		}
		raw, err := u.Archive.Raw(number)
		if err != nil {
			rep.Error = &replyError{Code: -32000, Message: err.Error()}
			return rep
		}
		u.Blocks.Add(1)
		rep.Result = "0x" + hex.EncodeToString(raw)
	case "consensus_getFinalization":
		rep.Error = &replyError{Code: 503, Message: "not available"}
	default:
		rep.Error = &replyError{Code: -32601, Message: "method not found"}
	}
	return rep
}

func quantity(params []json.RawMessage) (uint64, bool) {
	if len(params) == 0 {
		return 0, false
	}
	var s string
	if err := json.Unmarshal(params[0], &s); err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
	return n, err == nil
}
