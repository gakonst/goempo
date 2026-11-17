package rpcexec

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tracing"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

var (
	ErrNoBlockExecutor = errors.New("rpcexec: block tracing needs a block executor")
	ErrStateKind       = errors.New("rpcexec: state cannot replay a block")
	ErrTracer          = errors.New("rpcexec: unsupported tracer")
)

type BlockState interface {
	executor.State
	Discard()
}

type tracerKind int

const (
	kindStruct tracerKind = iota
	kindCall
	kindPrestate
	kindPrestateDiff
	kindFourByte
	kindNoop
	kindMux
	kindFlatCall
)

type traceConfig struct {
	kind     tracerKind
	call     tracing.CallConfig
	steps    tracing.StepOptions
	mux      []muxEntry
	recorded bool
}

type muxEntry struct {
	name string
	cfg  traceConfig
}

type rawOptions struct {
	Tracer           string          `json:"tracer"`
	TracerConfig     json.RawMessage `json:"tracerConfig"`
	DisableStorage   bool            `json:"disableStorage"`
	DisableStack     bool            `json:"disableStack"`
	EnableMemory     bool            `json:"enableMemory"`
	EnableReturnData bool            `json:"enableReturnData"`
}

func parseOne(name string, cfgRaw json.RawMessage, raw rawOptions) (traceConfig, error) {
	switch name {
	case "":
		return traceConfig{kind: kindStruct, steps: tracing.StepOptions{
			Enabled: true, Stack: !raw.DisableStack, Memory: raw.EnableMemory,
			ReturnData: raw.EnableReturnData, Storage: !raw.DisableStorage,
		}}, nil
	case "callTracer":
		var c struct {
			OnlyTopCall bool `json:"onlyTopCall"`
			WithLog     bool `json:"withLog"`
		}
		if len(cfgRaw) > 0 && string(cfgRaw) != "null" {
			if err := json.Unmarshal(cfgRaw, &c); err != nil {
				return traceConfig{}, err
			}
		}
		return traceConfig{kind: kindCall, call: tracing.CallConfig{OnlyTopCall: c.OnlyTopCall, WithLog: c.WithLog}}, nil
	case "prestateTracer":
		var c struct {
			DiffMode bool `json:"diffMode"`
		}
		if len(cfgRaw) > 0 && string(cfgRaw) != "null" {
			if err := json.Unmarshal(cfgRaw, &c); err != nil {
				return traceConfig{}, err
			}
		}
		if c.DiffMode {
			return traceConfig{kind: kindPrestateDiff}, nil
		}
		return traceConfig{kind: kindPrestate}, nil
	case "4byteTracer":
		return traceConfig{kind: kindFourByte}, nil
	case "noopTracer":
		return traceConfig{kind: kindNoop}, nil
	case "flatCallTracer":
		return traceConfig{kind: kindFlatCall}, nil
	}
	return traceConfig{}, ErrTracer
}

func parseOptions(opts *rpc.TraceOptions) (traceConfig, error) {
	var raw rawOptions
	if opts != nil && len(opts.Raw) > 0 {
		if err := json.Unmarshal(opts.Raw, &raw); err != nil {
			return traceConfig{}, err
		}
	}
	if raw.Tracer != "muxTracer" {
		return parseOne(raw.Tracer, raw.TracerConfig, raw)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw.TracerConfig, &m); err != nil {
		return traceConfig{}, err
	}
	out := traceConfig{kind: kindMux}
	for _, n := range sortedKeys(m) {
		sub, err := parseOne(n, m[n], raw)
		if err != nil {
			return traceConfig{}, err
		}
		if sub.kind == kindStruct {
			return traceConfig{}, ErrTracer
		}
		out.mux = append(out.mux, muxEntry{name: n, cfg: sub})
	}
	return out, nil
}

type blockRun struct {
	data    *rpc.BlockData
	session *tracing.Session
	user    []int
	infos   []tracing.TxInfo
}

