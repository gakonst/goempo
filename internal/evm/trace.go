package evm

import (
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type Tracer interface {
	Enter(c *TraceCall)
	Exit(x *TraceExit)
	Step(f *Frame, op OpCode)
	StepEnd(f *Frame, op OpCode, status Status)
}

type TracerSource interface {
	EVMTracer(tx *TxContext) Tracer
}

type TraceCall struct {
	Kind   CallKind
	From   Address
	Target Address
	To     Address
	Input  []byte
	Value  u256.Int
	Gas    uint64
	Depth  int
	Static bool
}

type TraceExit struct {
	Status  Status
	GasLeft uint64
	Output  []byte
	Address Address
	Depth   int
}

func (f *Frame) PC() int { return f.pc - 1 }

func (f *Frame) Stack() []u256.Int { return f.stack[:f.sp] }

func (f *Frame) ReturnData() []byte { return f.returnData }

func (e *EVM) Tracer() Tracer { return e.tracer }

func tracerOf(ctx Context) Tracer {
	if ctx.Tracer != nil {
		return ctx.Tracer
	}
	if src, ok := ctx.State.(TracerSource); ok {
		return src.EVMTracer(ctx.Tx)
	}
	return nil
}

func (e *EVM) dispatchTraced(req callRequest) (frameResult, bool) {
	ev := TraceCall{
		Kind:   req.kind,
		From:   req.caller,
		Target: req.address,
		To:     req.codeAddress,
		Input:  req.input,
		Value:  req.value,
		Gas:    req.gas,
		Depth:  req.depth,
		Static: req.static,
	}
	if req.kind.IsCreate() {
		ev.To = e.createAddress(req, e.State.Nonce(req.caller))
		ev.Input = req.input
	}
	e.tracer.Enter(&ev)
	var res frameResult
	var pushed bool
	if req.kind.IsCreate() {
		res, pushed = e.dispatchCreate(req)
	} else {
		res, pushed = e.dispatchCall(req)
	}
	if !pushed {
		if req.kind.IsCreate() && res.address == (Address{}) {
			res.address = ev.To
		}
		e.traceExit(res, req.depth)
	}
	return res, pushed
}

func (e *EVM) traceExit(res frameResult, depth int) {
	e.tracer.Exit(&TraceExit{Status: res.status, GasLeft: res.gasLeft, Output: res.output, Address: res.address, Depth: depth})
}

func (e *EVM) runTraced() frameResult {
	for iter := 0; iter < frameStepsMax; iter++ {
		f := e.frames[e.top]
		status := e.interpretTraced(f)
		if status == StatusCall {
			continue
		}
		res := e.finish(f, status)
		e.traceExit(res, f.depth)
		e.top--
		if e.top < 0 {
			return res
		}
		e.deliver(e.frames[e.top], res)
	}
	assert.Fail("evm run reached frameStepsMax")
	return frameResult{}
}

func (e *EVM) interpretTraced(f *Frame) Status {
	table := e.table
	code := f.code
	t := e.tracer
	for steps := 0; steps < instructionsMax; steps++ {
		op := STOP
		if f.pc < len(code) {
			op = OpCode(code[f.pc])
		}
		f.pc++
		t.Step(f, op)
		status := e.step(f, &table[op])
		t.StepEnd(f, op, status)
		if status != StatusContinue {
			return status
		}
	}
	assert.Fail("evm interpret reached instructionsMax")
	return StatusOutOfGas
}

func (e *EVM) step(f *Frame, o *Operation) Status {
	if !o.Defined {
		return StatusInvalidOpcode
	}
	if f.sp < o.In {
		return StatusStackUnderflow
	}
	need := f.sp - o.In + o.Out
	if need > StackLimit {
		return StatusStackOverflow
	}
	if need > len(f.stack) {
		f.growStack(need)
	}
	if f.gas < o.Gas {
		return StatusOutOfGas
	}
	f.gas -= o.Gas
	return o.Exec(e, f)
}
