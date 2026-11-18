package evm

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// interpret runs frame f until it halts or calls out.
//
// The loop keeps pc, gas and the stack depth in locals and dispatches once,
// on the opcode, through one switch. PUSHn, DUPn, SWAPn, POP, the stack-only
// arithmetic, comparison and bitwise ops, JUMP, JUMPI, PC, GAS and MLOAD or
// MSTORE within current memory run inline. Every other opcode, an opcode an
// extension replaced (Set clears its inline mark), or an inline opcode off
// its fast case syncs the locals to the frame and runs its table entry.
//
// The checks before an opcode (defined, underflow, overflow, static gas) run
// for every opcode in the table path's order, so statuses and gas are the
// same whichever way an opcode runs.
func (e *EVM) interpret(f *Frame) Status {
	table := e.table
	code := f.code
	jumpdests := f.jumpdests
	assert.Equal(len(f.stack), StackLimit, "a frame's stack is preallocated")
	stack := (*[StackLimit]u256.Int)(f.stack)
	pc, gas, sp := f.pc, f.gas, f.sp
	status := StatusContinue
run:
	for steps := 0; steps < instructionsMax; steps++ {
		op := STOP
		if pc < len(code) {
			op = OpCode(code[pc])
		}
		o := &table[op]
		if !o.Defined {
			status = StatusInvalidOpcode
			break
		}
		if sp < o.In {
			status = StatusStackUnderflow
			break
		}
		if sp-o.In+o.Out > StackLimit {
			status = StatusStackOverflow
			break
		}
		if gas < o.Gas {
			status = StatusOutOfGas
			break
		}
		gas -= o.Gas
		pc++
		slow := !o.inline
		if !slow {
			switch op {
			case STOP:
				status = StatusStop
				break run
			case JUMPDEST:
			case POP:
				sp--
			case PUSH0:
				stack[sp] = u256.Int{}
				sp++
			case PUSH1:
				var v uint64
				if pc < len(code) {
					v = uint64(code[pc])
				}
				stack[sp] = u256.Int{v}
				sp++
				pc++
			case PUSH1 + 1, PUSH1 + 2, PUSH1 + 3, PUSH1 + 4, PUSH1 + 5, PUSH1 + 6, PUSH1 + 7, PUSH1 + 8, PUSH1 + 9, PUSH1 + 10, PUSH1 + 11, PUSH1 + 12, PUSH1 + 13, PUSH1 + 14, PUSH1 + 15, PUSH1 + 16, PUSH1 + 17, PUSH1 + 18, PUSH1 + 19, PUSH1 + 20, PUSH1 + 21, PUSH1 + 22, PUSH1 + 23, PUSH1 + 24, PUSH1 + 25, PUSH1 + 26, PUSH1 + 27, PUSH1 + 28, PUSH1 + 29, PUSH1 + 30, PUSH1 + 31:
				n := int(op-PUSH1) + 1
				stack[sp] = pushImmediate(code, pc, n)
				sp++
				pc += n
			case DUP1, DUP1 + 1, DUP1 + 2, DUP1 + 3, DUP1 + 4, DUP1 + 5, DUP1 + 6, DUP1 + 7, DUP1 + 8, DUP1 + 9, DUP1 + 10, DUP1 + 11, DUP1 + 12, DUP1 + 13, DUP1 + 14, DUP1 + 15:
				stack[sp] = stack[sp-1-int(op-DUP1)]
				sp++
			case SWAP1, SWAP1 + 1, SWAP1 + 2, SWAP1 + 3, SWAP1 + 4, SWAP1 + 5, SWAP1 + 6, SWAP1 + 7, SWAP1 + 8, SWAP1 + 9, SWAP1 + 10, SWAP1 + 11, SWAP1 + 12, SWAP1 + 13, SWAP1 + 14, SWAP1 + 15:
				other := sp - 2 - int(op-SWAP1)
				stack[sp-1], stack[other] = stack[other], stack[sp-1]
			case ADD:
				stack[sp-2] = stack[sp-1].Add(stack[sp-2])
				sp--
			case SUB:
				stack[sp-2] = stack[sp-1].Sub(stack[sp-2])
				sp--
			case MUL:
				stack[sp-2] = stack[sp-1].Mul(stack[sp-2])
				sp--
			case DIV:
				stack[sp-2] = stack[sp-1].Div(stack[sp-2])
				sp--
			case SDIV:
				stack[sp-2] = stack[sp-1].SDiv(stack[sp-2])
				sp--
			case MOD:
				stack[sp-2] = stack[sp-1].Mod(stack[sp-2])
				sp--
			case SMOD:
				stack[sp-2] = stack[sp-1].SMod(stack[sp-2])
				sp--
			case SIGNEXTEND:
				stack[sp-2] = stack[sp-2].SignExtend(stack[sp-1])
				sp--
			case LT:
				stack[sp-2] = wordOf(stack[sp-1].Lt(stack[sp-2]))
				sp--
			case GT:
				stack[sp-2] = wordOf(stack[sp-1].Gt(stack[sp-2]))
				sp--
			case SLT:
				stack[sp-2] = wordOf(stack[sp-1].Slt(stack[sp-2]))
				sp--
			case SGT:
				stack[sp-2] = wordOf(stack[sp-1].Sgt(stack[sp-2]))
				sp--
			case EQ:
				stack[sp-2] = wordOf(stack[sp-1] == stack[sp-2])
				sp--
			case ISZERO:
				stack[sp-1] = wordOf(stack[sp-1].IsZero())
			case AND:
				stack[sp-2] = stack[sp-1].And(stack[sp-2])
				sp--
			case OR:
				stack[sp-2] = stack[sp-1].Or(stack[sp-2])
				sp--
			case XOR:
				stack[sp-2] = stack[sp-1].Xor(stack[sp-2])
				sp--
			case NOT:
				stack[sp-1] = stack[sp-1].Not()
			case BYTE:
				stack[sp-2] = stack[sp-2].Byte(stack[sp-1])
				sp--
			case SHL:
				stack[sp-2] = stack[sp-2].Shl(stack[sp-1])
				sp--
			case SHR:
				stack[sp-2] = stack[sp-2].Shr(stack[sp-1])
				sp--
			case SAR:
				stack[sp-2] = stack[sp-2].Sar(stack[sp-1])
				sp--
			case JUMP:
				dest, ok := jumpTarget(code, jumpdests, stack[sp-1])
				sp--
				if !ok {
					status = StatusInvalidJump
					break run
				}
				pc = dest
			case JUMPI:
				taken := !stack[sp-2].IsZero()
				dest, ok := jumpTarget(code, jumpdests, stack[sp-1])
				sp -= 2
				if taken && !ok {
					status = StatusInvalidJump
					break run
				}
				if taken {
					pc = dest
				}
			case PC:
				stack[sp] = u256.Int{uint64(pc - 1)}
				sp++
			case GAS:
				stack[sp] = u256.Int{gas}
				sp++
			case MLOAD:
				offset, ok := wordInMemory(f.memory, stack[sp-1])
				if ok {
					stack[sp-1] = loadWord(f.memory[offset : offset+wordBytes])
				}
				slow = !ok
			case MSTORE:
				offset, ok := wordInMemory(f.memory, stack[sp-1])
				if ok {
					storeWord(f.memory[offset:offset+wordBytes], stack[sp-2])
					sp -= 2
				}
				slow = !ok
			default:
				slow = true
			}
		}
		if slow {
			f.pc, f.gas, f.sp = pc, gas, sp
			status = o.Exec(e, f)
			pc, gas, sp = f.pc, f.gas, f.sp
			if status != StatusContinue {
				break
			}
		}
	}
	assert.Ok(status != StatusContinue, "evm interpret reached instructionsMax")
	f.pc, f.gas, f.sp = pc, gas, sp
	return status
}

