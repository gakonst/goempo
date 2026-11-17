package rpc

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

const traceFilterBlocksMax = 100

const _ = uint(simulateBlocksMax - traceFilterBlocksMax)

type DiffMode bool

const (
	NoStateDiff   DiffMode = false
	WithStateDiff DiffMode = true
)

type ParityTrace struct {
	From      primitives.Address
	To        primitives.Address
	ToKnown   bool
	Addr      []int
	Plain     []byte
	Localized []byte
}

type TxReplay struct {
	Traces    []ParityTrace
	Output    []byte
	StateDiff []byte
}

type CallBundle struct {
	Calls  []*CallRequest
	Number *uint64
}

type traceTypes struct {
	trace     bool
	stateDiff bool
	vmTrace   bool
}

func traceTypesParam(raw json.RawMessage) (traceTypes, error) {
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return traceTypes{}, invalidParams("invalid trace types")
	}
	var t traceTypes
	for _, n := range names {
		switch n {
		case "trace":
			t.trace = true
		case "stateDiff":
			t.stateDiff = true
		case "vmTrace":
			t.vmTrace = true
		default:
			return traceTypes{}, invalidParams("unknown variant `" + n + "`, expected one of `vmTrace`, `trace`, `stateDiff`")
		}
	}
	return t, nil
}

func systemPlainTrace(w *writer, d *types.LegacyTx) {
	w.objOpen()
	w.kStr("type", "call")
	w.key("action")
	w.objOpen()
	w.kAddress("from", primitives.Address{})
	w.kStr("callType", "call")
	w.kQuantity("gas", systemCallGasLimit)
	w.kData("input", d.Data)
	w.kAddress("to", systemTo(d))
	w.kQuantity256("value", d.Value)
	w.objClose()
	w.key("result")
	w.objOpen()
	w.kQuantity("gasUsed", 0)
	w.kData("output", nil)
	w.objClose()
	w.key("subtraces")
	w.integer(0)
	w.key("traceAddress")
	w.raw(resultEmpty)
	w.objClose()
}

type traceResult struct {
	replay *TxReplay
	sys    *types.LegacyTx
	types  traceTypes
	hash   *primitives.Hash
}

func writeTraceResults(w *writer, res traceResult) {
	r, sys, tt, hash := res.replay, res.sys, res.types, res.hash
	w.objOpen()
	any := tt.trace || tt.stateDiff || tt.vmTrace
	switch {
	case r != nil:
		w.kData("output", r.Output)
	default:
		w.kData("output", nil)
	}
	w.key("stateDiff")
	switch {
	case !tt.stateDiff || !any:
		w.raw(resultNull)
	case r != nil:
		w.raw(r.StateDiff)
	default:
		w.raw([]byte("{}"))
	}
	w.key("trace")
	w.arrOpen()
	if tt.trace {
		if r != nil {
			for i := range r.Traces {
				w.raw(r.Traces[i].Plain)
			}
		} else if sys != nil {
			systemPlainTrace(w, sys)
		}
	}
	w.arrClose()
	w.key("vmTrace")
	w.raw(resultNull)
	if hash != nil {
		w.kHash("transactionHash", *hash)
	}
	w.objClose()
}

