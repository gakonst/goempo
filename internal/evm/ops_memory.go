package evm

import "github.com/gakonst/goempo/internal/u256"

type source struct {
	data   []byte
	offset u256.Int
}

func copyPadded(dst []byte, src source) {
	if !src.offset.IsUint64() || src.offset.Uint64() >= uint64(len(src.data)) {
		clear(dst)
		return
	}
	n := copy(dst, src.data[src.offset.Uint64():])
	clear(dst[n:])
}

var word32 = u256.FromUint64(wordBytes)

func opMload(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	r, ok := toRange(span{offset: *top, size: word32})
	if !ok {
		return StatusOutOfGas
	}
	if !f.expandRange(e.gas, r) {
		return StatusOutOfGas
	}
	*top = u256.FromBytes(f.memSlice(r))
	return StatusContinue
}

func opMstore(e *EVM, f *Frame) Status {
	offset := f.Pop()
	value := f.Pop()
	r, ok := toRange(span{offset: offset, size: word32})
	if !ok {
		return StatusOutOfGas
	}
	if !f.expandRange(e.gas, r) {
		return StatusOutOfGas
	}
	value.PutBytes32(f.memSlice(r))
	return StatusContinue
}

func opMstore8(e *EVM, f *Frame) Status {
	offset := f.Pop()
	value := f.Pop()
	r, ok := toRange(span{offset: offset, size: u256.One()})
	if !ok {
		return StatusOutOfGas
	}
	if !f.expandRange(e.gas, r) {
		return StatusOutOfGas
	}
	f.memory[r.offset] = byte(value.Uint64())
	return StatusContinue
}

func opMcopy(e *EVM, f *Frame) Status {
	dest := f.Pop()
	src := f.Pop()
	size := f.Pop()
	to, ok := toRange(span{offset: dest, size: size})
	if !ok {
		return StatusOutOfGas
	}
	from, ok := toRange(span{offset: src, size: size})
	if !ok {
		return StatusOutOfGas
	}
	if !f.UseGas(toWords(to.size) * e.gas.CopyWord) {
		return StatusOutOfGas
	}
	if !f.expand(e.gas, max(to.end(), from.end())) {
		return StatusOutOfGas
	}
	copy(f.memSlice(to), f.memSlice(from))
	return StatusContinue
}

func opSload(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	key := wordHash(*top)
	if !f.UseGas(e.AccessSlot(f.address, key)) {
		return StatusOutOfGas
	}
	*top = e.State.Storage(f.address, key)
	return StatusContinue
}

func opSstore(e *EVM, f *Frame) Status {
	if f.static {
		return StatusStaticViolation
	}
	if f.gas <= e.gas.SstoreSentry {
		return StatusOutOfGas
	}
	key := wordHash(f.Pop())
	value := f.Pop()
	cost := uint64(0)
	if e.gas.AccessLists && !e.State.SlotWarm(f.address, key) {
		e.State.WarmSlot(f.address, key)
		cost = e.gas.ColdSload
	}
	slot := slotValues{
		original: e.State.OriginalStorage(f.address, key),
		current:  e.State.Storage(f.address, key),
		value:    value,
	}
	cost += e.sstoreCost(slot)
	if e.storage != nil && slot.current.IsZero() != value.IsZero() {
		if f.gas < e.gas.WarmAccess {
			return StatusOutOfGas
		}
		w := StorageWrite{Address: f.address, Key: key, Current: slot.current, Value: value, Gas: f.gas - e.gas.WarmAccess}
		used, ok := e.storage.CrossZero(w)
		if !ok {
			return StatusOutOfGas
		}
		cost += used
	}
	if !f.UseGas(cost) {
		return StatusOutOfGas
	}
	e.State.SetStorage(f.address, key, value)
	return StatusContinue
}

type slotValues struct {
	original u256.Int
	current  u256.Int
	value    u256.Int
}

func (e *EVM) sstoreCost(s slotValues) uint64 {
	g := e.gas
	if s.current == s.value {
		return g.WarmAccess
	}
	clearRefund := int64(g.SstoreClearRefund)
	if s.original == s.current {
		if s.original.IsZero() {
			return g.SstoreSet
		}
		if s.value.IsZero() {
			e.State.AddRefund(clearRefund)
		}
		return g.SstoreReset
	}
	if !s.original.IsZero() {
		if s.current.IsZero() {
			e.State.AddRefund(-clearRefund)
		} else if s.value.IsZero() {
			e.State.AddRefund(clearRefund)
		}
	}
	if s.original == s.value {
		if s.original.IsZero() {
			e.State.AddRefund(int64(g.SstoreSetRefund))
		} else {
			e.State.AddRefund(int64(g.SstoreReset - g.WarmAccess))
		}
	}
	return g.WarmAccess
}

func opTload(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	*top = e.State.TransientStorage(f.address, wordHash(*top))
	return StatusContinue
}

func opTstore(e *EVM, f *Frame) Status {
	if f.static {
		return StatusStaticViolation
	}
	key := wordHash(f.Pop())
	value := f.Pop()
	e.State.SetTransientStorage(f.address, key, value)
	return StatusContinue
}

func opJump(_ *EVM, f *Frame) Status {
	dest := f.Pop()
	if !validJumpdest(f, dest) {
		return StatusInvalidJump
	}
	f.pc = int(dest.Uint64())
	return StatusContinue
}

func opJumpi(_ *EVM, f *Frame) Status {
	dest := f.Pop()
	cond := f.Pop()
	if cond.IsZero() {
		return StatusContinue
	}
	if !validJumpdest(f, dest) {
		return StatusInvalidJump
	}
	f.pc = int(dest.Uint64())
	return StatusContinue
}

func opPc(_ *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(uint64(f.pc - 1)))
	return StatusContinue
}

func opMsize(_ *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(uint64(len(f.memory))))
	return StatusContinue
}

func opGas(_ *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(f.gas))
	return StatusContinue
}

func makeLog(n int) ExecFunc {
	return func(e *EVM, f *Frame) Status {
		if f.static {
			return StatusStaticViolation
		}
		offset := f.Pop()
		size := f.Pop()
		r, ok := toRange(span{offset: offset, size: size})
		if !ok {
			return StatusOutOfGas
		}
		if !f.UseGas(uint64(n)*e.gas.LogTopic + r.size*e.gas.LogDataByte) {
			return StatusOutOfGas
		}
		if !f.expandRange(e.gas, r) {
			return StatusOutOfGas
		}
		topics := make([]Hash, n)
		for i := 0; i < n; i++ {
			topics[i] = wordHash(f.Pop())
		}
		data := make([]byte, r.size)
		copy(data, f.memSlice(r))
		e.State.AddLog(Log{Address: f.address, Topics: topics, Data: data})
		return StatusContinue
	}
}
