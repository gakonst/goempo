package evm

type ExecFunc func(e *EVM, f *Frame) Status

type Operation struct {
	Exec    ExecFunc
	Gas     uint64
	In      int
	Out     int
	Defined bool
	// inline marks a stock operation interpret runs without calling Exec.
	inline bool
}

type JumpTable [256]Operation

func (t *JumpTable) Set(op OpCode, o Operation) {
	o.Defined = true
	// A replaced operation runs its own Exec.
	o.inline = false
	t[op] = o
}

func (t *JumpTable) Unset(op OpCode) {
	t[op] = Operation{}
}

func NewJumpTable(fork Fork, g *GasTable) *JumpTable {
	t := &JumpTable{}
	setArithmetic(t, g)
	setEnvironment(t, g)
	setMemoryOps(t, g)
	setSystem(t, g)
	setStackOps(t, g)
	if fork >= London {
		t.Set(BASEFEE, Operation{Exec: opBaseFee, Gas: g.Base, Out: 1})
	}
	if fork >= Shanghai {
		t.Set(PUSH0, Operation{Exec: opPush0, Gas: g.Base, Out: 1})
	}
	if fork >= Cancun {
		t.Set(TLOAD, Operation{Exec: opTload, Gas: g.WarmAccess, In: 1, Out: 1})
		t.Set(TSTORE, Operation{Exec: opTstore, Gas: g.WarmAccess, In: 2})
		t.Set(MCOPY, Operation{Exec: opMcopy, Gas: g.VeryLow, In: 3})
		t.Set(BLOBHASH, Operation{Exec: opBlobHash, Gas: g.VeryLow, In: 1, Out: 1})
		t.Set(BLOBBASEFEE, Operation{Exec: opBlobBaseFee, Gas: g.Base, Out: 1})
	}
	if fork >= Osaka {
		t.Set(CLZ, Operation{Exec: opClz, Gas: g.Low, In: 1, Out: 1})
	}
	markInline(t)
	return t
}