// inlineOps are the opcodes interpret runs inline when the table holds the
// stock operation for them.
var inlineOps = [...]OpCode{
	STOP, JUMPDEST, POP, PUSH0, PUSH1, PUSH1 + 1, PUSH1 + 2, PUSH1 + 3, PUSH1 + 4, PUSH1 + 5, PUSH1 + 6, PUSH1 + 7, PUSH1 + 8, PUSH1 + 9, PUSH1 + 10, PUSH1 + 11, PUSH1 + 12, PUSH1 + 13, PUSH1 + 14, PUSH1 + 15, PUSH1 + 16, PUSH1 + 17, PUSH1 + 18, PUSH1 + 19, PUSH1 + 20, PUSH1 + 21, PUSH1 + 22, PUSH1 + 23, PUSH1 + 24, PUSH1 + 25, PUSH1 + 26, PUSH1 + 27, PUSH1 + 28, PUSH1 + 29, PUSH1 + 30, PUSH1 + 31,
	DUP1, DUP1 + 1, DUP1 + 2, DUP1 + 3, DUP1 + 4, DUP1 + 5, DUP1 + 6, DUP1 + 7, DUP1 + 8, DUP1 + 9, DUP1 + 10, DUP1 + 11, DUP1 + 12, DUP1 + 13, DUP1 + 14, DUP1 + 15,
	SWAP1, SWAP1 + 1, SWAP1 + 2, SWAP1 + 3, SWAP1 + 4, SWAP1 + 5, SWAP1 + 6, SWAP1 + 7, SWAP1 + 8, SWAP1 + 9, SWAP1 + 10, SWAP1 + 11, SWAP1 + 12, SWAP1 + 13, SWAP1 + 14, SWAP1 + 15,
	ADD, SUB, MUL, DIV, SDIV, MOD, SMOD, SIGNEXTEND, LT, GT, SLT, SGT, EQ,
	ISZERO, AND, OR, XOR, NOT, BYTE, SHL, SHR, SAR, JUMP, JUMPI, PC, GAS,
	MLOAD, MSTORE,
}