func (s *Server) replayBlock(b *BlockData, diff DiffMode) ([]*TxReplay, error) {
	if systemOnly(b) {
		return make([]*TxReplay, len(b.Block.Transactions)), nil
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	out, err := s.exec.ReplayBlock(b.Number(), diff)
	if err != nil {
		return nil, executionError(err)
	}
	return out, nil
}

func systemTx(tx types.Transaction) *types.LegacyTx {
	if !isSystemTx(tx) {
		return nil
	}
	return tx.Data.(*types.LegacyTx)
}

func writeLocalizedTx(w *writer, b *BlockData, i int, r *TxReplay) {
	if d := systemTx(b.Block.Transactions[i]); d != nil {
		writeParityCall(w, b, i, d)
		return
	}
	if r == nil {
		return
	}
	for j := range r.Traces {
		w.raw(r.Traces[j].Localized)
	}
}

type txTarget struct {
	block *BlockData
	index int
}

func (s *Server) txTarget(p *params) (*txTarget, error) {
	raw, err := p.required("hash")
	if err != nil {
		return nil, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return nil, err
	}
	loc, ok := s.chain.TxLocation(h)
	if !ok {
		return nil, nil
	}
	b, ok := s.chain.Block(loc.Block)
	if !ok {
		return nil, nil
	}
	return &txTarget{block: b, index: loc.Index}, nil
}

func traceTransaction(s *Server, p *params) ([]byte, error) {
	tgt, err := s.txTarget(p)
	if err != nil || tgt == nil {
		return resultNull, err
	}
	b, i := tgt.block, tgt.index
	replays, err := s.replayBlock(b, NoStateDiff)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.arrOpen()
		writeLocalizedTx(w, b, i, replays[i])
		w.arrClose()
	}), nil
}

func traceGet(s *Server, p *params) ([]byte, error) {
	tgt, err := s.txTarget(p)
	if err != nil || tgt == nil {
		return resultNull, err
	}
	b, i := tgt.block, tgt.index
	raw, err := p.required("indices")
	if err != nil {
		return nil, err
	}
	var idx []json.RawMessage
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, invalidParams("invalid indices")
	}
	want := make([]int, len(idx))
	for k, r := range idx {
		v, err := quantityParam(r)
		if err != nil {
			return nil, err
		}
		want[k] = int(v)
	}
	if d := systemTx(b.Block.Transactions[i]); d != nil {
		if len(want) != 0 {
			return resultNull, nil
		}
		return encode(func(w *writer) { writeParityCall(w, b, i, d) }), nil
	}
	replays, err := s.replayBlock(b, NoStateDiff)
	if err != nil {
		return nil, err
	}
	r := replays[i]
	if r == nil {
		return resultNull, nil
	}
	for j := range r.Traces {
		if slices.Equal(r.Traces[j].Addr, want) {
			return r.Traces[j].Localized, nil
		}
	}
	return resultNull, nil
}

func traceReplayTransaction(s *Server, p *params) ([]byte, error) {
	tgt, err := s.txTarget(p)
	if err != nil {
		return nil, err
	}
	raw, err := p.required("trace_types")
	if err != nil {
		return nil, err
	}
	tt, err := traceTypesParam(raw)
	if err != nil {
		return nil, err
	}
	if tgt == nil {
		return resultNull, nil
	}
	b, i := tgt.block, tgt.index
	replays, err := s.replayBlock(b, DiffMode(tt.stateDiff))
	if err != nil {
		return nil, err
	}
	h := b.TxHash(i)
	return encode(func(w *writer) {
		writeTraceResults(w, traceResult{replay: replays[i], sys: systemTx(b.Block.Transactions[i]), types: tt, hash: &h})
	}), nil
}

func traceReplayBlockTransactions(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block")
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return nil, err
	}
	rawTypes, err := p.required("trace_types")
	if err != nil {
		return nil, err
	}
	tt, err := traceTypesParam(rawTypes)
	if err != nil {
		return nil, err
	}
	b := s.blockFor(ref)
	if b == nil {
		return resultNull, nil
	}
	replays, err := s.replayBlock(b, DiffMode(tt.stateDiff))
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for i, tx := range b.Block.Transactions {
			h := b.TxHash(i)
			writeTraceResults(w, traceResult{replay: replays[i], sys: systemTx(tx), types: tt, hash: &h})
		}
		w.arrClose()
	}), nil
}

type rawTraceFilter struct {
	FromBlock   json.RawMessage `json:"fromBlock"`
	ToBlock     json.RawMessage `json:"toBlock"`
	FromAddress []string        `json:"fromAddress"`
	ToAddress   []string        `json:"toAddress"`
	Mode        string          `json:"mode"`
	After       *uint64         `json:"after"`
	Count       *uint64         `json:"count"`
}

type traceAddrs map[primitives.Address]bool

func (a traceAddrs) matches(addr primitives.Address) bool {
	return len(a) == 0 || a[addr]
}

