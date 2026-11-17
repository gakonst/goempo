package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	revertSelectorLength = 4
	abiWord              = 32
)

const systemCallGasLimit = 250_000_000

const _ = uint(abiWord - revertSelectorLength)

var errorStringSelector = [revertSelectorLength]byte{0x08, 0xc3, 0x79, 0xa0}

type CallRequest struct {
	From                 *primitives.Address
	To                   *primitives.Address
	Gas                  *uint64
	GasPrice             *u256.Int
	MaxFeePerGas         *u256.Int
	MaxPriorityFeePerGas *u256.Int
	Value                u256.Int
	Input                []byte
	Nonce                *uint64
	AccessList           types.AccessList
	FeeToken             *primitives.Address
	NonceKey             *u256.Int
	Calls                []types.Call
}

type CallResult struct {
	Return   []byte
	GasUsed  uint64
	Reverted bool
	Halt     string
}

type TraceOptions struct {
	Tracer    string
	Config    json.RawMessage
	Raw       json.RawMessage
	OpcodeGas bool
}

type Executor interface {
	Call(block uint64, req *CallRequest, gasCap uint64) (CallResult, error)
	EstimateGas(block uint64, req *CallRequest, gasCap uint64) (uint64, error)
	TraceBlock(block uint64, opts *TraceOptions) ([]json.RawMessage, error)
	ReplayBlock(block uint64, diff DiffMode) ([]*TxReplay, error)
	TraceCalls(block uint64, reqs []*CallRequest, diff DiffMode) ([]TxReplay, error)
	CallMany(block uint64, bundles []CallBundle) ([][]CallResult, error)
	TraceCallMany(block uint64, bundles []CallBundle, opts *TraceOptions) ([][]json.RawMessage, error)
	TraceCall(block uint64, req *CallRequest, opts *TraceOptions) (json.RawMessage, error)
	CreateAccessList(block uint64, req *CallRequest, gasCap uint64) (AccessListResult, error)
	SimulateV1(block uint64, req *SimulateRequest) ([]SimulatedBlock, error)
}

type AccessListResult struct {
	AccessList types.AccessList
	GasUsed    uint64
	Failure    CallResult
}