func markInline(t *JumpTable) {
	for i := 0; i < len(inlineOps); i++ {
		o := &t[inlineOps[i]]
		o.inline = o.Defined
	}
}

func wordOf(b bool) u256.Int {
	if b {
		return u256.Int{1}
	}
	return u256.Int{}
}

// pushImmediate reads PUSHn's n-byte immediate at code[start:]. Within the
// code it loads each limb with one 8-byte big-endian load; an immediate cut
// off by the code's end reads its missing bytes as zeros.
func pushImmediate(code []byte, start int, n int) u256.Int {
	assert.Ok(n >= 1 && n <= u256.Bytes, "push immediates are 1..32 bytes")
	end := start + n
	if end > len(code) {
		var buf [u256.Bytes]byte
		if start < len(code) {
			copy(buf[u256.Bytes-n:], code[start:])
		}
		return u256.FromBytes32(buf)
	}
	var z u256.Int
	limb := 0
	for ; end-start >= 8; limb++ {
		z[limb] = binary.BigEndian.Uint64(code[end-8 : end])
		end -= 8
	}
	rest := end - start
	if rest == 0 {
		return z
	}
	if end >= 8 {
		z[limb] = binary.BigEndian.Uint64(code[end-8:end]) & (1<<(8*uint(rest)) - 1)
		return z
	}
	var v uint64
	for i := start; i < end; i++ {
		v = v<<8 | uint64(code[i])
	}
	z[limb] = v
	return z
}

func jumpTarget(code []byte, jumpdests []uint64, dest u256.Int) (int, bool) {
	if !dest.IsUint64() {
		return 0, false
	}
	d := dest.Uint64()
	if d >= uint64(len(code)) {
		return 0, false
	}
	return int(d), jumpdests[d/64]&(1<<(d%64)) != 0
}

// wordInMemory reports whether the word at offset lies within memory as it
// is, so MLOAD or MSTORE there needs no expansion and costs only static gas.
func wordInMemory(memory []byte, offset u256.Int) (uint64, bool) {
	if !offset.IsUint64() {
		return 0, false
	}
	o := offset.Uint64()
	size := uint64(len(memory))
	return o, size >= wordBytes && o <= size-wordBytes
}

func loadWord(b []byte) u256.Int {
	_ = b[wordBytes-1]
	return u256.Int{
		binary.BigEndian.Uint64(b[24:32]),
		binary.BigEndian.Uint64(b[16:24]),
		binary.BigEndian.Uint64(b[8:16]),
		binary.BigEndian.Uint64(b[0:8]),
	}
}

func storeWord(b []byte, v u256.Int) {
	_ = b[wordBytes-1]
	binary.BigEndian.PutUint64(b[0:8], v[3])
	binary.BigEndian.PutUint64(b[8:16], v[2])
	binary.BigEndian.PutUint64(b[16:24], v[1])
	binary.BigEndian.PutUint64(b[24:32], v[0])
}
