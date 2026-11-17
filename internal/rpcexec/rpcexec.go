package rpcexec

import (
	"sync"

	"errors"
	"github.com/gakonst/goempo/internal/tempo/executor"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	txBaseGas            = 21_000
	txTokenGas           = 4
	nonZeroTokens        = 4
	txFloorTokenGas      = 10
	accessListAddressGas = 2_400
	accessListSlotGas    = 1_900
	initCodeWordGas      = 2
	callStipend          = 2_300
	estimateRatioPermil  = 15
	permil               = 1000
	searchStepsMax       = 64
	blockHashWindow      = 256
)

const (
	_ = uint(txBaseGas - accessListAddressGas)
	_ = uint(permil - estimateRatioPermil)
	_ = uint(blockHashWindow - searchStepsMax)
	_ = uint(accessListAddressGas - accessListSlotGas)
	_ = uint(callStipend - initCodeWordGas)
	_ = uint(txFloorTokenGas - txTokenGas)
	_ = uint(nonZeroTokens - initCodeWordGas)
)

var (
	ErrNoBlock   = errors.New("rpcexec: block is not available")
	ErrNoTracer  = errors.New("rpcexec: tracing is not implemented")
	ErrGasTooLow = errors.New("rpcexec: gas required exceeds allowance")
)

type State interface {
	evm.State
}

type StateSource interface {
	ExecState(number uint64) (State, error)
}

type Executor struct {
	ChainID hardfork.ChainID
	Chain   rpc.Chain
	States  StateSource
	Blocks  *executor.Executor
	mu      sync.Mutex
}

type env struct {
	header *types.Block
	st     State
	rules  chain.Rules
	cfg    *evm.Config
	block  *evm.BlockContext
}

func (x *Executor) envAt(number uint64) (env, error) {
	b, ok := x.Chain.Block(number)
	if !ok {
		return env{}, ErrNoBlock
	}
	st, err := x.States.ExecState(number)
	if err != nil {
		return env{}, err
	}
	h := &b.Block.Header
	rules := chain.RulesAt(x.ChainID, h.Inner.Timestamp)
	set := precompiles.NewSet(rules.Fork, rules.EthereumPrecompiles())
	ts := chain.Timestamp{Seconds: h.Inner.Timestamp, MillisPart: h.TimestampMillisPart}
	blk := &evm.BlockContext{
		Coinbase:   h.Inner.Beneficiary,
		Number:     h.Inner.Number,
		Time:       h.Inner.Timestamp,
		GasLimit:   h.Inner.GasLimit,
		BaseFee:    u256.Zero(),
		PrevRandao: h.Inner.MixHash,
		GetHash:    x.blockHash(number),
	}
	return env{header: b.Block, st: st, rules: rules, cfg: rules.Config(ts.Millis(), set), block: blk}, nil
}

func (x *Executor) blockHash(current uint64) func(uint64) primitives.Hash {
	return func(n uint64) primitives.Hash {
		if n >= current || current-n > blockHashWindow {
			return primitives.Hash{}
		}
		b, ok := x.Chain.Block(n)
		if !ok {
			return primitives.Hash{}
		}
		return b.Hash
	}
}

type outcome struct {
	res     evm.Result
	gasUsed uint64
}

func sender(req *rpc.CallRequest) primitives.Address {
	if req.From == nil {
		return primitives.Address{}
	}
	return *req.From
}

func calldataTokens(input []byte) uint64 {
	var tokens uint64
	for _, b := range input {
		if b == 0 {
			tokens++
		} else {
			tokens += nonZeroTokens
		}
	}
	return tokens
}

type intrinsicGas struct {
	gas   uint64
	floor uint64
}

func intrinsic(e *env, req *rpc.CallRequest, nonce uint64) intrinsicGas {
	tokens := calldataTokens(req.Input)
	gas := uint64(txBaseGas) + tokens*txTokenGas
	if req.To == nil {
		gas += e.rules.GasTable().Create + (uint64(len(req.Input))+31)/32*initCodeWordGas
	}
	for _, t := range req.AccessList {
		gas += accessListAddressGas + uint64(len(t.StorageKeys))*accessListSlotGas
	}
	if nonce == 0 && e.rules.AtLeast(hardfork.T1) {
		gas += e.rules.NewAccountNonceGas()
	}
	return intrinsicGas{gas: gas, floor: txBaseGas + tokens*txFloorTokenGas}
}

