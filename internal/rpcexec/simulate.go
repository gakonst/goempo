package rpcexec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"maps"
	"slices"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tracing"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const _ = uint(simulateGasCap - simulateCallsMax)

const _ = uint(simulateCallsMax - simulateBlocksMax)

const (
	simulateGasCap      = 50_000_000
	simulateCallsMax    = 1 << 12
	simulateBlocksMax   = 256
	refundQuotient      = 5
	refundQuotientLess1 = 4
	codeFeeCapTooLow    = -38012
)

var (
	ErrSimulateGas     = errors.New("rpcexec: block gas limit reached")
	ErrSimulateNumber  = errors.New("rpcexec: block numbers must increase")
	ErrSimulateTooMany = errors.New("rpcexec: too many simulated calls")
	emptyOmmersHash    = primitives.Hash{0x1d, 0xcc, 0x4d, 0xe8, 0xde, 0xc7, 0x5d, 0x7a, 0xab, 0x85, 0xb5, 0x67, 0xb6, 0xcc, 0xd4, 0x1a, 0xd3, 0x12, 0x45, 0x1b, 0x94, 0x8a, 0x74, 0x13, 0xf0, 0xa1, 0x42, 0xfd, 0x40, 0xd4, 0x93, 0x47}
)

type simRun struct {
	x        *Executor
	st       BlockState
	req      *rpc.SimulateRequest
	gasLeft  uint64
	prev     types.Header
	prevHash primitives.Hash
}