func setArithmetic(t *JumpTable, g *GasTable) {
	t.Set(STOP, Operation{Exec: opStop})
	t.Set(ADD, Operation{Exec: opAdd, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(MUL, Operation{Exec: opMul, Gas: g.Low, In: 2, Out: 1})
	t.Set(SUB, Operation{Exec: opSub, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(DIV, Operation{Exec: opDiv, Gas: g.Low, In: 2, Out: 1})
	t.Set(SDIV, Operation{Exec: opSdiv, Gas: g.Low, In: 2, Out: 1})
	t.Set(MOD, Operation{Exec: opMod, Gas: g.Low, In: 2, Out: 1})
	t.Set(SMOD, Operation{Exec: opSmod, Gas: g.Low, In: 2, Out: 1})
	t.Set(ADDMOD, Operation{Exec: opAddmod, Gas: g.Mid, In: 3, Out: 1})
	t.Set(MULMOD, Operation{Exec: opMulmod, Gas: g.Mid, In: 3, Out: 1})
	t.Set(EXP, Operation{Exec: opExp, Gas: g.Exp, In: 2, Out: 1})
	t.Set(SIGNEXTEND, Operation{Exec: opSignExtend, Gas: g.Low, In: 2, Out: 1})
	t.Set(LT, Operation{Exec: opLt, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(GT, Operation{Exec: opGt, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(SLT, Operation{Exec: opSlt, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(SGT, Operation{Exec: opSgt, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(EQ, Operation{Exec: opEq, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(ISZERO, Operation{Exec: opIsZero, Gas: g.VeryLow, In: 1, Out: 1})
	t.Set(AND, Operation{Exec: opAnd, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(OR, Operation{Exec: opOr, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(XOR, Operation{Exec: opXor, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(NOT, Operation{Exec: opNot, Gas: g.VeryLow, In: 1, Out: 1})
	t.Set(BYTE, Operation{Exec: opByte, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(SHL, Operation{Exec: opShl, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(SHR, Operation{Exec: opShr, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(SAR, Operation{Exec: opSar, Gas: g.VeryLow, In: 2, Out: 1})
	t.Set(KECCAK256, Operation{Exec: opKeccak, Gas: g.Keccak, In: 2, Out: 1})
}

func setEnvironment(t *JumpTable, g *GasTable) {
	t.Set(ADDRESS, Operation{Exec: opAddress, Gas: g.Base, Out: 1})
	t.Set(BALANCE, Operation{Exec: opBalance, In: 1, Out: 1})
	t.Set(ORIGIN, Operation{Exec: opOrigin, Gas: g.Base, Out: 1})
	t.Set(CALLER, Operation{Exec: opCaller, Gas: g.Base, Out: 1})
	t.Set(CALLVALUE, Operation{Exec: opCallValue, Gas: g.Base, Out: 1})
	t.Set(CALLDATALOAD, Operation{Exec: opCallDataLoad, Gas: g.VeryLow, In: 1, Out: 1})
	t.Set(CALLDATASIZE, Operation{Exec: opCallDataSize, Gas: g.Base, Out: 1})
	t.Set(CALLDATACOPY, Operation{Exec: opCallDataCopy, Gas: g.VeryLow, In: 3})
	t.Set(CODESIZE, Operation{Exec: opCodeSize, Gas: g.Base, Out: 1})
	t.Set(CODECOPY, Operation{Exec: opCodeCopy, Gas: g.VeryLow, In: 3})
	t.Set(GASPRICE, Operation{Exec: opGasPrice, Gas: g.Base, Out: 1})
	t.Set(EXTCODESIZE, Operation{Exec: opExtCodeSize, In: 1, Out: 1})
	t.Set(EXTCODECOPY, Operation{Exec: opExtCodeCopy, In: 4})
	t.Set(RETURNDATASIZE, Operation{Exec: opReturnDataSize, Gas: g.Base, Out: 1})
	t.Set(RETURNDATACOPY, Operation{Exec: opReturnDataCopy, Gas: g.VeryLow, In: 3})
	t.Set(EXTCODEHASH, Operation{Exec: opExtCodeHash, In: 1, Out: 1})
	t.Set(BLOCKHASH, Operation{Exec: opBlockHash, Gas: g.BlockHash, In: 1, Out: 1})
	t.Set(COINBASE, Operation{Exec: opCoinbase, Gas: g.Base, Out: 1})
	t.Set(TIMESTAMP, Operation{Exec: opTimestamp, Gas: g.Base, Out: 1})
	t.Set(NUMBER, Operation{Exec: opNumber, Gas: g.Base, Out: 1})
	t.Set(PREVRANDAO, Operation{Exec: opPrevRandao, Gas: g.Base, Out: 1})
	t.Set(GASLIMIT, Operation{Exec: opGasLimit, Gas: g.Base, Out: 1})
	t.Set(CHAINID, Operation{Exec: opChainID, Gas: g.Base, Out: 1})
	t.Set(SELFBALANCE, Operation{Exec: opSelfBalance, Gas: g.Low, Out: 1})
}

func setMemoryOps(t *JumpTable, g *GasTable) {
	t.Set(POP, Operation{Exec: opPop, Gas: g.Base, In: 1})
	t.Set(MLOAD, Operation{Exec: opMload, Gas: g.VeryLow, In: 1, Out: 1})
	t.Set(MSTORE, Operation{Exec: opMstore, Gas: g.VeryLow, In: 2})
	t.Set(MSTORE8, Operation{Exec: opMstore8, Gas: g.VeryLow, In: 2})
	t.Set(SLOAD, Operation{Exec: opSload, In: 1, Out: 1})
	t.Set(SSTORE, Operation{Exec: opSstore, In: 2})
	t.Set(JUMP, Operation{Exec: opJump, Gas: g.Mid, In: 1})
	t.Set(JUMPI, Operation{Exec: opJumpi, Gas: g.High, In: 2})
	t.Set(PC, Operation{Exec: opPc, Gas: g.Base, Out: 1})
	t.Set(MSIZE, Operation{Exec: opMsize, Gas: g.Base, Out: 1})
	t.Set(GAS, Operation{Exec: opGas, Gas: g.Base, Out: 1})
	t.Set(JUMPDEST, Operation{Exec: opJumpdest, Gas: g.JumpDest})
}

func setSystem(t *JumpTable, g *GasTable) {
	t.Set(CREATE, Operation{Exec: opCreate, Gas: g.Create, In: 3, Out: 1})
	t.Set(CALL, Operation{Exec: opCall, In: 7, Out: 1})
	t.Set(CALLCODE, Operation{Exec: opCallCode, In: 7, Out: 1})
	t.Set(RETURN, Operation{Exec: opReturn, In: 2})
	t.Set(DELEGATECALL, Operation{Exec: opDelegateCall, In: 6, Out: 1})
	t.Set(CREATE2, Operation{Exec: opCreate2, Gas: g.Create, In: 4, Out: 1})
	t.Set(STATICCALL, Operation{Exec: opStaticCall, In: 6, Out: 1})
	t.Set(REVERT, Operation{Exec: opRevert, In: 2})
	t.Set(INVALID, Operation{Exec: opInvalid})
	t.Set(SELFDESTRUCT, Operation{Exec: opSelfDestruct, Gas: g.SelfDestruct, In: 1})
}

func setStackOps(t *JumpTable, g *GasTable) {
	for i := 0; i < 32; i++ {
		t.Set(PUSH1+OpCode(i), Operation{Exec: makePush(i + 1), Gas: g.VeryLow, Out: 1})
	}
	for i := 0; i < 16; i++ {
		t.Set(DUP1+OpCode(i), Operation{Exec: makeDup(i), Gas: g.VeryLow, In: i + 1, Out: i + 2})
		t.Set(SWAP1+OpCode(i), Operation{Exec: makeSwap(i + 1), Gas: g.VeryLow, In: i + 2, Out: i + 2})
	}
	for i := 0; i <= 4; i++ {
		t.Set(LOG0+OpCode(i), Operation{Exec: makeLog(i), Gas: g.Log, In: i + 2})
	}
}