func (x *Executor) replayBlock(number uint64, opts tracing.StepOptions) (*blockRun, error) {
	if x.Blocks == nil {
		return nil, ErrNoBlockExecutor
	}
	if number == 0 {
		return nil, ErrNoBlock
	}
	b, ok := x.Chain.Block(number)
	if !ok {
		return nil, ErrNoBlock
	}
	bs, err := x.blockState(number - 1)
	if err != nil {
		return nil, err
	}
	defer bs.Discard()
	run := &blockRun{data: b}
	var senders []evm.Address
	txs := b.Block.Transactions
	run.infos = make([]tracing.TxInfo, len(txs))
	for i, tx := range txs {
		if isSystem(tx) {
			continue
		}
		from, err := tx.Sender()
		if err != nil {
			return nil, err
		}
		run.user = append(run.user, i)
		senders = append(senders, from)
		_, aa := tx.Data.(*types.TempoTx)
		run.infos[i] = tracing.TxInfo{AA: aa, Sender: from, GasLimit: gasLimit(tx)}
	}
	run.session = tracing.NewSession(bs, senders, opts)
	res, err := x.Blocks.Execute(run.session, executor.Input{Block: b.Block, GetHash: x.blockHash(number)})
	if err != nil {
		return nil, err
	}
	var prev uint64
	for i := range txs {
		if i >= len(res.Receipts) {
			break
		}
		rc := &res.Receipts[i]
		run.infos[i].GasUsed = rc.CumulativeGasUsed - prev
		run.infos[i].Success = rc.Success
		prev = rc.CumulativeGasUsed
	}
	return run, nil
}

func (x *Executor) blockState(number uint64) (BlockState, error) {
	st, err := x.States.ExecState(number)
	if err != nil {
		return nil, err
	}
	bs, ok := st.(BlockState)
	if !ok {
		return nil, ErrStateKind
	}
	return bs, nil
}

func isSystem(tx types.Transaction) bool {
	l, ok := tx.Data.(*types.LegacyTx)
	return ok && l.IsSystem()
}

func gasLimit(tx types.Transaction) uint64 {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		return d.Gas
	case *types.AccessListTx:
		return d.Gas
	case *types.DynamicFeeTx:
		return d.Gas
	case *types.SetCodeTx:
		return d.Gas
	case *types.TempoTx:
		return d.GasLimit
	}
	return 0
}

func (r *blockRun) trace(userIdx int) *tracing.TxTrace {
	t := r.session.Traces()[userIdx]
	if t == nil {
		t = &tracing.TxTrace{Recorder: tracing.NewRecorder(nil, tracing.StepOptions{})}
	}
	if t.Recorder == nil {
		t.Recorder = tracing.NewRecorder(nil, tracing.StepOptions{})
	}
	return t
}

func render(cfg traceConfig, t *tracing.TxTrace, info tracing.TxInfo) []byte {
	if cfg.kind != kindMux {
		return renderOne(cfg, t, info)
	}
	out := []byte{'{'}
	for i, m := range cfg.mux {
		if i > 0 {
			out = append(out, ',')
		}
		out = strconv.AppendQuote(out, m.name)
		out = append(out, ':')
		out = append(out, renderOne(m.cfg, t, info)...)
	}
	return append(out, '}')
}

func renderOne(cfg traceConfig, t *tracing.TxTrace, info tracing.TxInfo) []byte {
	switch cfg.kind {
	case kindStruct:
		return tracing.StructJSON(t.Recorder, info, cfg.steps)
	case kindCall:
		return tracing.CallJSON(t.Recorder, info, cfg.call)
	case kindPrestate:
		return tracing.PrestateJSON(t)
	case kindPrestateDiff:
		return tracing.PrestateDiffJSON(t)
	case kindFourByte:
		return tracing.FourByteJSON(t.Recorder)
	case kindNoop, kindMux:
		return []byte("{}")
	case kindFlatCall:
		return tracing.ParityJSON(t.Recorder, info, tracing.Locator{})
	default:
		assert.Unreachable("tracerKind: unhandled value")
	}
	return []byte("{}")
}

func (x *Executor) TraceBlock(number uint64, opts *rpc.TraceOptions) ([]json.RawMessage, error) {
	cfg, err := parseOptions(opts)
	if err != nil {
		return nil, err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	run, err := x.replayBlock(number, cfg.steps)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, len(run.data.Block.Transactions))
	for u, i := range run.user {
		out[i] = render(cfg, run.trace(u), run.infos[i])
	}
	return out, nil
}

func (x *Executor) TraceBlockParity(number uint64) ([]json.RawMessage, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	run, err := x.replayBlock(number, tracing.StepOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, len(run.data.Block.Transactions))
	for u, i := range run.user {
		loc := tracing.Locator{BlockHash: run.data.Hash, BlockNumber: number, TxHash: run.data.TxHash(i), TxIndex: i}
		out[i] = tracing.ParityJSON(run.trace(u).Recorder, run.infos[i], loc)
	}
	return out, nil
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
