package evm

import (
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const StackLimit = 1024

const CallDepthLimit = 1024

const _ = uint(StackLimit - CallDepthLimit)

type CallKind uint8

const (
	KindCall CallKind = iota
	KindCallCode
	KindDelegateCall
	KindStaticCall
	KindCreate
	KindCreate2
)

func (k CallKind) IsCreate() bool {
	return k == KindCreate || k == KindCreate2
}

type Frame struct {
	stack       []u256.Int
	sp          int
	memory      []byte
	code        []byte
	jumpdests   []uint64
	input       []byte
	returnData  []byte
	output      []byte
	value       u256.Int
	caller      Address
	address     Address
	codeAddress Address
	gas         uint64
	pc          int
	depth       int
	snapshot    int
	kind        CallKind
	static      bool
	pending     pendingCall
}

type pendingCall struct {
	kind      CallKind
	retOffset uint64
	retSize   uint64
}

func (f *Frame) Gas() uint64 { return f.gas }

func (f *Frame) Address() Address { return f.address }

func (f *Frame) Caller() Address { return f.caller }

func (f *Frame) Static() bool { return f.static }

func (f *Frame) Depth() int { return f.depth }

func (f *Frame) StackLen() int { return f.sp }

func (f *Frame) Push(v u256.Int) {
	f.stack[f.sp] = v
	f.sp++
}

func (f *Frame) Pop() u256.Int {
	f.sp--
	return f.stack[f.sp]
}

func (f *Frame) Peek(n int) *u256.Int {
	return &f.stack[f.sp-1-n]
}

func (f *Frame) UseGas(amount uint64) bool {
	if f.gas < amount {
		return false
	}
	f.gas -= amount
	return true
}

func (f *Frame) Memory() []byte { return f.memory }

// reset readies a reused frame. Every frame owns a StackLimit-word stack
// from its creation, so the interpreter's overflow check is the only bound
// pushes need.
func (f *Frame) reset() {
	assert.Equal(len(f.stack), StackLimit, "a frame's stack is preallocated")
	f.sp = 0
	f.memory = f.memory[:0]
	f.returnData = nil
	f.output = nil
	f.pc = 0
	f.pending = pendingCall{}
}

type memRange struct {
	offset uint64
	size   uint64
}

func (r memRange) end() uint64 {
	if r.size == 0 {
		return 0
	}
	return r.offset + r.size
}

type span struct {
	offset u256.Int
	size   u256.Int
}

func toRange(s span) (memRange, bool) {
	offset := s.offset
	size := s.size
	if size.IsZero() {
		return memRange{}, true
	}
	if !size.IsUint64() || !offset.IsUint64() {
		return memRange{}, false
	}
	if size.Uint64() > memoryBytesMax || offset.Uint64() > memoryBytesMax {
		return memRange{}, false
	}
	return memRange{offset: offset.Uint64(), size: size.Uint64()}, true
}

func (f *Frame) expand(gas *GasTable, end uint64) bool {
	current := uint64(len(f.memory))
	if end <= current {
		return true
	}
	if end > memoryBytesMax {
		return false
	}
	newWords := toWords(end)
	cost := gas.MemoryCost(newWords) - gas.MemoryCost(current/wordBytes)
	if !f.UseGas(cost) {
		return false
	}
	newSize := int(newWords * wordBytes)
	if newSize <= cap(f.memory) {
		old := len(f.memory)
		f.memory = f.memory[:newSize]
		clear(f.memory[old:])
		return true
	}
	grown := make([]byte, newSize, max(newSize, 2*cap(f.memory)))
	copy(grown, f.memory)
	f.memory = grown
	return true
}

func (f *Frame) expandRange(gas *GasTable, r memRange) bool {
	return f.expand(gas, r.end())
}

func (f *Frame) memSlice(r memRange) []byte {
	if r.size == 0 {
		return nil
	}
	return f.memory[r.offset : r.offset+r.size]
}
