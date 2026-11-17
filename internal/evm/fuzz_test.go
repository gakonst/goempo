package evm_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

func FuzzInterpreter(f *testing.F) {
	f.Add([]byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x00}, []byte{})
	f.Add([]byte{0x36, 0x60, 0x00, 0x60, 0x00, 0x37, 0x36, 0x60, 0x00, 0x60, 0x00, 0xf0, 0x00}, []byte{0x60, 0x00, 0xf3})
	f.Add([]byte{0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x30, 0x5a, 0xf4, 0x00}, []byte{})
	f.Fuzz(func(t *testing.T, code []byte, input []byte) {
		st := memstate.New()
		st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40)})
		st.Put(contract, memstate.Account{Code: code, Nonce: 1, Balance: u256.FromUint64(1000)})
		st.Put(other, memstate.Account{Code: code, Nonce: 1})
		st.WarmAddress(sender)
		st.WarmAddress(contract)
		machine := evm.New(evm.Context{
			State:  st,
			Block:  &evm.BlockContext{Number: 1, GasLimit: 30_000_000},
			Tx:     &evm.TxContext{Origin: sender},
			Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
		})
		gas := uint64(200_000)
		res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Input: input, Gas: gas})
		assert.Ok(res.GasLeft <= gas, "gas left never exceeds gas given")
		if res.Status.Halted() {
			assert.Equal(res.GasLeft, uint64(0), "an exceptional halt consumes all gas")
		}
	})
}
