package chain_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

func rules(f hardfork.Fork) chain.Rules {
	return chain.Rules{ChainID: hardfork.MainnetChainID, Fork: f}
}

func TestGasTablePerFork(t *testing.T) {
	g := rules(hardfork.T0).GasTable()
	assert.Equal(g.SstoreSet, uint64(20_000), "T0 sstore set")
	assert.Equal(g.Create, uint64(32_000), "T0 create")
	assert.Equal(g.NewAccount, uint64(25_000), "T0 new account")
	assert.Equal(g.CodeDepositByte, uint64(200), "T0 code deposit")
	assert.Equal(g.MaxRefundQuotient, uint64(5), "T0 refund quotient")

	g = rules(hardfork.T1).GasTable()
	assert.Equal(g.SstoreSet, uint64(250_100), "T1 sstore set")
	assert.Equal(g.SstoreSetRefund, uint64(19_900), "T1 set refund")
	assert.Equal(g.Create, uint64(500_000), "T1 create")
	assert.Equal(g.NewAccount, uint64(250_000), "T1 new account")
	assert.Equal(g.CodeDepositByte, uint64(1_000), "T1 code deposit")
	assert.Equal(g.SstoreClearRefund, uint64(4_800), "T1 clear refund")

	g = rules(hardfork.T7).GasTable()
	assert.Equal(g.SstoreSet, uint64(5_100), "T7 sstore set")
	assert.Equal(g.SstoreSetRefund, uint64(5_000), "T7 set refund")
	assert.Equal(g.SstoreClearRefund, uint64(0), "T7 clear refund")
	assert.Equal(g.MaxRefundQuotient, uint64(1), "T7 refund quotient")
	assert.Equal(g.Create, uint64(500_000), "T7 create")
}

func TestEthereumForkAndPrecompiles(t *testing.T) {
	p256 := evm.PrecompileAddress(0x100)
	_, before := rules(hardfork.T1B).EthereumPrecompiles().Lookup(p256)
	_, after := rules(hardfork.T1C).EthereumPrecompiles().Lookup(p256)
	assert.Ok(!before, "P256VERIFY absent before T1C")
	assert.Ok(after, "P256VERIFY present from T1C")
	assert.Equal(rules(hardfork.T0).EthereumFork(), evm.Prague, "precompiles of Prague at T0")
	assert.Equal(rules(hardfork.T13).EthereumFork(), evm.Osaka, "precompiles of Osaka at T13")
}

func TestMillisTimestampBeforeT1C(t *testing.T) {
	ts := chain.Timestamp{Seconds: 1_768_000_000, MillisPart: 417}
	code := []byte{byte(chain.MillisTimestamp), 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}
	contract := primitives.Address{0x42}
	run := func(f hardfork.Fork) evm.Result {
		st := memstate.New()
		st.Put(contract, memstate.Account{Code: code, Nonce: 1})
		cfg := rules(f).Config(ts.Millis(), nil)
		machine := evm.New(evm.Context{State: st, Block: &evm.BlockContext{}, Tx: &evm.TxContext{}, Config: cfg})
		return machine.Execute(evm.Message{Kind: evm.KindCall, To: contract, Gas: 100_000})
	}
	res := run(hardfork.T1B)
	assert.Equal(res.Status, evm.StatusReturn, "status before T1C")
	assert.Equal(len(res.Output), 32, "output length")
	var want [32]byte
	ms := ts.Millis()
	for i := 0; i < 8; i++ {
		want[31-i] = byte(ms >> (8 * i))
	}
	assert.Equal([32]byte(res.Output), want, "millis timestamp")
	assert.Equal(100_000-res.GasLeft, uint64(2+3+6+3+3), "gas of MILLIS_TIMESTAMP is 2")
	res = run(hardfork.T1C)
	assert.Equal(res.Status, evm.StatusInvalidOpcode, "undefined from T1C")
}

func TestLimitsPerFork(t *testing.T) {
	assert.Equal(rules(hardfork.T1).TxGasLimitCap(), uint64(16_777_216), "cap before T1A")
	assert.Equal(rules(hardfork.T1A).TxGasLimitCap(), uint64(30_000_000), "cap from T1A")
	assert.Equal(rules(hardfork.T0).FixedBaseFee(), uint64(10_000_000_000), "T0 base fee")
	assert.Equal(rules(hardfork.T1).FixedBaseFee(), uint64(20_000_000_000), "T1 base fee")
	assert.Equal(rules(hardfork.T0).NewAccountNonceGas(), uint64(0), "no nonce-zero charge at T0")
	assert.Equal(rules(hardfork.T1).NewAccountNonceGas(), uint64(250_000), "nonce-zero charge from T1")
	assert.Equal(rules(hardfork.T0).AuthorizationGas(), uint64(25_000), "7702 auth gas at T0")
	assert.Equal(rules(hardfork.T1).AuthorizationRefund(), uint64(0), "no 7702 refund from T1")
	assert.Equal(rules(hardfork.T0).GeneralGasLimit(500_000_000), uint64(225_000_000), "T0 general gas limit")
	assert.Equal(rules(hardfork.T0).SharedGasLimit(500_000_000), uint64(50_000_000), "T0 shared gas limit")
	assert.Ok(rules(hardfork.T3).HasSystemTransaction(), "system tx before T4")
	assert.Ok(!rules(hardfork.T4).HasSystemTransaction(), "no system tx from T4")
}

func TestBaseFeeT7(t *testing.T) {
	r := rules(hardfork.T7)
	assert.Equal(r.ExpectedBaseFee(chain.ParentGas{BaseFee: 1_000_000_000, GasUsed: 10_000_000}), uint64(1_000_000_000), "at target")
	assert.Equal(r.ExpectedBaseFee(chain.ParentGas{BaseFee: 1_000_000_000, GasUsed: 20_000_000}), uint64(1_125_000_000), "full")
	assert.Equal(r.ExpectedBaseFee(chain.ParentGas{BaseFee: 1_000_000_000, GasUsed: 0}), uint64(875_000_000), "empty")
	assert.Equal(r.ExpectedBaseFee(chain.ParentGas{BaseFee: 600_000_000, GasUsed: 0}), uint64(600_000_000), "floor")
	assert.Equal(r.ExpectedBaseFee(chain.ParentGas{BaseFee: 12_000_000_000, GasUsed: 30_000_000}), uint64(12_000_000_000), "ceiling")
	assert.Equal(rules(hardfork.T6).ExpectedBaseFee(chain.ParentGas{BaseFee: 1, GasUsed: 0}), uint64(20_000_000_000), "fixed before T7")
}
