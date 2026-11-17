package evm

type OpCode byte

const (
	STOP       OpCode = 0x00
	ADD        OpCode = 0x01
	MUL        OpCode = 0x02
	SUB        OpCode = 0x03
	DIV        OpCode = 0x04
	SDIV       OpCode = 0x05
	MOD        OpCode = 0x06
	SMOD       OpCode = 0x07
	ADDMOD     OpCode = 0x08
	MULMOD     OpCode = 0x09
	EXP        OpCode = 0x0a
	SIGNEXTEND OpCode = 0x0b

	LT     OpCode = 0x10
	GT     OpCode = 0x11
	SLT    OpCode = 0x12
	SGT    OpCode = 0x13
	EQ     OpCode = 0x14
	ISZERO OpCode = 0x15
	AND    OpCode = 0x16
	OR     OpCode = 0x17
	XOR    OpCode = 0x18
	NOT    OpCode = 0x19
	BYTE   OpCode = 0x1a
	SHL    OpCode = 0x1b
	SHR    OpCode = 0x1c
	SAR    OpCode = 0x1d
	CLZ    OpCode = 0x1e

	KECCAK256 OpCode = 0x20

	ADDRESS        OpCode = 0x30
	BALANCE        OpCode = 0x31
	ORIGIN         OpCode = 0x32
	CALLER         OpCode = 0x33
	CALLVALUE      OpCode = 0x34
	CALLDATALOAD   OpCode = 0x35
	CALLDATASIZE   OpCode = 0x36
	CALLDATACOPY   OpCode = 0x37
	CODESIZE       OpCode = 0x38
	CODECOPY       OpCode = 0x39
	GASPRICE       OpCode = 0x3a
	EXTCODESIZE    OpCode = 0x3b
	EXTCODECOPY    OpCode = 0x3c
	RETURNDATASIZE OpCode = 0x3d
	RETURNDATACOPY OpCode = 0x3e
	EXTCODEHASH    OpCode = 0x3f

	BLOCKHASH   OpCode = 0x40
	COINBASE    OpCode = 0x41
	TIMESTAMP   OpCode = 0x42
	NUMBER      OpCode = 0x43
	PREVRANDAO  OpCode = 0x44
	GASLIMIT    OpCode = 0x45
	CHAINID     OpCode = 0x46
	SELFBALANCE OpCode = 0x47
	BASEFEE     OpCode = 0x48
	BLOBHASH    OpCode = 0x49
	BLOBBASEFEE OpCode = 0x4a

	POP      OpCode = 0x50
	MLOAD    OpCode = 0x51
	MSTORE   OpCode = 0x52
	MSTORE8  OpCode = 0x53
	SLOAD    OpCode = 0x54
	SSTORE   OpCode = 0x55
	JUMP     OpCode = 0x56
	JUMPI    OpCode = 0x57
	PC       OpCode = 0x58
	MSIZE    OpCode = 0x59
	GAS      OpCode = 0x5a
	JUMPDEST OpCode = 0x5b
	TLOAD    OpCode = 0x5c
	TSTORE   OpCode = 0x5d
	MCOPY    OpCode = 0x5e
	PUSH0    OpCode = 0x5f
	PUSH1    OpCode = 0x60
	PUSH32   OpCode = 0x7f
	DUP1     OpCode = 0x80
	DUP16    OpCode = 0x8f
	SWAP1    OpCode = 0x90
	SWAP16   OpCode = 0x9f
	LOG0     OpCode = 0xa0
	LOG4     OpCode = 0xa4

	CREATE       OpCode = 0xf0
	CALL         OpCode = 0xf1
	CALLCODE     OpCode = 0xf2
	RETURN       OpCode = 0xf3
	DELEGATECALL OpCode = 0xf4
	CREATE2      OpCode = 0xf5
	STATICCALL   OpCode = 0xfa
	REVERT       OpCode = 0xfd
	INVALID      OpCode = 0xfe
	SELFDESTRUCT OpCode = 0xff
)

var opNames = [256]string{
	STOP: "STOP", ADD: "ADD", MUL: "MUL", SUB: "SUB", DIV: "DIV", SDIV: "SDIV", MOD: "MOD", SMOD: "SMOD",
	ADDMOD: "ADDMOD", MULMOD: "MULMOD", EXP: "EXP", SIGNEXTEND: "SIGNEXTEND",
	LT: "LT", GT: "GT", SLT: "SLT", SGT: "SGT", EQ: "EQ", ISZERO: "ISZERO", AND: "AND", OR: "OR", XOR: "XOR",
	NOT: "NOT", BYTE: "BYTE", SHL: "SHL", SHR: "SHR", SAR: "SAR", CLZ: "CLZ",
	KECCAK256: "KECCAK256",
	ADDRESS:   "ADDRESS", BALANCE: "BALANCE", ORIGIN: "ORIGIN", CALLER: "CALLER", CALLVALUE: "CALLVALUE",
	CALLDATALOAD: "CALLDATALOAD", CALLDATASIZE: "CALLDATASIZE", CALLDATACOPY: "CALLDATACOPY",
	CODESIZE: "CODESIZE", CODECOPY: "CODECOPY", GASPRICE: "GASPRICE", EXTCODESIZE: "EXTCODESIZE",
	EXTCODECOPY: "EXTCODECOPY", RETURNDATASIZE: "RETURNDATASIZE", RETURNDATACOPY: "RETURNDATACOPY",
	EXTCODEHASH: "EXTCODEHASH",
	BLOCKHASH:   "BLOCKHASH", COINBASE: "COINBASE", TIMESTAMP: "TIMESTAMP", NUMBER: "NUMBER",
	PREVRANDAO: "PREVRANDAO", GASLIMIT: "GASLIMIT", CHAINID: "CHAINID", SELFBALANCE: "SELFBALANCE",
	BASEFEE: "BASEFEE", BLOBHASH: "BLOBHASH", BLOBBASEFEE: "BLOBBASEFEE",
	POP: "POP", MLOAD: "MLOAD", MSTORE: "MSTORE", MSTORE8: "MSTORE8", SLOAD: "SLOAD", SSTORE: "SSTORE",
	JUMP: "JUMP", JUMPI: "JUMPI", PC: "PC", MSIZE: "MSIZE", GAS: "GAS", JUMPDEST: "JUMPDEST",
	TLOAD: "TLOAD", TSTORE: "TSTORE", MCOPY: "MCOPY", PUSH0: "PUSH0",
	CREATE: "CREATE", CALL: "CALL", CALLCODE: "CALLCODE", RETURN: "RETURN", DELEGATECALL: "DELEGATECALL",
	CREATE2: "CREATE2", STATICCALL: "STATICCALL", REVERT: "REVERT", INVALID: "INVALID",
	SELFDESTRUCT: "SELFDESTRUCT",
}

func (op OpCode) String() string {
	name := opNames[op]
	if name != "" {
		return name
	}
	if op >= PUSH1 && op <= PUSH32 {
		return "PUSH" + smallDecimal(int(op-PUSH1)+1)
	}
	if op >= DUP1 && op <= DUP16 {
		return "DUP" + smallDecimal(int(op-DUP1)+1)
	}
	if op >= SWAP1 && op <= SWAP16 {
		return "SWAP" + smallDecimal(int(op-SWAP1)+1)
	}
	if op >= LOG0 && op <= LOG4 {
		return "LOG" + smallDecimal(int(op-LOG0))
	}
	return "0x" + string(hexDigit(byte(op)>>4)) + string(hexDigit(byte(op)))
}

func smallDecimal(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func hexDigit(nibble byte) byte {
	return "0123456789abcdef"[nibble&0xf]
}