func (x *Executor) SimulateV1(block uint64, req *rpc.SimulateRequest) ([]rpc.SimulatedBlock, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	parent, ok := x.Chain.Block(block)
	if !ok {
		return nil, ErrNoBlock
	}
	bs, err := x.blockState(block)
	if err != nil {
		return nil, err
	}
	defer bs.Discard()
	run := &simRun{x: x, st: bs, req: req, gasLeft: simulateGasCap, prev: parent.Block.Header, prevHash: parent.Hash}
	out := make([]rpc.SimulatedBlock, 0, len(req.Blocks))
	calls := 0
	for i := range req.Blocks {
		calls += len(req.Blocks[i].Calls)
		if calls > simulateCallsMax {
			return nil, ErrSimulateTooMany
		}
		gap, err := run.gapBlocks(req.Blocks[i].Block.Number)
		if err != nil {
			return nil, err
		}
		out = append(out, gap...)
		sb, err := run.block(&req.Blocks[i])
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, nil
}

func (r *simRun) gapBlocks(number *uint64) ([]rpc.SimulatedBlock, error) {
	if number == nil || *number <= r.prev.Inner.Number+1 {
		return nil, nil
	}
	gap := *number - r.prev.Inner.Number - 1
	if gap > simulateBlocksMax {
		return nil, ErrSimulateTooMany
	}
	out := make([]rpc.SimulatedBlock, 0, gap)
	for range gap {
		sb, err := r.block(&rpc.SimBlock{})
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, nil
}

func (r *simRun) header(ov *rpc.BlockOverrides) (types.Header, error) {
	p := &r.prev
	h := types.Header{
		GeneralGasLimit:     p.GeneralGasLimit,
		SharedGasLimit:      p.SharedGasLimit,
		TimestampMillisPart: p.TimestampMillisPart,
		Inner: types.EthHeader{
			ParentHash:      r.prevHash,
			OmmersHash:      emptyOmmersHash,
			Beneficiary:     p.Inner.Beneficiary,
			Number:          p.Inner.Number + 1,
			GasLimit:        p.Inner.GasLimit,
			Timestamp:       p.Inner.Timestamp + 1,
			OptionalFields:  p.Inner.OptionalFields,
			WithdrawalsRoot: trie.EmptyRoot,
			RequestsHash:    sha256.Sum256(nil),
		},
	}
	if r.req.Validation {
		h.Inner.BaseFeePerGas = p.Inner.BaseFeePerGas
	}
	if ov.Number != nil {
		if *ov.Number <= p.Inner.Number {
			return types.Header{}, ErrSimulateNumber
		}
		h.Inner.Number = *ov.Number
	}
	if ov.Time != nil {
		h.Inner.Timestamp = *ov.Time
	}
	if ov.GasLimit != nil {
		h.Inner.GasLimit = *ov.GasLimit
	}
	if ov.FeeRecipient != nil {
		h.Inner.Beneficiary = *ov.FeeRecipient
	}
	if ov.BaseFee != nil && ov.BaseFee.IsUint64() {
		h.Inner.BaseFeePerGas = ov.BaseFee.Uint64()
	}
	if ov.PrevRandao != nil {
		h.Inner.MixHash = *ov.PrevRandao
	}
	return h, nil
}

func (r *simRun) applyState(state map[primitives.Address]rpc.AccountOverride) {
	for _, addr := range sortedAddresses(state) {
		o := state[addr]
		if o.Balance != nil {
			r.st.SubBalance(addr, r.st.Balance(addr))
			r.st.AddBalance(addr, *o.Balance)
		}
		if o.Nonce != nil {
			r.st.SetNonce(addr, *o.Nonce)
		}
		if o.HasCode {
			r.st.SetCode(addr, o.Code)
		}
		for _, k := range sortedSlots(o.State) {
			v := o.State[k]
			r.st.SetStorage(addr, k, u256.FromBytes(v[:]))
		}
		for _, k := range sortedSlots(o.StateDiff) {
			v := o.StateDiff[k]
			r.st.SetStorage(addr, k, u256.FromBytes(v[:]))
		}
	}
	r.st.FinalizeTx()
}

func (r *simRun) block(sb *rpc.SimBlock) (rpc.SimulatedBlock, error) {
	h, err := r.header(&sb.Block)
	if err != nil {
		return rpc.SimulatedBlock{}, err
	}
	r.applyState(sb.State)
	blk := &types.Block{Header: h, HasWithdrawals: true}
	senders := make([]primitives.Address, 0, len(sb.Calls))
	receipts := make([]types.Receipt, 0, len(sb.Calls))
	calls := make([]rpc.SimCall, 0, len(sb.Calls))
	var cumulative uint64
	for _, req := range sb.Calls {
		res, err := r.call(blk, req, cumulative)
		if err != nil {
			return rpc.SimulatedBlock{}, err
		}
		cumulative += res.call.GasUsed
		blk.Transactions = append(blk.Transactions, res.tx)
		senders = append(senders, sender(req))
		receipts = append(receipts, types.Receipt{
			Type:              res.tx.Type(),
			Success:           res.call.Success,
			CumulativeGasUsed: cumulative,
			Bloom:             types.LogsBloom(res.call.Logs),
			Logs:              res.call.Logs,
		})
		calls = append(calls, res.call)
	}
	r.seal(blk, receipts, cumulative)
	data := rpc.NewSimulatedBlockData(blk, receipts, senders)
	r.prev, r.prevHash = blk.Header, data.Hash
	return rpc.SimulatedBlock{Data: data, Calls: calls}, nil
}

func (r *simRun) seal(blk *types.Block, receipts []types.Receipt, gasUsed uint64) {
	txs := blk.TransactionEnvelopes()
	enc := make([][]byte, len(receipts))
	var bloom types.Bloom
	for i := range receipts {
		enc[i] = receipts[i].Encode()
		bloom.Or(&receipts[i].Bloom)
	}
	h := &blk.Header.Inner
	h.TransactionsRoot = trie.OrderedRoot(txs)
	h.ReceiptsRoot = trie.OrderedRoot(enc)
	h.LogsBloom = bloom
	h.GasUsed = gasUsed
}

type simResult struct {
	tx   types.Transaction
	call rpc.SimCall
}

func (r *simRun) call(blk *types.Block, req *rpc.CallRequest, cumulative uint64) (simResult, error) {
	remaining := blk.Header.Inner.GasLimit - min(cumulative, blk.Header.Inner.GasLimit)
	gas := min(remaining, r.gasLeft)
	if req.Gas != nil {
		if *req.Gas > remaining {
			return simResult{}, ErrSimulateGas
		}
		gas = min(*req.Gas, r.gasLeft)
	}
	from := sender(req)
	nonce := r.st.Nonce(from)
	if req.Nonce != nil {
		nonce = *req.Nonce
	}
	tx := buildTx(req, txShape{chainID: uint64(r.x.ChainID), gas: gas, nonce: nonce})
	session := tracing.NewSession(r.st, []evm.Address{from}, tracing.StepOptions{})
	strict := r.req.Validation
	if strict && priceOf(req).maxFee.Lt(u256.FromUint64(blk.Header.Inner.BaseFeePerGas)) {
		return simResult{}, &rpc.Error{Code: codeFeeCapTooLow, Message: "max fee per gas less than block base fee"}
	}
	sim := executor.Simulation{Sender: from, SkipNonceCheck: !strict, SkipBalanceCheck: !strict, SkipGasCap: true, SkipFeeCharge: !strict}
	out, err := r.x.Blocks.ApplyCall(session, blk, &tx, sim)
	if err != nil {
		return simResult{}, err
	}
	refund := r.st.Refund()
	trace := session.Close()
	r.st.FinalizeTx()
	if out.GasUsed > r.gasLeft {
		return simResult{}, ErrSimulateGas
	}
	r.gasLeft -= out.GasUsed
	c := rpc.SimCall{GasUsed: out.GasUsed, MaxUsedGas: spentGas(out.GasUsed, refund), Success: out.Success}
	end := lastFrame(trace.Recorder)
	ret, status := end.output, end.status
	if out.Success {
		c.ReturnData = ret
		c.Logs = toLogs(out.Logs)
	} else if status == evm.StatusRevert {
		c.Reverted, c.ReturnData = true, ret
	} else {
		c.Halt = haltText(status)
	}
	return simResult{tx: tx, call: c}, nil
}

func spentGas(used uint64, refund int64) uint64 {
	if refund <= 0 {
		return used
	}
	direct := used + uint64(refund)
	if uint64(refund) <= direct/refundQuotient {
		return direct
	}
	return used * refundQuotient / refundQuotientLess1
}

type frameEnd struct {
	output []byte
	status evm.Status
}

func lastFrame(rec *tracing.Recorder) frameEnd {
	if len(rec.Roots) == 0 {
		return frameEnd{status: evm.StatusStop}
	}
	for _, r := range rec.Roots {
		if n := &rec.Nodes[r]; !n.Success() {
			return frameEnd{output: n.Output, status: n.Status}
		}
	}
	n := &rec.Nodes[rec.Roots[len(rec.Roots)-1]]
	return frameEnd{output: n.Output, status: n.Status}
}

func toLogs(in []evm.Log) []types.Log {
	out := make([]types.Log, len(in))
	for i := range in {
		out[i] = types.Log{Address: in[i].Address, Topics: in[i].Topics, Data: in[i].Data}
	}
	return out
}

func sortedAddresses(m map[primitives.Address]rpc.AccountOverride) []primitives.Address {
	out := slices.Collect(maps.Keys(m))
	slices.SortFunc(out, func(a, b primitives.Address) int { return bytes.Compare(a[:], b[:]) })
	return out
}

func sortedSlots(m map[primitives.Hash]primitives.Hash) []primitives.Hash {
	out := slices.Collect(maps.Keys(m))
	slices.SortFunc(out, func(a, b primitives.Hash) int { return bytes.Compare(a[:], b[:]) })
	return out
}
