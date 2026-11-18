package evm_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/u256"
)

// A hot contract loop: each benchmark runs one message whose code loops
// hotLoopIterations times over a fixed body, and reports ns per instruction.

const hotLoopIterations = 100_000

const hotLoopHead = 4 // the JUMPDEST after PUSH3 iterations

type hotLoop struct {
	code         []byte
	instructions int // per iteration, JUMPDEST through JUMPI
	input        []byte
}

func hotLoopCode(body []byte) []byte {
	n := uint32(hotLoopIterations)
	code := []byte{0x62, byte(n >> 16), byte(n >> 8), byte(n), 0x5b}
	code = append(code, body...)
	// PUSH1 1 SWAP1 SUB DUP1 PUSH1 hotLoopHead JUMPI STOP
	return append(code, 0x60, 0x01, 0x90, 0x03, 0x80, 0x60, hotLoopHead, 0x57, 0x00)
}

// arithLoop: stack, arithmetic and memory words.
func arithLoop() hotLoop {
	body := []byte{
		0x80,       // DUP1
		0x60, 0x20, // PUSH1 32
		0x51,       // MLOAD
		0x01,       // ADD
		0x60, 0x20, // PUSH1 32
		0x52,       // MSTORE
		0x80, 0x80, // DUP1 DUP1
		0x02,       // MUL
		0x60, 0xff, // PUSH1 0xff
		0x16,       // AND
		0x60, 0x00, // PUSH1 0
		0x52, // MSTORE
	}
	return hotLoop{code: hotLoopCode(body), instructions: 1 + 13 + 6}
}

// dispatchLoop: selector matching and wide pushes, as a contract's entry.
func dispatchLoop() hotLoop {
	body := []byte{0x60, 0x00, 0x35, 0x60, 0xe0, 0x1c, 0x63, 0xa9, 0x05, 0x9c, 0xbb, 0x14, 0x15, 0x50}
	body = append(body, 0x7f)
	for i := 0; i < 32; i++ {
		body = append(body, byte(i*7+1))
	}
	body = append(body, 0x73)
	for i := 0; i < 20; i++ {
		body = append(body, byte(i*3+2))
	}
	body = append(body, 0x18, 0x50) // XOR POP
	input := make([]byte, 36)
	input[0], input[1], input[2], input[3] = 0xa9, 0x05, 0x9c, 0xbb
	return hotLoop{code: hotLoopCode(body), instructions: 1 + 12 + 6, input: input}
}

func runHotLoop(b *testing.B, l hotLoop) {
	st := memstate.New()
	st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40)})
	st.Put(contract, memstate.Account{Code: l.code, Nonce: 1})
	st.WarmAddress(sender)
	st.WarmAddress(contract)
	ctx := evm.Context{
		State:  st,
		Block:  &evm.BlockContext{Number: 10, GasLimit: 1 << 40},
		Tx:     &evm.TxContext{Origin: sender},
		Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
	}
	machine := evm.New(ctx)
	msg := evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Input: l.input, Gas: 1 << 40}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := machine.Execute(msg)
		if res.Status != evm.StatusStop {
			b.Fatalf("loop ended with %v", res.Status)
		}
		machine.Release()
		machine.Reset(ctx)
	}
	b.StopTimer()
	instructions := float64(b.N) * float64(hotLoopIterations) * float64(l.instructions)
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/instructions, "ns/instr")
}

func BenchmarkHotLoopArith(b *testing.B) { runHotLoop(b, arithLoop()) }

func BenchmarkHotLoopDispatch(b *testing.B) { runHotLoop(b, dispatchLoop()) }

// The loops' gas pins their instruction counts and static costs.
func TestHotLoopGas(t *testing.T) {
	cases := []struct {
		loop    hotLoop
		perIter uint64
	}{
		// JUMPDEST 1, 17 three-gas ops, MUL 5, JUMPI 10.
		{arithLoop(), 1 + 17*3 + 5 + 10},
		// JUMPDEST 1, 17 three-gas ops (POP is 2: two of them), JUMPI 10.
		{dispatchLoop(), 1 + 16*3 + 2*2 + 10 - 3},
	}
	for i, c := range cases {
		r := execute(t, program{code: c.loop.code, gas: 1 << 40, input: c.loop.input})
		if r.result.Status != evm.StatusStop {
			t.Fatalf("loop %d: %v", i, r.result.Status)
		}
		used := uint64(1<<40) - r.result.GasLeft
		memory := uint64(0)
		if i == 0 {
			memory = 6 // two words
		}
		want := 3 + c.perIter*hotLoopIterations + memory
		if used != want {
			t.Fatalf("loop %d used %d gas, want %d", i, used, want)
		}
	}
}
