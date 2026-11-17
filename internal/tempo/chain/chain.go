package chain

import (
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type Address = primitives.Address

type gasAmount uint64

const (
	sstoreLoadGas        = 100
	sstoreCreateGas      = 250_000
	sstoreSetGasT7       = 5_000
	contractCreateGas    = 500_000
	newAccountGas        = 250_000
	codeDepositGasT1     = 1_000
	authGasT0            = 25_000
	authRefundT0         = 12_500
	authGasT1            = 12_500
	txGasCapT0           = 16_777_216
	txGasCapT1A          = 30_000_000
	baseFeeT0            = 10_000_000_000
	baseFeeT1            = 20_000_000_000
	millisTimestampGas   = 2
	millisPerSecond      = 1_000
	systemCallGas        = 30_000_000
	tempoSystemCallGas   = 250_000_000
	generalGasLimitT1    = 30_000_000
	baseFeeTargetT7      = 10_000_000
	baseFeeDenominatorT7 = 8
	baseFeeMinT7         = 600_000_000
	baseFeeMaxT7         = 12_000_000_000
	sharedGasDivisor     = 10
)

const (
	MillisTimestamp evm.OpCode = 0x4f
)

var (
	HistoryStorageAddress = Address{0x00, 0x00, 0xf9, 0x08, 0x27, 0xf1, 0xc5, 0x3a, 0x10, 0xcb, 0x7a, 0x02, 0x33, 0x5b, 0x17, 0x53, 0x20, 0x00, 0x29, 0x35}
	SystemAddress         = Address{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe}
)

type Rules struct {
	ChainID hardfork.ChainID
	Fork    hardfork.Fork
}

func RulesAt(chainID hardfork.ChainID, timestamp uint64) Rules {
	return Rules{ChainID: chainID, Fork: hardfork.At(chainID, timestamp)}
}

func (r Rules) AtLeast(f hardfork.Fork) bool { return r.Fork.AtLeast(f) }

func (r Rules) GasTable() *evm.GasTable {
	g := evm.GasForFork(evm.Osaka)
	if r.AtLeast(hardfork.T1) {
		g.SstoreSet = sstoreCreateGas + sstoreLoadGas
		g.Create = contractCreateGas
		g.NewAccount = newAccountGas
		g.CodeDepositByte = codeDepositGasT1
	}
	if r.AtLeast(hardfork.T7) {
		g.SstoreSet = sstoreSetGasT7 + sstoreLoadGas
		g.SstoreSetRefund = sstoreSetGasT7
		g.SstoreClearRefund = 0
		g.MaxRefundQuotient = 1
	}
	return g
}

func (r Rules) EthereumFork() evm.Fork {
	if r.AtLeast(hardfork.T1C) {
		return evm.Osaka
	}
	return evm.Prague
}

func (r Rules) EthereumPrecompiles() *evm.PrecompileSet {
	return evm.EthereumPrecompiles(r.EthereumFork())
}

func (r Rules) JumpTable(g *evm.GasTable) *evm.JumpTable {
	t := evm.NewJumpTable(evm.Osaka, g)
	if !r.AtLeast(hardfork.T1C) {
		t.Set(MillisTimestamp, evm.Operation{
			Exec: opMillisTimestamp,
			Gas:  millisTimestampGas,
			Out:  1,
		})
	}
	return t
}

func opMillisTimestamp(e *evm.EVM, f *evm.Frame) evm.Status {
	f.Push(u256.FromUint64(e.Block.TimeMillis))
	return evm.StatusContinue
}

func (r Rules) Config(precompiles evm.PrecompileLookup) *evm.Config {
	g := r.GasTable()
	if precompiles == nil {
		precompiles = r.EthereumPrecompiles()
	}
	return r.config(g, r.JumpTable(g), precompiles)
}

func (r Rules) config(g *evm.GasTable, t *evm.JumpTable, precompiles evm.PrecompileLookup) *evm.Config {
	return &evm.Config{
		Fork:        evm.Osaka,
		ChainID:     u256.FromUint64(uint64(r.ChainID)),
		Gas:         g,
		Table:       t,
		Precompiles: precompiles,
	}
}

func (r Rules) TxGasLimitCap() uint64 {
	if r.AtLeast(hardfork.T1A) {
		return txGasCapT1A
	}
	return txGasCapT0
}

func (r Rules) NewAccountNonceGas() uint64 {
	if r.AtLeast(hardfork.T1) {
		return newAccountGas
	}
	return 0
}

func (r Rules) AuthorizationGas() uint64 {
	if r.AtLeast(hardfork.T1) {
		return authGasT1
	}
	return authGasT0
}

func (r Rules) AuthorizationRefund() uint64 {
	if r.AtLeast(hardfork.T1) {
		return 0
	}
	return authRefundT0
}

func (r Rules) FixedBaseFee() uint64 {
	if r.AtLeast(hardfork.T1) {
		return baseFeeT1
	}
	return baseFeeT0
}

type ParentGas struct {
	BaseFee uint64
	GasUsed uint64
}

func (r Rules) ExpectedBaseFee(parent ParentGas) uint64 {
	if !r.AtLeast(hardfork.T7) {
		return r.FixedBaseFee()
	}
	if parent.GasUsed == baseFeeTargetT7 {
		return clampBaseFee(parent.BaseFee)
	}
	if parent.GasUsed > baseFeeTargetT7 {
		delta := mulDiv(parent.BaseFee, gasAmount(parent.GasUsed-baseFeeTargetT7))
		return clampBaseFee(parent.BaseFee + max(delta, 1))
	}
	delta := mulDiv(parent.BaseFee, gasAmount(baseFeeTargetT7-parent.GasUsed))
	if delta >= parent.BaseFee {
		return baseFeeMinT7
	}
	return clampBaseFee(parent.BaseFee - delta)
}

func mulDiv(fee uint64, gasDelta gasAmount) uint64 {
	v := u256.FromUint64(fee).Mul(u256.FromUint64(uint64(gasDelta)))
	v = v.Div(u256.FromUint64(baseFeeTargetT7)).Div(u256.FromUint64(baseFeeDenominatorT7))
	return v.Uint64()
}

func clampBaseFee(fee uint64) uint64 {
	return min(max(fee, baseFeeMinT7), baseFeeMaxT7)
}

func (r Rules) HasSystemTransaction() bool { return !r.AtLeast(hardfork.T4) }

func (r Rules) SystemCallGas() uint64 { return systemCallGas }

func (r Rules) TempoSystemCallGas() uint64 { return tempoSystemCallGas }

func (r Rules) SharedGasLimit(gasLimit uint64) uint64 {
	if r.AtLeast(hardfork.T4) {
		return 0
	}
	return gasLimit / sharedGasDivisor
}

func (r Rules) GeneralGasLimit(gasLimit uint64) uint64 {
	if r.AtLeast(hardfork.T1) {
		return generalGasLimitT1
	}
	return (gasLimit - r.SharedGasLimit(gasLimit)) / 2
}

type Timestamp struct {
	Seconds    uint64
	MillisPart uint64
}

func (t Timestamp) Millis() uint64 {
	return t.Seconds*millisPerSecond + t.MillisPart
}
