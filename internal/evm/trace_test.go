package evm_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type countTracer struct {
	enters   []evm.TraceCall
	exits    []evm.TraceExit
	steps    int
	stepEnds int
	ops      []evm.OpCode
}

func (c *countTracer) Enter(call *evm.TraceCall) { c.enters = append(c.enters, *call) }

func (c *countTracer) Exit(x *evm.TraceExit) { c.exits = append(c.exits, *x) }

func (c *countTracer) Step(_ *evm.Frame, op evm.OpCode) {
	c.steps++
	c.ops = append(c.ops, op)
}

func (c *countTracer) StepEnd(_ *evm.Frame, _ evm.OpCode, _ evm.Status) { c.stepEnds++ }

var callerCode = []byte{
	0x60, 0x20, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00,
	0x73, 0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0x61, 0xff, 0xff, 0xf1,
	0x60, 0x00, 0x51, 0x60, 0x01, 0x55,
	0x60, 0x00, 0x55, 0x00,
}

var calleeCode = []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}

func runCaller(tracer evm.Tracer) evm.Result {
	st := memstate.New()
	st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40)})
	st.Put(contract, memstate.Account{Code: callerCode, Nonce: 1})
	st.Put(other, memstate.Account{Code: calleeCode, Nonce: 1})
	st.WarmAddress(sender)
	st.WarmAddress(contract)
	machine := evm.New(evm.Context{
		State:  st,
		Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
		Tx:     &evm.TxContext{Origin: sender},
		Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
		Tracer: tracer,
	})
	return machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 200_000})
}

func TestTracerSeesCallsAndSteps(t *testing.T) {
	plain := runCaller(nil)
	c := &countTracer{}
	traced := runCaller(c)
	assert.Equal(traced.Status, plain.Status, "status unchanged by tracing")
	assert.Equal(traced.GasLeft, plain.GasLeft, "gas unchanged by tracing")
	assert.Equal(len(c.enters), 2, "two frames entered")
	assert.Equal(len(c.exits), 2, "two frames exited")
	assert.Equal(c.enters[0].Depth, 0, "root depth")
	assert.Equal(c.enters[1].Depth, 1, "child depth")
	assert.Equal(c.enters[1].To, other, "child target")
	assert.Equal(c.exits[0].Depth, 1, "child exits first")
	assert.Equal(c.steps, c.stepEnds, "every step ends")
	assert.Equal(c.steps, 21, "caller and callee opcodes")
	assert.Equal(c.ops[len(c.ops)-1], evm.STOP, "last op")
}

type sourceState struct {
	*memstate.State
	tracer evm.Tracer
}

func (s sourceState) EVMTracer(*evm.TxContext) evm.Tracer { return s.tracer }

func TestTracerFromState(t *testing.T) {
	st := memstate.New()
	st.Put(contract, memstate.Account{Code: calleeCode, Nonce: 1})
	c := &countTracer{}
	machine := evm.New(evm.Context{
		State:  sourceState{State: st, tracer: c},
		Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
		Tx:     &evm.TxContext{Origin: sender},
		Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
	})
	res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 100_000})
	assert.Equal(res.Status, evm.StatusReturn, "status")
	assert.Equal(c.steps, 6, "callee opcodes")
}

func loopCode() []byte {
	return []byte{
		0x61, 0x27, 0x10,
		0x5b,
		0x60, 0x01, 0x90, 0x03,
		0x80, 0x60, 0x03, 0x57,
		0x00,
	}
}

func benchLoop(b *testing.B, tracer evm.Tracer) {
	st := memstate.New()
	st.Put(contract, memstate.Account{Code: loopCode(), Nonce: 1})
	cfg := evm.NewConfig(evm.Osaka, u256.FromUint64(1))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		machine := evm.New(evm.Context{
			State:  st,
			Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
			Tx:     &evm.TxContext{Origin: sender},
			Config: cfg,
			Tracer: tracer,
		})
		res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 10_000_000})
		assert.Equal(res.Status, evm.StatusStop, "loop stops")
	}
}

func BenchmarkLoopUntraced(b *testing.B) { benchLoop(b, nil) }

type nopTracer struct{}

func (nopTracer) Enter(*evm.TraceCall) {}

func (nopTracer) Exit(*evm.TraceExit) {}

func (nopTracer) Step(*evm.Frame, evm.OpCode) {}

func (nopTracer) StepEnd(*evm.Frame, evm.OpCode, evm.Status) {}

func BenchmarkLoopNopTracer(b *testing.B) { benchLoop(b, nopTracer{}) }
