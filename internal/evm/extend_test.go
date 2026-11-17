package evm_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type fixedPrecompile struct{}

func (fixedPrecompile) Run(call *evm.PrecompileCall) evm.PrecompileResult {
	call.EVM.State.SetStorage(call.Address, primitives.Hash{}, u256.FromUint64(9))
	return evm.PrecompileResult{Output: []byte{0x2a}, GasUsed: 100, Status: evm.StatusReturn}
}

func TestChainConfigSwapsGasOpcodesAndPrecompiles(t *testing.T) {
	cfg := evm.NewConfig(evm.Osaka, u256.FromUint64(4217))
	cfg.Gas.SstoreSet = 250_000
	cfg.Table = evm.NewJumpTable(evm.Osaka, cfg.Gas)
	cfg.Table.Unset(evm.SELFDESTRUCT)
	custom := evm.PrecompileAddress(0x20c0)
	set := evm.EthereumPrecompiles(evm.Osaka)
	set.Set(custom, fixedPrecompile{})
	cfg.Precompiles = set

	st := memstate.New()
	st.Put(contract, memstate.Account{Code: []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x00}, Nonce: 1})
	st.WarmAddress(contract)
	machine := evm.New(evm.Context{State: st, Block: &evm.BlockContext{}, Tx: &evm.TxContext{}, Config: cfg})
	res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 400_000})
	assert.Equal(res.Status, evm.StatusStop, "status")
	assert.Equal(400_000-res.GasLeft, uint64(3+3+2100+250_000), "swapped SSTORE set cost")

	res = machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: custom, Gas: 1000})
	assert.Equal(res.Status, evm.StatusReturn, "custom precompile")
	assert.Equal(string(res.Output), "\x2a", "custom output")
	assert.Equal(st.Storage(custom, primitives.Hash{}), u256.FromUint64(9), "stateful precompile wrote state")

	st.Put(other, memstate.Account{Code: []byte{0x60, 0x00, 0xff}, Nonce: 1})
	res = machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: other, Gas: 100_000})
	assert.Equal(res.Status, evm.StatusInvalidOpcode, "unset opcode is invalid")
}
