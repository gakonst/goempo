package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/validator"
)

const (
	rpcBodyMax        = 1 << 20
	rpcReadTimeout    = 10 * time.Second
	rpcCodeNotFound   = -32601
	rpcCodeBadParams  = -32602
	rpcCodeParseError = -32700
)

const _ = uint(maxMessageSize - rpcBodyMax)

type snapshot struct {
	epoch  uint64
	member bool
	peers  int
	stats  validator.Stats
}

type status struct {
	c    *chain
	last atomic.Pointer[snapshot]
}

type rpcRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcBlock struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	StateRoot  string `json:"stateRoot"`
	Miner      string `json:"miner"`
	Timestamp  string `json:"timestamp"`
	GasUsed    string `json:"gasUsed"`
	GasLimit   string `json:"gasLimit"`
	ExtraData  string `json:"extraData"`
	TxCount    int    `json:"transactionCount"`
	Proposer   string `json:"proposer,omitempty"`
	View       string `json:"view,omitempty"`
	Epoch      string `json:"epoch,omitempty"`
}

var errBlockTag = errors.New("unknown block tag")

func hexU64(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }

func blockJSON(b *types.Block) rpcBlock {
	h := b.Header.Inner
	out := rpcBlock{
		Number: hexU64(h.Number), Hash: b.Hash().String(), ParentHash: h.ParentHash.String(), StateRoot: h.StateRoot.String(),
		Miner: h.Beneficiary.String(), Timestamp: hexU64(h.Timestamp), GasUsed: hexU64(h.GasUsed), GasLimit: hexU64(h.GasLimit),
		ExtraData: fmt.Sprintf("0x%x", h.ExtraData), TxCount: len(b.Transactions),
	}
	if c := b.Header.Consensus; c != nil {
		out.Proposer = fmt.Sprintf("0x%x", c.Proposer)
		out.View, out.Epoch = hexU64(c.View), hexU64(c.Epoch)
	}
	return out
}

func (s *status) blockNumber(raw json.RawMessage) (uint64, error) {
	var tag string
	if err := json.Unmarshal(raw, &tag); err != nil {
		return 0, err
	}
	switch tag {
	case "latest", "finalized", "safe", "pending":
		return s.c.Head().Header.Inner.Number, nil
	case "earliest":
		return 0, nil
	default:
		if !strings.HasPrefix(tag, "0x") {
			return 0, errBlockTag
		}
		return strconv.ParseUint(tag[2:], 16, 64)
	}
}

func (s *status) call(req rpcRequest) (any, error) {
	switch req.Method {
	case "eth_blockNumber":
		return hexU64(s.c.Head().Header.Inner.Number), nil
	case "eth_chainId":
		return hexU64(uint64(s.c.Executor().ChainID)), nil
	case "eth_getBlockByNumber":
		if len(req.Params) == 0 {
			return nil, &rpcError{Code: rpcCodeBadParams, Message: "missing block"}
		}
		n, err := s.blockNumber(req.Params[0])
		if err != nil {
			return nil, &rpcError{Code: rpcCodeBadParams, Message: err.Error()}
		}
		b, ok := s.c.BlockByNumber(n)
		if !ok {
			return nil, nil
		}
		return blockJSON(b), nil
	case "goempo_validatorStatus":
		snap := s.last.Load()
		if snap == nil {
			return nil, nil
		}
		return map[string]any{"epoch": snap.epoch, "member": snap.member, "peers": snap.peers, "proposed": snap.stats.Proposed,
			"committed": snap.stats.Committed, "epochs": snap.stats.Epochs, "dealt": snap.stats.Dealt, "acks": snap.stats.Acks}, nil
	default:
		return nil, &rpcError{Code: rpcCodeNotFound, Message: "method not found: " + req.Method}
	}
}

func (s *status) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/metrics" {
		s.metrics(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, rpcBodyMax))
	resp := rpcResponse{JSONRPC: "2.0"}
	var req rpcRequest
	if err == nil {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		resp.Error = &rpcError{Code: rpcCodeParseError, Message: err.Error()}
	} else {
		resp.ID = req.ID
		var cerr error
		resp.Result, cerr = s.call(req)
		if cerr != nil {
			resp.Error = &rpcError{Code: rpcCodeBadParams, Message: cerr.Error()}
			var re *rpcError
			if errors.As(cerr, &re) {
				resp.Error = re
			}
		}
	}
	w.Header().Set("content-type", "application/json")
	if resp.Result == nil && resp.Error == nil {
		_, err = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(resp.ID) + `,"result":null}`))
		logWrite(err)
		return
	}
	logWrite(json.NewEncoder(w).Encode(resp))
}

func logWrite(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "goempo validator: rpc write:", err)
	}
}

func (s *status) metrics(w http.ResponseWriter) {
	snap := s.last.Load()
	if snap == nil {
		snap = &snapshot{}
	}
	member := 0
	if snap.member {
		member = 1
	}
	fmt.Fprintf(w, "goempo_validator_head %d\ngoempo_validator_epoch %d\ngoempo_validator_member %d\ngoempo_validator_peers %d\ngoempo_validator_proposed_total %d\ngoempo_validator_committed_total %d\ngoempo_validator_dkg_dealt_total %d\ngoempo_validator_dkg_acks_total %d\n",
		s.c.Head().Header.Inner.Number, snap.epoch, member, snap.peers, snap.stats.Proposed, snap.stats.Committed, snap.stats.Dealt, snap.stats.Acks)
}

func serveStatus(wg *sync.WaitGroup, addr string, s *status) (*http.Server, error) {
	if addr == "" {
		return nil, nil
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: rpcReadTimeout}
	wg.Go(func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "goempo validator: status server:", err)
		}
	})
	return srv, nil
}