type callObject struct {
	From                 *primitives.Address `json:"from"`
	To                   *primitives.Address `json:"to"`
	Gas                  *string             `json:"gas"`
	GasPrice             *string             `json:"gasPrice"`
	MaxFeePerGas         *string             `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *string             `json:"maxPriorityFeePerGas"`
	Value                *string             `json:"value"`
	Input                *string             `json:"input"`
	Data                 *string             `json:"data"`
	Nonce                *string             `json:"nonce"`
	FeeToken             *primitives.Address `json:"feeToken"`
	NonceKey             *string             `json:"nonceKey"`
}

func quotedQuantity(s *string) (*uint64, error) {
	if s == nil {
		return nil, nil
	}
	v, err := parseHexUint64(*s)
	if err != nil {
		return nil, invalidParams(errDetail(err))
	}
	return &v, nil
}

func quoted256(s *string) (*u256.Int, error) {
	if s == nil {
		return nil, nil
	}
	b, err := json.Marshal(*s)
	if err != nil {
		return nil, invalidParams("invalid quantity")
	}
	v, err := quantity256Param(b)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func callParam(raw json.RawMessage) (*CallRequest, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, typeError(raw, "struct TransactionRequest")
	}
	var obj callObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, invalidParams(err.Error() + column(raw))
	}
	req := &CallRequest{From: obj.From, To: obj.To, FeeToken: obj.FeeToken}
	var err error
	if req.Gas, err = quotedQuantity(obj.Gas); err != nil {
		return nil, err
	}
	if req.Nonce, err = quotedQuantity(obj.Nonce); err != nil {
		return nil, err
	}
	if req.GasPrice, err = quoted256(obj.GasPrice); err != nil {
		return nil, err
	}
	if req.MaxFeePerGas, err = quoted256(obj.MaxFeePerGas); err != nil {
		return nil, err
	}
	if req.MaxPriorityFeePerGas, err = quoted256(obj.MaxPriorityFeePerGas); err != nil {
		return nil, err
	}
	if req.NonceKey, err = quoted256(obj.NonceKey); err != nil {
		return nil, err
	}
	value, err := quoted256(obj.Value)
	if err != nil {
		return nil, err
	}
	if value != nil {
		req.Value = *value
	}
	return req, callInput(req, &obj)
}

func callInput(req *CallRequest, obj *callObject) error {
	if obj.Input != nil && obj.Data != nil && *obj.Input != *obj.Data {
		return newError(CodeInvalidParams, `both "data" and "input" are set and not equal. Please use "input" to pass transaction call data`)
	}
	src := obj.Input
	if src == nil {
		src = obj.Data
	}
	if src == nil {
		return nil
	}
	b, err := primitives.DecodeHex(*src)
	if err != nil {
		return invalidParams("invalid hex string")
	}
	req.Input = b
	return nil
}

type callTarget struct {
	req   *CallRequest
	block uint64
}

func (s *Server) callArgs(p *params) (callTarget, error) {
	raw, err := p.required("request")
	if err != nil {
		return callTarget{}, err
	}
	req, err := callParam(raw)
	if err != nil {
		return callTarget{}, err
	}
	ref, err := blockIDParam(p.optional("block_number"))
	if err != nil {
		return callTarget{}, err
	}
	n, err := s.stateBlock(ref)
	if err != nil {
		return callTarget{}, err
	}
	if s.exec == nil {
		return callTarget{}, internalError("unimplemented")
	}
	return callTarget{req: req, block: n}, nil
}

func RevertError(ret []byte) *Error {
	msg := "execution reverted"
	if reason, ok := decodeTempoError(ret); ok {
		msg += ": " + reason
	} else if reason, ok := decodeErrorString(ret); ok {
		msg += ": " + reason
	}
	return &Error{Code: CodeExecution, Message: msg, Data: dataResult(ret)}
}

func decodeErrorString(ret []byte) (string, bool) {
	if len(ret) < revertSelectorLength+2*abiWord {
		return "", false
	}
	if !bytes.Equal(ret[:revertSelectorLength], errorStringSelector[:]) {
		return "", false
	}
	body := ret[revertSelectorLength:]
	off := u256.FromBytes(body[:abiWord])
	if !off.IsUint64() || off.Uint64() > uint64(len(body)-abiWord) {
		return "", false
	}
	o := off.Uint64()
	size := u256.FromBytes(body[o : o+abiWord])
	if !size.IsUint64() || size.Uint64() > uint64(len(body))-o-abiWord {
		return "", false
	}
	return string(body[o+abiWord : o+abiWord+size.Uint64()]), true
}

func executionError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return newError(CodeServer, err.Error())
}

func ethCall(s *Server, p *params) ([]byte, error) {
	t, err := s.callArgs(p)
	if err != nil {
		return nil, err
	}
	res, err := s.exec.Call(t.block, t.req, s.cfg.GasCap)
	if err != nil {
		return nil, executionError(err)
	}
	if res.Reverted {
		return nil, RevertError(res.Return)
	}
	if res.Halt != "" {
		return nil, newError(CodeServer, res.Halt)
	}
	return dataResult(res.Return), nil
}

func ethEstimateGas(s *Server, p *params) ([]byte, error) {
	t, err := s.callArgs(p)
	if err != nil {
		return nil, err
	}
	gas, err := s.exec.EstimateGas(t.block, t.req, s.cfg.GasCap)
	if err != nil {
		return nil, executionError(err)
	}
	return quantityResult(gas), nil
}

func ethCreateAccessList(s *Server, p *params) ([]byte, error) {
	t, err := s.callArgs(p)
	if err != nil {
		return nil, err
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	res, err := s.exec.CreateAccessList(t.block, t.req, s.cfg.GasCap)
	if err != nil {
		return nil, executionError(err)
	}
	failure := ""
	switch {
	case res.Failure.Reverted:
		failure = RevertDebugString(res.Failure.Return)
	case res.Failure.Halt != "":
		failure = res.Failure.Halt
	}
	return encode(func(w *writer) {
		w.objOpen()
		writeAccessList(w, res.AccessList)
		if failure != "" {
			w.kStr("error", failure)
		}
		w.kQuantity("gasUsed", res.GasUsed)
		w.objClose()
	}), nil
}

func traceOptions(p *params) (*TraceOptions, error) {
	raw := p.optional("opts")
	opts := &TraceOptions{}
	if raw == nil {
		return opts, nil
	}
	var obj struct {
		Tracer       string          `json:"tracer"`
		TracerConfig json.RawMessage `json:"tracerConfig"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, typeError(raw, "struct GethDebugTracingOptions")
	}
	opts.Tracer, opts.Config, opts.Raw = obj.Tracer, obj.TracerConfig, raw
	return opts, nil
}

