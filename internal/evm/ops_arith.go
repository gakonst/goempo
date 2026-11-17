package evm

import "github.com/gakonst/goempo/internal/u256"

func opStop(_ *EVM, _ *Frame) Status { return StatusStop }

func opInvalid(_ *EVM, _ *Frame) Status { return StatusInvalidOpcode }

func opJumpdest(_ *EVM, _ *Frame) Status { return StatusContinue }

func opAdd(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Add(*y)
	return StatusContinue
}

func opMul(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Mul(*y)
	return StatusContinue
}

func opSub(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Sub(*y)
	return StatusContinue
}

func opDiv(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Div(*y)
	return StatusContinue
}

func opSdiv(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.SDiv(*y)
	return StatusContinue
}

func opMod(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Mod(*y)
	return StatusContinue
}

func opSmod(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.SMod(*y)
	return StatusContinue
}

func opAddmod(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Pop()
	m := f.Peek(0)
	*m = x.AddFull(y).Mod(*m)
	return StatusContinue
}

func opMulmod(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Pop()
	m := f.Peek(0)
	*m = x.MulFull(y).Mod(*m)
	return StatusContinue
}

func opExp(e *EVM, f *Frame) Status {
	base := f.Pop()
	exponent := f.Peek(0)
	if !f.UseGas(uint64(exponent.ByteLen()) * e.gas.ExpByte) {
		return StatusOutOfGas
	}
	*exponent = base.Exp(*exponent)
	return StatusContinue
}

func opSignExtend(_ *EVM, f *Frame) Status {
	size := f.Pop()
	x := f.Peek(0)
	*x = x.SignExtend(size)
	return StatusContinue
}

type truth bool

func boolWord(b truth) u256.Int {
	if b {
		return u256.One()
	}
	return u256.Zero()
}

func opLt(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = boolWord(truth(x.Lt(*y)))
	return StatusContinue
}

func opGt(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = boolWord(truth(x.Gt(*y)))
	return StatusContinue
}

func opSlt(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = boolWord(truth(x.Slt(*y)))
	return StatusContinue
}

func opSgt(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = boolWord(truth(x.Sgt(*y)))
	return StatusContinue
}

func opEq(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = boolWord(truth(x == *y))
	return StatusContinue
}

func opIsZero(_ *EVM, f *Frame) Status {
	x := f.Peek(0)
	*x = boolWord(truth(x.IsZero()))
	return StatusContinue
}

func opAnd(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.And(*y)
	return StatusContinue
}

func opOr(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Or(*y)
	return StatusContinue
}

func opXor(_ *EVM, f *Frame) Status {
	x := f.Pop()
	y := f.Peek(0)
	*y = x.Xor(*y)
	return StatusContinue
}

func opNot(_ *EVM, f *Frame) Status {
	x := f.Peek(0)
	*x = x.Not()
	return StatusContinue
}

func opByte(_ *EVM, f *Frame) Status {
	index := f.Pop()
	x := f.Peek(0)
	*x = x.Byte(index)
	return StatusContinue
}

func opShl(_ *EVM, f *Frame) Status {
	shift := f.Pop()
	x := f.Peek(0)
	*x = x.Shl(shift)
	return StatusContinue
}

func opShr(_ *EVM, f *Frame) Status {
	shift := f.Pop()
	x := f.Peek(0)
	*x = x.Shr(shift)
	return StatusContinue
}

func opSar(_ *EVM, f *Frame) Status {
	shift := f.Pop()
	x := f.Peek(0)
	*x = x.Sar(shift)
	return StatusContinue
}

func opClz(_ *EVM, f *Frame) Status {
	x := f.Peek(0)
	*x = u256.FromUint64(uint64(u256.Bits - x.BitLen()))
	return StatusContinue
}

func opKeccak(e *EVM, f *Frame) Status {
	offset := f.Pop()
	size := f.Peek(0)
	r, ok := toRange(span{offset: offset, size: *size})
	if !ok {
		return StatusOutOfGas
	}
	if !f.UseGas(toWords(r.size) * e.gas.KeccakWord) {
		return StatusOutOfGas
	}
	if !f.expandRange(e.gas, r) {
		return StatusOutOfGas
	}
	sum := e.hasher.Hash(f.memSlice(r))
	*size = u256.FromBytes32(sum)
	return StatusContinue
}

func opPop(_ *EVM, f *Frame) Status {
	f.sp--
	return StatusContinue
}

func opPush0(_ *EVM, f *Frame) Status {
	f.Push(u256.Zero())
	return StatusContinue
}

func makePush(n int) ExecFunc {
	return func(_ *EVM, f *Frame) Status {
		start := min(f.pc, len(f.code))
		end := min(f.pc+n, len(f.code))
		var buf [u256.Bytes]byte
		copy(buf[u256.Bytes-n:], f.code[start:end])
		f.Push(u256.FromBytes32(buf))
		f.pc += n
		return StatusContinue
	}
}

func makeDup(n int) ExecFunc {
	return func(_ *EVM, f *Frame) Status {
		f.Push(*f.Peek(n))
		return StatusContinue
	}
}

func makeSwap(n int) ExecFunc {
	return func(_ *EVM, f *Frame) Status {
		top := f.Peek(0)
		other := f.Peek(n)
		*top, *other = *other, *top
		return StatusContinue
	}
}
