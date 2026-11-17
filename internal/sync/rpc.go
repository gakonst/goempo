package sync

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/rlp"
)

const rpcBatchMax = 32

const _ = uint(BatchMax - rpcBatchMax)

var (
	ErrRPC      = errors.New("sync: upstream returned an error")
	ErrRPCReply = errors.New("sync: upstream reply is malformed")
)

type RPCSource struct {
	caller io.Caller
}

func NewRPCSource(caller io.Caller) *RPCSource {
	return &RPCSource{caller: caller}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func quantity(n uint64) string {
	return "0x" + strconv.FormatUint(n, 16)
}

func (s *RPCSource) batch(reqs []rpcRequest) ([]json.RawMessage, error) {
	body, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}
	data, err := s.caller.Call(body)
	if err != nil {
		return nil, err
	}
	var replies []rpcReply
	if err := json.Unmarshal(data, &replies); err != nil {
		return nil, errors.Join(ErrRPCReply, err)
	}
	out := make([]json.RawMessage, len(reqs))
	for _, r := range replies {
		if r.Error != nil {
			return nil, errors.Join(ErrRPC, errors.New(r.Error.Message))
		}
		if r.ID < 0 || r.ID >= len(out) {
			return nil, ErrRPCReply
		}
		out[r.ID] = r.Result
	}
	for _, r := range out {
		if r == nil {
			return nil, ErrRPCReply
		}
	}
	return out, nil
}

func decodeHex(raw json.RawMessage) ([]byte, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errors.Join(ErrRPCReply, err)
	}
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return nil, errors.Join(ErrRPCReply, err)
	}
	return b, nil
}

type tipBlock struct {
	Number string `json:"number"`
	Hash   string `json:"hash"`
}

func (s *RPCSource) Tip() (Link, error) {
	out, err := s.batch([]rpcRequest{{JSONRPC: "2.0", ID: 0, Method: "eth_getBlockByNumber", Params: []any{"latest", false}}})
	if err != nil {
		return Link{}, err
	}
	var b tipBlock
	if err := json.Unmarshal(out[0], &b); err != nil {
		return Link{}, errors.Join(ErrRPCReply, err)
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(b.Number, "0x"), 16, 64)
	if err != nil {
		return Link{}, errors.Join(ErrRPCReply, err)
	}
	hash, err := decodeHex(json.RawMessage(strconv.Quote(b.Hash)))
	if err != nil {
		return Link{}, err
	}
	link := Link{Number: n}
	if len(hash) != len(link.Hash) {
		return Link{}, ErrRPCReply
	}
	copy(link.Hash[:], hash)
	return link, nil
}

func (s *RPCSource) Blocks(from uint64, count int) ([]Raw, error) {
	count = max(1, min(count, rpcBatchMax))
	reqs := make([]rpcRequest, count)
	for i := range reqs {
		reqs[i] = rpcRequest{JSONRPC: "2.0", ID: i, Method: "debug_getRawBlock", Params: []any{quantity(from + uint64(i))}}
	}
	out, err := s.batch(reqs)
	if err != nil {
		return nil, err
	}
	raws := make([]Raw, 0, count)
	for _, r := range out {
		block, err := decodeHex(r)
		if err != nil {
			return nil, err
		}
		raw, err := SplitBlock(block)
		if err != nil {
			return nil, err
		}
		raws = append(raws, raw)
	}
	return raws, nil
}

func SplitBlock(block []byte) (Raw, error) {
	outer := rlp.NewStream(block)
	list, err := outer.List()
	if err != nil {
		return Raw{}, errors.Join(ErrBodyShape, err)
	}
	header, err := list.Raw()
	if err != nil {
		return Raw{}, errors.Join(ErrBodyShape, err)
	}
	return Raw{Header: header, Body: rlp.EncodeList(list.Rest())}, nil
}