func (s *Server) traceBlock(b *BlockData, ref blockRef, p *params) ([]byte, error) {
	opts, err := traceOptions(p)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, blockNotFound(ref.describe())
	}
	traces, err := s.blockTraces(b, opts)
	if err != nil {
		return nil, err
	}
	n := min(len(traces), len(b.Block.Transactions))
	return encode(func(w *writer) {
		w.arrOpen()
		for i := range n {
			w.objOpen()
			w.kHash("txHash", b.TxHash(i))
			w.key("result")
			w.raw(traces[i])
			w.objClose()
		}
		w.arrClose()
	}), nil
}

func debugTraceBlockByNumber(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block")
	if err != nil {
		return nil, err
	}
	ref, err := blockNumberParam(raw)
	if err != nil {
		return nil, err
	}
	return s.traceBlock(s.blockFor(ref), ref, p)
}

func debugTraceBlockByHash(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block")
	if err != nil {
		return nil, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return nil, err
	}
	ref := blockRef{kind: tagHash, hash: h}
	return s.traceBlock(s.blockFor(ref), ref, p)
}

func debugTraceTransaction(s *Server, p *params) ([]byte, error) {
	t, err := txByHashArg(s, p)
	if err != nil {
		return nil, err
	}
	opts, err := traceOptions(p)
	if err != nil {
		return nil, err
	}
	if t.b == nil {
		return nil, newError(CodeServer, "transaction not found")
	}
	traces, err := s.blockTraces(t.b, opts)
	if err != nil {
		return nil, err
	}
	if t.i >= len(traces) {
		return nil, internalError("trace missing for transaction")
	}
	return traces[t.i], nil
}

func debugTraceCall(s *Server, p *params) ([]byte, error) {
	t, err := s.callArgs(p)
	if err != nil {
		return nil, err
	}
	opts, err := traceOptions(p)
	if err != nil {
		return nil, err
	}
	out, err := s.exec.TraceCall(t.block, t.req, opts)
	if err != nil {
		return nil, executionError(err)
	}
	return out, nil
}

func isSystemTx(tx types.Transaction) bool {
	legacy, ok := tx.Data.(*types.LegacyTx)
	return ok && legacy.IsSystem()
}

func systemOnly(b *BlockData) bool {
	for _, tx := range b.Block.Transactions {
		if !isSystemTx(tx) {
			return false
		}
	}
	return true
}

type tracerKind int

const (
	tracerStruct tracerKind = iota
	tracerCall
	tracerPrestate
	tracerPrestateDiff
	tracerFourByte
	tracerNoop
	tracerMux
	tracerOther
)

func tracerNamed(name string, config json.RawMessage) tracerKind {
	switch name {
	case "":
		return tracerStruct
	case "callTracer":
		return tracerCall
	case "prestateTracer":
		var cfg struct {
			DiffMode bool `json:"diffMode"`
		}
		if len(config) > 0 && json.Unmarshal(config, &cfg) == nil && cfg.DiffMode {
			return tracerPrestateDiff
		}
		return tracerPrestate
	case "4byteTracer":
		return tracerFourByte
	case "noopTracer":
		return tracerNoop
	case "muxTracer":
		return tracerMux
	}
	return tracerOther
}

func tracerOf(opts *TraceOptions) tracerKind {
	return tracerNamed(opts.Tracer, opts.Config)
}

func systemTrace(tx types.Transaction, kind tracerKind, opts *TraceOptions) ([]byte, bool) {
	if kind == tracerMux {
		return systemMux(tx, opts)
	}
	return systemTraceOne(tx, kind)
}

