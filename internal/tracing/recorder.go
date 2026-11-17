// Package tracing records EVM execution for debug and trace RPC tracers.
package tracing

import (
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	noChild      = -1
	framesMax    = 1 << 20
	stepsMax     = 1 << 26
	wordBytes    = 32
	selectorSize = 4
)

const _ = uint(wordBytes - selectorSize)

const _ = uint(stepsMax - framesMax)

type StepOptions struct {
	Enabled    bool
	Stack      bool
	Memory     bool
	ReturnData bool
	Storage    bool
}

type SlotChange struct {
	Key   evm.Hash
	Value evm.Hash
	Set   bool
}

type Step struct {
	PC         int
	Op         evm.OpCode
	Gas        uint64
	Cost       uint64
	Depth      int
	Status     evm.Status
	Refund     int64
	Stack      []u256.Int
	Memory     []byte
	ReturnData []byte
	Slot       SlotChange
	Child      int
}

type Log struct {
	Log      evm.Log
	Position int
	Index    int
}

type Node struct {
	Kind       evm.CallKind
	From       evm.Address
	To         evm.Address
	Input      []byte
	Value      u256.Int
	Gas        uint64
	GasUsed    uint64
	Output     []byte
	Status     evm.Status
	Depth      int
	Parent     int
	Children   []int
	Logs       []Log
	Steps      []Step
	refundBase int64
	openStep   int
}

func (n *Node) Success() bool {
	return n.Status == evm.StatusStop || n.Status == evm.StatusReturn || n.Status == evm.StatusSelfDestruct
}

type Recorder struct {
	Nodes []Node
	Roots []int
	opts  StepOptions
	state evm.State
	stack []int
}

func NewRecorder(st evm.State, opts StepOptions) *Recorder {
	return &Recorder{state: st, opts: opts}
}

func (r *Recorder) current() int {
	if len(r.stack) == 0 {
		return noChild
	}
	return r.stack[len(r.stack)-1]
}

func (r *Recorder) Enter(c *evm.TraceCall) {
	assert.Ok(len(r.Nodes) < framesMax, "trace frames within bound")
	from, to := c.From, c.To
	if delegated(c.Kind) {
		from = c.Target
	}
	idx := len(r.Nodes)
	parent := r.current()
	r.Nodes = append(r.Nodes, Node{
		Kind:       c.Kind,
		From:       from,
		To:         to,
		Input:      append([]byte(nil), c.Input...),
		Value:      c.Value,
		Gas:        c.Gas,
		Depth:      len(r.stack),
		Parent:     parent,
		refundBase: r.state.Refund(),
		openStep:   noChild,
	})
	if parent == noChild {
		r.Roots = append(r.Roots, idx)
	} else {
		p := &r.Nodes[parent]
		p.Children = append(p.Children, idx)
		if p.openStep != noChild {
			p.Steps[p.openStep].Child = idx
		}
	}
	r.stack = append(r.stack, idx)
}

func (r *Recorder) Exit(x *evm.TraceExit) {
	idx := r.current()
	assert.Ok(idx != noChild, "exit matches an enter")
	r.stack = r.stack[:len(r.stack)-1]
	n := &r.Nodes[idx]
	n.Status = x.Status
	n.Output = append([]byte(nil), x.Output...)
	n.GasUsed = n.Gas - min(x.GasLeft, n.Gas)
	if n.Kind.IsCreate() && x.Address != (evm.Address{}) {
		n.To = x.Address
	}
	if n.Kind.IsCreate() && n.Success() && r.state != nil {
		n.Output = append([]byte(nil), r.state.Code(n.To)...)
	}
}

func (r *Recorder) Step(f *evm.Frame, op evm.OpCode) {
	if !r.opts.Enabled {
		return
	}
	idx := r.current()
	assert.Ok(idx != noChild, "step inside a frame")
	n := &r.Nodes[idx]
	assert.Ok(len(n.Steps) < stepsMax, "trace steps within bound")
	s := Step{PC: f.PC(), Op: op, Gas: f.Gas(), Depth: n.Depth, Refund: r.state.Refund() - n.refundBase, Child: noChild}
	if r.opts.Stack {
		s.Stack = append([]u256.Int(nil), f.Stack()...)
	}
	if r.opts.Memory {
		s.Memory = append([]byte(nil), f.Memory()...)
	}
	if r.opts.ReturnData {
		s.ReturnData = append([]byte(nil), f.ReturnData()...)
	}
	if r.opts.Storage && touchesSlot(op) {
		st := f.Stack()
		if len(st) >= 1 {
			s.Slot.Key = st[len(st)-1].Bytes32()
		}
		if op == evm.SSTORE && len(st) >= 2 {
			s.Slot.Value = st[len(st)-2].Bytes32()
			s.Slot.Set = true
		}
	}
	n.openStep = len(n.Steps)
	n.Steps = append(n.Steps, s)
}

func (r *Recorder) StepEnd(f *evm.Frame, op evm.OpCode, status evm.Status) {
	if !r.opts.Enabled {
		return
	}
	idx := r.current()
	if status == evm.StatusCall {
		idx = r.Nodes[idx].Parent
	}
	assert.Ok(idx != noChild, "step end inside a frame")
	n := &r.Nodes[idx]
	assert.Ok(n.openStep != noChild, "step end follows a step")
	s := &n.Steps[n.openStep]
	n.openStep = noChild
	s.Status = status
	s.Cost = s.Gas - min(f.Gas(), s.Gas)
	if status == evm.StatusCall && createsFrame(op) && s.Child != noChild {
		s.Cost -= min(r.Nodes[s.Child].Gas, s.Cost)
	}
	if r.opts.Storage && op == evm.SLOAD && status == evm.StatusContinue {
		st := f.Stack()
		if len(st) >= 1 {
			s.Slot.Value = st[len(st)-1].Bytes32()
			s.Slot.Set = true
		}
	}
}

func (r *Recorder) AddLog(l evm.Log, index int) {
	idx := r.current()
	if idx == noChild {
		return
	}
	n := &r.Nodes[idx]
	n.Logs = append(n.Logs, Log{Log: l, Position: len(n.Children), Index: index})
}

func delegated(k evm.CallKind) bool {
	return k == evm.KindDelegateCall || k == evm.KindCallCode
}

func touchesSlot(op evm.OpCode) bool {
	return op == evm.SLOAD || op == evm.SSTORE
}

func createsFrame(op evm.OpCode) bool {
	return op == evm.CREATE || op == evm.CREATE2
}
