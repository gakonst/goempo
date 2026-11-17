package evm_test

import (
	"bytes"
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// A reused EVM leaves nothing of one message in the next: memory a message
// wrote reads back as zero after Release and Reset, and outputs do not alias.
func TestReusedEVMStartsClean(t *testing.T) {
	// MSTORE 0x2a at 0, then RETURN 32 bytes from 0 on the first call; the
	// second program returns memory it never wrote.
	writer := []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}
	reader := []byte{0x60, 0x20, 0x60, 0x00, 0xf3}
	newState := func(code []byte) *memstate.State {
		st := memstate.New()
		st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40)})
		st.Put(contract, memstate.Account{Code: code, Nonce: 1})
		st.WarmAddress(sender)
		st.WarmAddress(contract)
		return st
	}
	ctx := func(st *memstate.State) evm.Context {
		return evm.Context{
			State:  st,
			Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
			Tx:     &evm.TxContext{Origin: sender},
			Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
		}
	}
	machine := evm.New(ctx(newState(writer)))
	first := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 100_000})
	assert.Equal(first.Status, evm.StatusReturn, "first status")
	want := make([]byte, 32)
	want[31] = 0x2a
	assert.Ok(bytes.Equal(first.Output, want), "first output")
	machine.Release()
	machine.Reset(ctx(newState(reader)))
	second := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 100_000})
	assert.Equal(second.Status, evm.StatusReturn, "second status")
	assert.Ok(bytes.Equal(second.Output, make([]byte, 32)), "memory starts zero")
	assert.Ok(bytes.Equal(first.Output, want), "first output is not aliased")
}