func (x *Executor) run(e *env, req *rpc.CallRequest, gasLimit uint64) (outcome, error) {
	from := sender(req)
	need := intrinsic(e, req, e.st.Nonce(from))
	if gasLimit < max(need.gas, need.floor) {
		return outcome{}, ErrGasTooLow
	}
	snap := e.st.Snapshot()
	defer e.st.RevertToSnapshot(snap)
	e.st.WarmAddress(from)
	e.st.WarmAddress(e.block.Coinbase)
	for _, t := range req.AccessList {
		e.st.WarmAddress(t.Address)
		for _, k := range t.StorageKeys {
			e.st.WarmSlot(t.Address, k)
		}
	}
	machine := evm.New(evm.Context{State: e.st, Block: e.block, Tx: &evm.TxContext{Origin: from}, Config: e.cfg})
	msg := evm.Message{Kind: evm.KindCall, Caller: from, Value: req.Value, Input: req.Input, Gas: gasLimit - need.gas}
	if req.To == nil {
		msg.Kind = evm.KindCreate
		e.st.SetNonce(from, e.st.Nonce(from))
	} else {
		msg.To = *req.To
		e.st.WarmAddress(*req.To)
	}
	res := machine.Execute(msg)
	used := gasLimit - res.GasLeft
	refund := e.st.Refund()
	capped := used / e.cfg.Gas.MaxRefundQuotient
	if res.Status.Success() {
		if refund < 0 || uint64(refund) > capped {
			used -= capped
		} else {
			used -= uint64(refund)
		}
	}
	return outcome{res: res, gasUsed: max(used, need.floor)}, nil
}

func (x *Executor) gasLimit(e *env, req *rpc.CallRequest, gasCap uint64) uint64 {
	limit := min(e.block.GasLimit, gasCap)
	if c := e.rules.TxGasLimitCap(); c > 0 {
		limit = min(limit, c)
	}
	if req.Gas != nil {
		limit = min(*req.Gas, limit)
	}
	return limit
}

func (x *Executor) Call(block uint64, req *rpc.CallRequest, gasCap uint64) (rpc.CallResult, error) {
	e, err := x.envAt(block)
	if err != nil {
		return rpc.CallResult{}, err
	}
	out, err := x.execute(&e, req, x.gasLimit(&e, req, gasCap))
	if err != nil {
		return rpc.CallResult{}, err
	}
	return result(out), nil
}

func result(out outcome) rpc.CallResult {
	r := rpc.CallResult{Return: out.res.Output, GasUsed: out.gasUsed}
	switch {
	case out.res.Status.Success():
	case out.res.Status == evm.StatusRevert:
		r.Reverted = true
	default:
		r.Halt = haltText(out.res.Status)
	}
	return r
}

func haltText(s evm.Status) string { return s.String() }

type gasRange struct {
	lowest  uint64
	highest uint64
}

func (g *gasRange) update(out outcome, limit uint64) {
	if out.res.Status.Success() {
		g.highest = limit
		return
	}
	g.lowest = limit
}

func (g *gasRange) settled() bool {
	if g.lowest+1 >= g.highest {
		return true
	}
	return (g.highest-g.lowest)*permil < g.highest*estimateRatioPermil
}

func (x *Executor) EstimateGas(block uint64, req *rpc.CallRequest, gasCap uint64) (uint64, error) {
	e, err := x.envAt(block)
	if err != nil {
		return 0, err
	}
	highest := x.gasLimit(&e, req, gasCap)
	out, err := x.execute(&e, req, highest)
	if err != nil {
		return 0, err
	}
	if !out.res.Status.Success() {
		return 0, failure(out)
	}
	g := gasRange{lowest: out.gasUsed - 1, highest: highest}
	optimistic := (out.gasUsed + uint64(max(e.st.Refund(), 0)) + callStipend) * 64 / 63
	if optimistic < g.highest {
		if o, err := x.execute(&e, req, optimistic); err == nil {
			g.update(o, optimistic)
		}
	}
	mid := min(out.gasUsed*3, (g.highest+g.lowest)/2)
	for range searchStepsMax {
		if g.settled() {
			break
		}
		o, err := x.execute(&e, req, mid)
		if err != nil {
			g.lowest = mid
		} else {
			g.update(o, mid)
		}
		mid = (g.highest + g.lowest) / 2
	}
	return g.highest, nil
}

func failure(out outcome) error {
	if out.res.Status == evm.StatusRevert {
		return rpc.RevertError(out.res.Output)
	}
	return errors.New(haltText(out.res.Status))
}