func systemTraceOne(tx types.Transaction, kind tracerKind) ([]byte, bool) {
	d, ok := tx.Data.(*types.LegacyTx)
	if !ok {
		return nil, false
	}
	switch kind {
	case tracerStruct:
		return []byte(`{"failed":false,"gas":0,"returnValue":"0x","structLogs":[]}`), true
	case tracerPrestate:
		return []byte("{}"), true
	case tracerPrestateDiff:
		return []byte(`{"post":{},"pre":{}}`), true
	case tracerNoop:
		return []byte("{}"), true
	case tracerFourByte:
		return encode(func(w *writer) {
			w.objOpen()
			if len(d.Data) >= selectorBytes {
				w.key(field(hexSelector(d.Data)))
				w.integer(1)
			}
			w.objClose()
		}), true
	case tracerCall:
		return encode(func(w *writer) {
			w.objOpen()
			w.kAddress("from", primitives.Address{})
			w.kQuantity("gas", 0)
			w.kQuantity("gasUsed", 0)
			w.key("to")
			w.address(systemTo(d))
			w.kData("input", d.Data)
			w.kQuantity256("value", d.Value)
			w.kStr("type", "CALL")
			w.objClose()
		}), true
	case tracerMux, tracerOther:
		return nil, false
	default:
		assert.Unreachable("tracerKind: unhandled value")
	}
	return nil, false
}

const selectorBytes = 4

func hexSelector(data []byte) string {
	const digits = "0123456789abcdef"
	out := []byte("0x")
	for _, c := range data[:selectorBytes] {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	out = append(out, '-')
	out = strconv.AppendInt(out, int64(len(data)-selectorBytes), 10)
	return string(out)
}

func systemMux(tx types.Transaction, opts *TraceOptions) ([]byte, bool) {
	var subs map[string]json.RawMessage
	if json.Unmarshal(opts.Config, &subs) != nil {
		return nil, false
	}
	names := make([]string, 0, len(subs))
	for n := range subs {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([][]byte, len(names))
	for i, n := range names {
		k := tracerNamed(n, subs[n])
		if k == tracerMux || k == tracerStruct {
			return nil, false
		}
		t, ok := systemTraceOne(tx, k)
		if !ok {
			return nil, false
		}
		parts[i] = t
	}
	return encode(func(w *writer) {
		w.objOpen()
		for i, n := range names {
			w.key(field(n))
			w.raw(parts[i])
		}
		w.objClose()
	}), true
}

func systemTraces(b *BlockData, kind tracerKind, opts *TraceOptions) ([]json.RawMessage, bool) {
	out := make([]json.RawMessage, 0, len(b.Block.Transactions))
	for _, tx := range b.Block.Transactions {
		t, ok := systemTrace(tx, kind, opts)
		if !ok {
			return nil, false
		}
		out = append(out, t)
	}
	return out, true
}

func (s *Server) blockTraces(b *BlockData, opts *TraceOptions) ([]json.RawMessage, error) {
	kind := tracerOf(opts)
	if systemOnly(b) {
		if traces, ok := systemTraces(b, kind, opts); ok {
			return traces, nil
		}
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	traces, err := s.exec.TraceBlock(b.Number(), opts)
	if err != nil {
		return nil, executionError(err)
	}
	for i, tx := range b.Block.Transactions {
		if i >= len(traces) || traces[i] != nil || !isSystemTx(tx) {
			continue
		}
		t, ok := systemTrace(tx, kind, opts)
		if !ok {
			return nil, internalError("unsupported tracer for a system transaction")
		}
		traces[i] = t
	}
	return traces, nil
}

func traceBlockParity(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block")
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return nil, err
	}
	b := s.blockFor(ref)
	if b == nil {
		return resultNull, nil
	}
	replays, err := s.replayBlock(b, false)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for i := range b.Block.Transactions {
			writeLocalizedTx(w, b, i, replays[i])
		}
		w.arrClose()
	}), nil
}

func writeParityCall(w *writer, b *BlockData, i int, d *types.LegacyTx) {
	w.objOpen()
	w.key("action")
	w.objOpen()
	w.kAddress("from", primitives.Address{})
	w.kStr("callType", "call")
	w.kQuantity("gas", systemCallGasLimit)
	w.kData("input", d.Data)
	w.kAddress("to", systemTo(d))
	w.kQuantity256("value", d.Value)
	w.objClose()
	w.kHash("blockHash", b.Hash)
	w.key("blockNumber")
	w.integer(int64(b.Number()))
	w.key("result")
	w.objOpen()
	w.kQuantity("gasUsed", 0)
	w.kData("output", nil)
	w.objClose()
	w.key("subtraces")
	w.integer(0)
	w.key("traceAddress")
	w.raw(resultEmpty)
	w.kHash("transactionHash", b.TxHash(i))
	w.key("transactionPosition")
	w.integer(int64(i))
	w.kStr("type", "call")
	w.objClose()
}

func systemTo(d *types.LegacyTx) primitives.Address {
	if d.To == nil {
		return primitives.Address{}
	}
	return *d.To
}