func addressSetParam(list []string) (traceAddrs, error) {
	out := traceAddrs{}
	for _, s := range list {
		a, err := addressParam(json.RawMessage(`"` + s + `"`))
		if err != nil {
			return nil, err
		}
		out[a] = true
	}
	return out, nil
}

type traceMatcher struct {
	from  traceAddrs
	to    traceAddrs
	union bool
}

func (m *traceMatcher) matches(t *ParityTrace) bool {
	fromOK := m.from.matches(t.From)
	toOK := len(m.to) == 0
	if t.ToKnown {
		toOK = m.to.matches(t.To)
	}
	if !m.union {
		return fromOK && toOK
	}
	if len(m.from) == 0 {
		return toOK
	}
	if len(m.to) == 0 {
		return fromOK
	}
	return fromOK || toOK
}

func (s *Server) filterBound(raw json.RawMessage) (uint64, error) {
	if !present(raw) {
		return s.chain.Head(), nil
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return 0, err
	}
	return s.stateBlock(ref)
}

func traceFilter(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("filter")
	if err != nil {
		return nil, err
	}
	var f rawTraceFilter
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, invalidParams(err.Error())
	}
	start, err := s.filterBound(f.FromBlock)
	if err != nil {
		return nil, err
	}
	end, err := s.filterBound(f.ToBlock)
	if err != nil {
		return nil, err
	}
	if start > end {
		return nil, newError(CodeInvalidParams, "invalid parameters: fromBlock cannot be greater than toBlock")
	}
	if end-start > traceFilterBlocksMax {
		return nil, newError(CodeInvalidParams, "Block range too large; currently limited to 100 blocks")
	}
	m := traceMatcher{union: f.Mode == "union"}
	if m.from, err = addressSetParam(f.FromAddress); err != nil {
		return nil, err
	}
	if m.to, err = addressSetParam(f.ToAddress); err != nil {
		return nil, err
	}
	span := min(end-start, traceFilterBlocksMax)
	var out [][]byte
	for k := uint64(0); k <= span; k++ {
		b, ok := s.chain.Block(start + k)
		if !ok {
			continue
		}
		matched, err := s.blockMatches(b, &m)
		if err != nil {
			return nil, err
		}
		out = append(out, matched...)
	}
	out = paginate(out, page{after: f.After, count: f.Count})
	return encode(func(w *writer) {
		w.arrOpen()
		for _, t := range out {
			w.raw(t)
		}
		w.arrClose()
	}), nil
}

func (s *Server) blockMatches(b *BlockData, m *traceMatcher) ([][]byte, error) {
	replays, err := s.replayBlock(b, NoStateDiff)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for i, tx := range b.Block.Transactions {
		if d := systemTx(tx); d != nil {
			t := ParityTrace{To: systemTo(d), ToKnown: true}
			if m.matches(&t) {
				out = append(out, encode(func(w *writer) { writeParityCall(w, b, i, d) }))
			}
			continue
		}
		if replays[i] == nil {
			continue
		}
		for j := range replays[i].Traces {
			if m.matches(&replays[i].Traces[j]) {
				out = append(out, replays[i].Traces[j].Localized)
			}
		}
	}
	return out, nil
}

type page struct {
	after *uint64
	count *uint64
}

func paginate(all [][]byte, pg page) [][]byte {
	after, count := pg.after, pg.count
	if after != nil {
		if *after >= uint64(len(all)) {
			return nil
		}
		all = all[*after:]
	}
	if count != nil && *count < uint64(len(all)) {
		all = all[:*count]
	}
	return all
}

