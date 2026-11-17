package precompiles

import (
	"errors"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

type handler func(c *Context, a []abiValue) ([]byte, error)

type method struct {
	sig     string
	args    []abiType
	mutates flag
	since   hardfork.Fork
	until   hardfork.Fork
	fn      handler
}

type methodTable struct {
	bySelector map[uint32]*method
}

func newTable(methods ...method) *methodTable {
	t := &methodTable{bySelector: make(map[uint32]*method, len(methods))}
	for i := range methods {
		m := &methods[i]
		key := selectorWord(selector(m.sig))
		_, dup := t.bySelector[key]
		assert.Ok(!dup, "duplicate precompile selector")
		t.bySelector[key] = m
	}
	return t
}

func view(sig string, args []abiType, fn handler) method {
	return method{sig: sig, args: args, fn: fn, until: hardfork.Latest() + 1}
}

func mutate(sig string, args []abiType, fn handler) method {
	return method{sig: sig, args: args, mutates: true, fn: fn, until: hardfork.Latest() + 1}
}

func (m method) from(f hardfork.Fork) method {
	m.since = f
	return m
}

func (m method) before(f hardfork.Fork) method {
	m.until = f
	return m
}

var (
	selUnknownFunction   = selector("UnknownFunctionSelector(bytes4)")
	selStaticNotAllowed  = selector("StaticCallNotAllowed()")
	selDelegateForbidden = selector("DelegateCallNotAllowed()")
	selPanic             = selector("Panic(uint256)")
)

const (
	panicUnderOverflow  = 0x11
	panicEnumConversion = 0x21
)

func underOverflow() error { return revert(abiError(selPanic, wordOfU64(panicUnderOverflow))) }

func errSel(sig string) error { return revert(abiError(selector(sig))) }

func (t *methodTable) dispatch(c *Context, input []byte) ([]byte, error) {
	if len(input) < 4 {
		if c.at(hardfork.T1) {
			return nil, revert(nil)
		}
		return nil, errFatal
	}
	sel := [4]byte(input[:4])
	m, ok := t.bySelector[selectorWord(sel)]
	if !ok || c.Fork < m.since || c.Fork >= m.until {
		var w Hash
		copy(w[:], sel[:])
		return nil, revert(abiError(selUnknownFunction, w))
	}
	mode := decodeMode{strict: flag(c.at(hardfork.T11)), trailing: flag(c.at(hardfork.T12))}
	args, err := decodeABI(input[4:], m.args, mode)
	if err != nil {
		return nil, revert(nil)
	}
	if bool(m.mutates) && c.Static {
		if !c.at(hardfork.T12) {
			return nil, revert(abiError(selStaticNotAllowed))
		}
		return nil, errStatic
	}
	return m.fn(c, args)
}

func inputCost(fork hardfork.Fork, n int) uint64 {
	per := gasInputWordPreT11
	if fork >= hardfork.T11 {
		per = gasInputWordT11
	}
	return (uint64(n) + 31) / 32 * per
}

type Set struct {
	Fork hardfork.Fork
	Base evm.PrecompileLookup
	Env  *TxEnv
}

func NewSet(fork hardfork.Fork, base evm.PrecompileLookup) *Set {
	return &Set{Fork: fork, Base: base, Env: &TxEnv{}}
}

func (s *Set) Lookup(addr Address) (evm.Precompile, bool) {
	if s.Base != nil {
		if p, ok := s.Base.Lookup(addr); ok {
			return p, true
		}
	}
	if !IsPrecompile(addr, s.Fork) {
		return nil, false
	}
	return &tempoPrecompile{set: s, address: addr}, true
}

type tempoPrecompile struct {
	set     *Set
	address Address
}

func (p *tempoPrecompile) Run(call *evm.PrecompileCall) evm.PrecompileResult {
	if call.Address != call.CodeAddress {
		return evm.PrecompileResult{Output: abiError(selDelegateForbidden), Status: evm.StatusRevert}
	}
	fork := p.set.Fork
	if inputCost(fork, len(call.Input)) > call.Gas {
		return evm.PrecompileResult{GasUsed: call.Gas, Status: evm.StatusOutOfGas}
	}
	c := NewContext(call.EVM.State, fork, call.Gas)
	c.Block = call.EVM.Block
	c.Tx = call.EVM.Tx
	c.Env = p.set.Env
	c.Caller = call.Caller
	c.ChainID = call.EVM.Config.ChainID
	c.Static = call.Static || call.Kind == evm.KindStaticCall
	out, err := Call(c, p.address, call.Input)
	return finish(c, outcome{out: out, err: err})
}

type outcome struct {
	out []byte
	err error
}

func finish(c *Context, o outcome) evm.PrecompileResult {
	if o.err == nil {
		c.State.AddRefund(c.refund)
		return evm.PrecompileResult{Output: o.out, GasUsed: c.gasUsed, Status: evm.StatusReturn}
	}
	var rv *Revert
	if errors.As(o.err, &rv) {
		return evm.PrecompileResult{Output: rv.Data, GasUsed: c.gasUsed, Status: evm.StatusRevert}
	}
	if errors.Is(o.err, errOutOfGas) {
		return evm.PrecompileResult{GasUsed: c.gasLimit, Status: evm.StatusOutOfGas}
	}
	return evm.PrecompileResult{GasUsed: c.gasLimit, Status: evm.StatusPrecompileFailure}
}

func Call(c *Context, addr Address, input []byte) ([]byte, error) {
	if err := c.charge(inputCost(c.Fork, len(input))); err != nil {
		return nil, err
	}
	if dormantPrecompile(c, addr) {
		return nil, nil
	}
	table, err := tableFor(c, addr)
	if err != nil {
		return nil, err
	}
	if addr == SignatureVerifierAddress && len(input) > maxVerifierInput {
		return nil, errSel("InvalidFormat()")
	}
	c.self = addr
	return table.dispatch(c, input)
}

func tableFor(c *Context, addr Address) (*methodTable, error) {
	if IsTIP20(addr) {
		code, err := c.accountCode(addr)
		if err != nil && c.at(hardfork.T4) {
			return nil, err
		}
		if len(code) == 0 {
			return nil, errSel("Uninitialized()")
		}
		return tip20Table, nil
	}
	t, ok := systemTables[addr]
	if !ok {
		return nil, revert(nil)
	}
	return t, nil
}

var systemTables = map[Address]*methodTable{}

func extend(t *methodTable, methods ...method) *methodTable {
	for i := range methods {
		m := &methods[i]
		key := selectorWord(selector(m.sig))
		_, dup := t.bySelector[key]
		assert.Ok(!dup, "duplicate precompile selector")
		t.bySelector[key] = m
	}
	return t
}

func register(addr Address, t *methodTable) *methodTable {
	systemTables[addr] = t
	return t
}
