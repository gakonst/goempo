package evm_test

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

// The interpreter runs stock opcodes inline; a table whose every entry was
// Set again runs each one through its Exec. Both must agree on every
// program: status, gas, output and storage.

func tableOnly(cfg *evm.Config) *evm.Config {
	slow := *cfg.Table
	for op := 0; op < len(slow); op++ {
		if slow[op].Defined {
			slow.Set(evm.OpCode(op), slow[op])
		}
	}
	out := *cfg
	out.Table = &slow
	return &out
}

type outcome struct {
	result  evm.Result
	storage [4]u256.Int
}

func runWith(cfg *evm.Config, code, input []byte, gas uint64) outcome {
	st := memstate.New()
	st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40)})
	st.Put(contract, memstate.Account{Code: code, Nonce: 1})
	st.WarmAddress(sender)
	st.WarmAddress(contract)
	machine := evm.New(evm.Context{
		State:  st,
		Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
		Tx:     &evm.TxContext{Origin: sender},
		Config: cfg,
	})
	res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Input: input, Gas: gas})
	var o outcome
	o.result = res
	for i := 0; i < len(o.storage); i++ {
		o.storage[i] = st.Storage(contract, primitives.Hash(u256.FromUint64(uint64(i)).Bytes32()))
	}
	return o
}

func sameOutcome(a, b outcome) bool {
	return a.result.Status == b.result.Status && a.result.GasLeft == b.result.GasLeft &&
		bytes.Equal(a.result.Output, b.result.Output) && a.storage == b.storage
}

// randomProgram favours the inline opcodes, small memory offsets and jumps
// that often land on a JUMPDEST.
func randomProgram(rng *rand.Rand) []byte {
	inline := []byte{
		0x00, 0x5b, 0x50, 0x5f, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x0b,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b,
		0x1c, 0x1d, 0x56, 0x57, 0x58, 0x5a, 0x51, 0x52, 0x59, 0x35, 0x55, 0x54,
	}
	n := 1 + rng.IntN(120)
	code := make([]byte, 0, n*4)
	for len(code) < n {
		switch r := rng.IntN(16); {
		case r < 5:
			code = append(code, inline[rng.IntN(len(inline))])
		case r < 9:
			width := 1 + rng.IntN(32)
			if rng.IntN(3) > 0 {
				width = 1 + rng.IntN(2)
			}
			code = append(code, 0x60+byte(width-1))
			for i := 0; i < width; i++ {
				if rng.IntN(4) == 0 {
					code = append(code, byte(rng.Uint32()))
				} else {
					code = append(code, byte(rng.IntN(70)))
				}
			}
		case r < 12:
			code = append(code, 0x80+byte(rng.IntN(16)))
		case r < 14:
			code = append(code, 0x90+byte(rng.IntN(16)))
		case r < 15:
			code = append(code, byte(rng.Uint32()))
		default:
			code = append(code, 0x60, byte(rng.IntN(n+4)), 0x56+byte(rng.IntN(2)))
		}
	}
	if rng.IntN(2) == 0 {
		// RETURN memory[0:64], so memory is part of the outcome.
		code = append(code, 0x60, 0x40, 0x60, 0x00, 0xf3)
	}
	if rng.IntN(8) == 0 && len(code) > 2 {
		// A PUSH cut off by the code's end.
		code = append(code, 0x60+byte(rng.IntN(32)))
	}
	return code
}

func TestInlineMatchesTable(t *testing.T) {
	fast := evm.NewConfig(evm.Osaka, u256.FromUint64(1))
	slow := tableOnly(fast)
	rng := rand.New(rand.NewPCG(1, 2))
	input := make([]byte, 40)
	for i := range input {
		input[i] = byte(rng.Uint32())
	}
	for i := 0; i < 30000; i++ {
		code := randomProgram(rng)
		gas := uint64(50 + rng.IntN(5000))
		a := runWith(fast, code, input, gas)
		b := runWith(slow, code, input, gas)
		if !sameOutcome(a, b) {
			t.Fatalf("program %d %x gas %d: inline %+v table %+v", i, code, gas, a, b)
		}
	}
}

func FuzzInlineMatchesTable(f *testing.F) {
	fast := evm.NewConfig(evm.Osaka, u256.FromUint64(1))
	slow := tableOnly(fast)
	f.Add(arithLoop().code, uint16(5000))
	f.Add(dispatchLoop().code, uint16(5000))
	f.Fuzz(func(t *testing.T, code []byte, gas uint16) {
		a := runWith(fast, code, nil, uint64(gas))
		b := runWith(slow, code, nil, uint64(gas))
		if !sameOutcome(a, b) {
			t.Fatalf("%x gas %d: inline %+v table %+v", code, gas, a, b)
		}
	})
}