func traceCallParity(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("call")
	if err != nil {
		return nil, err
	}
	req, err := callParam(raw)
	if err != nil {
		return nil, err
	}
	rawTypes, err := p.required("trace_types")
	if err != nil {
		return nil, err
	}
	tt, err := traceTypesParam(rawTypes)
	if err != nil {
		return nil, err
	}
	n, err := s.optionalStateBlock(p, "block")
	if err != nil {
		return nil, err
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	res, err := s.exec.TraceCalls(n, []*CallRequest{req}, DiffMode(tt.stateDiff))
	if err != nil {
		return nil, executionError(err)
	}
	return encode(func(w *writer) { writeTraceResults(w, traceResult{replay: &res[0], types: tt}) }), nil
}

func traceCallMany(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("calls")
	if err != nil {
		return nil, err
	}
	var pairs [][]json.RawMessage
	if err := json.Unmarshal(raw, &pairs); err != nil {
		return nil, invalidParams("invalid calls")
	}
	reqs := make([]*CallRequest, len(pairs))
	types := make([]traceTypes, len(pairs))
	diff := false
	for i, pair := range pairs {
		if len(pair) != 2 {
			return nil, invalidParams("invalid calls")
		}
		if reqs[i], err = callParam(pair[0]); err != nil {
			return nil, err
		}
		if types[i], err = traceTypesParam(pair[1]); err != nil {
			return nil, err
		}
		diff = diff || types[i].stateDiff
	}
	n, err := s.optionalStateBlock(p, "block")
	if err != nil {
		return nil, err
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	res, err := s.exec.TraceCalls(n, reqs, DiffMode(diff))
	if err != nil {
		return nil, executionError(err)
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for i := range res {
			writeTraceResults(w, traceResult{replay: &res[i], types: types[i]})
		}
		w.arrClose()
	}), nil
}

func (s *Server) optionalStateBlock(p *params, name string) (uint64, error) {
	ref, err := blockIDParam(p.optional(name))
	if err != nil {
		return 0, err
	}
	return s.stateBlock(ref)
}

type rawBundle struct {
	Transactions  []json.RawMessage `json:"transactions"`
	BlockOverride *rawOverrides     `json:"blockOverride"`
}

type rawStateContext struct {
	BlockNumber      json.RawMessage `json:"blockNumber"`
	TransactionIndex json.RawMessage `json:"transactionIndex"`
}

type bundleArgs struct {
	bundles []CallBundle
	block   uint64
}

func callBundlesArgs(s *Server, p *params) (bundleArgs, error) {
	raw, err := p.required("bundles")
	if err != nil {
		return bundleArgs{}, err
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '[' {
		return bundleArgs{}, typeError(t, "a sequence")
	}
	var rb []rawBundle
	if err := json.Unmarshal(raw, &rb); err != nil {
		return bundleArgs{}, invalidParams(err.Error())
	}
	bundles := make([]CallBundle, len(rb))
	for i := range rb {
		for _, t := range rb[i].Transactions {
			req, err := callParam(t)
			if err != nil {
				return bundleArgs{}, err
			}
			bundles[i].Calls = append(bundles[i].Calls, req)
		}
		ov, err := parseOverrides(rb[i].BlockOverride)
		if err != nil {
			return bundleArgs{}, err
		}
		bundles[i].Number = ov.Number
	}
	var sc rawStateContext
	if ctx := p.optional("state_context"); present(ctx) {
		if err := json.Unmarshal(ctx, &sc); err != nil {
			return bundleArgs{}, invalidParams(err.Error())
		}
	}
	ref, err := blockIDParam(sc.BlockNumber)
	if err != nil {
		return bundleArgs{}, err
	}
	n, err := s.stateBlock(ref)
	if err != nil {
		return bundleArgs{}, err
	}
	return bundleArgs{bundles: bundles, block: n}, nil
}

func ethCallMany(s *Server, p *params) ([]byte, error) {
	args, err := callBundlesArgs(s, p)
	if err != nil {
		return nil, err
	}
	bundles, n := args.bundles, args.block
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	res, err := s.exec.CallMany(n, bundles)
	if err != nil {
		return nil, executionError(err)
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for _, bundle := range res {
			w.arrOpen()
			for _, r := range bundle {
				w.objOpen()
				switch {
				case r.Reverted:
					w.kStr("error", RevertDebugString(r.Return))
				case r.Halt != "":
					w.kStr("error", r.Halt)
				default:
					w.kData("value", r.Return)
				}
				w.objClose()
			}
			w.arrClose()
		}
		w.arrClose()
	}), nil
}
