package evm

import "github.com/gakonst/goempo/internal/u256"

func addressWord(a Address) u256.Int {
	return u256.FromBytes(a[:])
}

func wordAddress(w u256.Int) Address {
	b := w.Bytes32()
	return Address(b[12:])
}

func wordHash(w u256.Int) Hash {
	return Hash(w.Bytes32())
}

func (e *EVM) accountAccessCost(addr Address) uint64 {
	if e.State.AddressWarm(addr) {
		return e.gas.WarmAccess
	}
	e.State.WarmAddress(addr)
	return e.gas.ColdAccountAccess
}

func opAddress(_ *EVM, f *Frame) Status {
	f.Push(addressWord(f.address))
	return StatusContinue
}

func opBalance(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	addr := wordAddress(*top)
	if !f.UseGas(e.accountAccessCost(addr)) {
		return StatusOutOfGas
	}
	*top = e.State.Balance(addr)
	return StatusContinue
}

func opOrigin(e *EVM, f *Frame) Status {
	f.Push(addressWord(e.Tx.Origin))
	return StatusContinue
}

func opCaller(_ *EVM, f *Frame) Status {
	f.Push(addressWord(f.caller))
	return StatusContinue
}

func opCallValue(_ *EVM, f *Frame) Status {
	f.Push(f.value)
	return StatusContinue
}

func opCallDataLoad(_ *EVM, f *Frame) Status {
	top := f.Peek(0)
	var buf [u256.Bytes]byte
	copyPadded(buf[:], source{data: f.input, offset: *top})
	*top = u256.FromBytes32(buf)
	return StatusContinue
}

func opCallDataSize(_ *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(uint64(len(f.input))))
	return StatusContinue
}

func opCodeSize(_ *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(uint64(len(f.code))))
	return StatusContinue
}

func opGasPrice(e *EVM, f *Frame) Status {
	f.Push(e.Tx.GasPrice)
	return StatusContinue
}

func opReturnDataSize(_ *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(uint64(len(f.returnData))))
	return StatusContinue
}

func (e *EVM) copyToMemory(f *Frame, src []byte) Status {
	dest := f.Pop()
	offset := f.Pop()
	size := f.Pop()
	r, ok := toRange(span{offset: dest, size: size})
	if !ok {
		return StatusOutOfGas
	}
	if !f.UseGas(toWords(r.size) * e.gas.CopyWord) {
		return StatusOutOfGas
	}
	if !f.expandRange(e.gas, r) {
		return StatusOutOfGas
	}
	copyPadded(f.memSlice(r), source{data: src, offset: offset})
	return StatusContinue
}

func opCallDataCopy(e *EVM, f *Frame) Status {
	return e.copyToMemory(f, f.input)
}

func opCodeCopy(e *EVM, f *Frame) Status {
	return e.copyToMemory(f, f.code)
}

func opReturnDataCopy(e *EVM, f *Frame) Status {
	offset := *f.Peek(1)
	size := *f.Peek(2)
	end, overflow := offset.AddOverflow(size)
	if overflow || end.Gt(u256.FromUint64(uint64(len(f.returnData)))) {
		return StatusReturnDataOutOfBounds
	}
	return e.copyToMemory(f, f.returnData)
}

func opExtCodeSize(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	addr := wordAddress(*top)
	if !f.UseGas(e.accountAccessCost(addr)) {
		return StatusOutOfGas
	}
	*top = u256.FromUint64(uint64(e.State.CodeSize(addr)))
	return StatusContinue
}

func opExtCodeCopy(e *EVM, f *Frame) Status {
	addr := wordAddress(f.Pop())
	if !f.UseGas(e.accountAccessCost(addr)) {
		return StatusOutOfGas
	}
	return e.copyToMemory(f, e.State.Code(addr))
}

func opExtCodeHash(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	addr := wordAddress(*top)
	if !f.UseGas(e.accountAccessCost(addr)) {
		return StatusOutOfGas
	}
	if e.State.Empty(addr) {
		*top = u256.Zero()
		return StatusContinue
	}
	*top = u256.FromBytes32(e.State.CodeHash(addr))
	return StatusContinue
}

func opBlockHash(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	current := e.Block.Number
	if !top.IsUint64() {
		*top = u256.Zero()
		return StatusContinue
	}
	n := top.Uint64()
	if n >= current || current-n > 256 || e.Block.GetHash == nil {
		*top = u256.Zero()
		return StatusContinue
	}
	*top = u256.FromBytes32(e.Block.GetHash(n))
	return StatusContinue
}

func opCoinbase(e *EVM, f *Frame) Status {
	f.Push(addressWord(e.Block.Coinbase))
	return StatusContinue
}

func opTimestamp(e *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(e.Block.Time))
	return StatusContinue
}

func opNumber(e *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(e.Block.Number))
	return StatusContinue
}

func opPrevRandao(e *EVM, f *Frame) Status {
	f.Push(u256.FromBytes32(e.Block.PrevRandao))
	return StatusContinue
}

func opGasLimit(e *EVM, f *Frame) Status {
	f.Push(u256.FromUint64(e.Block.GasLimit))
	return StatusContinue
}

func opChainID(e *EVM, f *Frame) Status {
	f.Push(e.Config.ChainID)
	return StatusContinue
}

func opSelfBalance(e *EVM, f *Frame) Status {
	f.Push(e.State.Balance(f.address))
	return StatusContinue
}

func opBaseFee(e *EVM, f *Frame) Status {
	f.Push(e.Block.BaseFee)
	return StatusContinue
}

func opBlobHash(e *EVM, f *Frame) Status {
	top := f.Peek(0)
	hashes := e.Tx.BlobHashes
	if top.IsUint64() && top.Uint64() < uint64(len(hashes)) {
		*top = u256.FromBytes32(hashes[top.Uint64()])
		return StatusContinue
	}
	*top = u256.Zero()
	return StatusContinue
}

func opBlobBaseFee(e *EVM, f *Frame) Status {
	f.Push(e.Block.BlobBaseFee)
	return StatusContinue
}
