package evm

import (
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const selfDestructRefund = 24000

type callArgs struct {
	kind  CallKind
	gas   u256.Int
	addr  Address
	value u256.Int
	in    memRange
	out   memRange
}

func popCallArgs(f *Frame, kind CallKind) (callArgs, bool) {
	a := callArgs{kind: kind}
	a.gas = f.Pop()
	a.addr = wordAddress(f.Pop())
	if kind == KindCall || kind == KindCallCode {
		a.value = f.Pop()
	}
	inOffset := f.Pop()
	inSize := f.Pop()
	outOffset := f.Pop()
	outSize := f.Pop()
	in, ok := toRange(span{offset: inOffset, size: inSize})
	if !ok {
		return a, false
	}
	out, ok := toRange(span{offset: outOffset, size: outSize})
	if !ok {
		return a, false
	}
	a.in = in
	a.out = out
	return a, true
}

func callGasLimit(available uint64, requested u256.Int) uint64 {
	capped := available - available/64
	if requested.IsUint64() && requested.Uint64() < capped {
		return requested.Uint64()
	}
	return capped
}

func opCall(e *EVM, f *Frame) Status { return e.doCall(f, KindCall) }

func opCallCode(e *EVM, f *Frame) Status { return e.doCall(f, KindCallCode) }

func opDelegateCall(e *EVM, f *Frame) Status { return e.doCall(f, KindDelegateCall) }

func opStaticCall(e *EVM, f *Frame) Status { return e.doCall(f, KindStaticCall) }

func (e *EVM) doCall(f *Frame, kind CallKind) Status {
	a, ok := popCallArgs(f, kind)
	if !ok {
		return StatusOutOfGas
	}
	if kind == KindCall && f.static && !a.value.IsZero() {
		return StatusStaticViolation
	}
	cost := e.accountAccessCost(a.addr)
	if !f.UseGas(cost) {
		return StatusOutOfGas
	}
	if !f.expand(e.gas, max(a.in.end(), a.out.end())) {
		return StatusOutOfGas
	}
	extra := uint64(0)
	if !a.value.IsZero() {
		extra += e.gas.CallValue
		if kind == KindCall && e.State.Empty(a.addr) {
			extra += e.gas.NewAccount
		}
	}
	codeSource := a.addr
	if e.Config.Fork >= Prague {
		target, delegated := DelegationTarget(e.State.Code(a.addr))
		if delegated {
			extra += e.accountAccessCost(target)
			codeSource = target
		}
	}
	if !f.UseGas(extra) {
		return StatusOutOfGas
	}
	callGas := callGasLimit(f.gas, a.gas)
	f.gas -= callGas
	if !a.value.IsZero() {
		callGas += e.gas.CallStipend
	}
	req := callRequest{
		kind:        kind,
		caller:      f.address,
		address:     a.addr,
		codeAddress: a.addr,
		codeSource:  codeSource,
		value:       a.value,
		input:       f.memSlice(a.in),
		gas:         callGas,
		depth:       f.depth + 1,
		static:      f.static,
	}
	switch kind {
	case KindCall:
	case KindCallCode:
		req.address = f.address
	case KindDelegateCall:
		req.caller = f.caller
		req.address = f.address
		req.value = f.value
	case KindStaticCall:
		req.static = true
	default:
		assert.Unreachable("CallKind: unhandled value")
	}
	f.pending = pendingCall{kind: kind, retOffset: a.out.offset, retSize: a.out.size}
	res, pushed := e.dispatch(req)
	if pushed {
		return StatusCall
	}
	e.deliver(f, res)
	return StatusContinue
}

func opCreate(e *EVM, f *Frame) Status { return e.doCreate(f, KindCreate) }

func opCreate2(e *EVM, f *Frame) Status { return e.doCreate(f, KindCreate2) }

func (e *EVM) doCreate(f *Frame, kind CallKind) Status {
	if f.static {
		return StatusStaticViolation
	}
	value := f.Pop()
	offset := f.Pop()
	size := f.Pop()
	var salt u256.Int
	if kind == KindCreate2 {
		salt = f.Pop()
	}
	r, ok := toRange(span{offset: offset, size: size})
	if !ok {
		return StatusOutOfGas
	}
	if e.gas.MaxInitCodeSize > 0 && r.size > uint64(e.gas.MaxInitCodeSize) {
		return StatusInitCodeSizeExceeded
	}
	words := toWords(r.size)
	cost := words * e.gas.InitCodeWord
	if kind == KindCreate2 {
		cost += words * e.gas.KeccakWord
	}
	if !f.UseGas(cost) {
		return StatusOutOfGas
	}
	if !f.expandRange(e.gas, r) {
		return StatusOutOfGas
	}
	callGas := f.gas - f.gas/64
	f.gas -= callGas
	req := callRequest{
		kind:   kind,
		caller: f.address,
		value:  value,
		salt:   salt,
		input:  f.memSlice(r),
		gas:    callGas,
		depth:  f.depth + 1,
	}
	f.pending = pendingCall{kind: kind}
	res, pushed := e.dispatch(req)
	if pushed {
		return StatusCall
	}
	e.deliver(f, res)
	return StatusContinue
}

func (e *EVM) setOutput(f *Frame) bool {
	offset := f.Pop()
	size := f.Pop()
	r, ok := toRange(span{offset: offset, size: size})
	if !ok {
		return false
	}
	if !f.expandRange(e.gas, r) {
		return false
	}
	f.output = make([]byte, r.size)
	copy(f.output, f.memSlice(r))
	return true
}

func opReturn(e *EVM, f *Frame) Status {
	if !e.setOutput(f) {
		return StatusOutOfGas
	}
	return StatusReturn
}

func opRevert(e *EVM, f *Frame) Status {
	if !e.setOutput(f) {
		return StatusOutOfGas
	}
	return StatusRevert
}

func opSelfDestruct(e *EVM, f *Frame) Status {
	if f.static {
		return StatusStaticViolation
	}
	beneficiary := wordAddress(f.Pop())
	cost := uint64(0)
	if e.gas.AccessLists && !e.State.AddressWarm(beneficiary) {
		e.State.WarmAddress(beneficiary)
		cost = e.gas.ColdAccountAccess
	}
	balance := e.State.Balance(f.address)
	if !balance.IsZero() && e.State.Empty(beneficiary) {
		cost += e.gas.NewAccount
	}
	if !f.UseGas(cost) {
		return StatusOutOfGas
	}
	if e.Config.Fork < London && !e.State.HasSelfDestructed(f.address) {
		e.State.AddRefund(selfDestructRefund)
	}
	e.State.SubBalance(f.address, balance)
	e.State.AddBalance(beneficiary, balance)
	if e.Config.Fork < Cancun || e.State.CreatedInTx(f.address) {
		e.State.SelfDestruct(f.address)
	}
	return StatusSelfDestruct
}
