package evm

const (
	memoryBytesMax     = 1 << 32
	memoryWordsMax     = memoryBytesMax / wordBytes
	wordBytes          = 32
	quadDivisorDefault = 512
)

const _ = uint64(1<<62 - memoryWordsMax*memoryWordsMax)

const _ = uint64(memoryBytesMax - memoryWordsMax*wordBytes)

type GasTable struct {
	Base              uint64
	VeryLow           uint64
	Low               uint64
	Mid               uint64
	High              uint64
	JumpDest          uint64
	WarmAccess        uint64
	ColdAccountAccess uint64
	ColdSload         uint64
	SstoreSet         uint64
	SstoreReset       uint64
	SstoreClearRefund uint64
	SstoreSentry      uint64
	CallValue         uint64
	CallStipend       uint64
	NewAccount        uint64
	Create            uint64
	CodeDepositByte   uint64
	InitCodeWord      uint64
	Keccak            uint64
	KeccakWord        uint64
	CopyWord          uint64
	Log               uint64
	LogTopic          uint64
	LogDataByte       uint64
	Exp               uint64
	ExpByte           uint64
	BlockHash         uint64
	SelfDestruct      uint64
	Memory            uint64
	MemoryQuadDivisor uint64
	MaxRefundQuotient uint64
	MaxCodeSize       int
	MaxInitCodeSize   int
}

func OsakaGas() *GasTable {
	return &GasTable{
		Base:              2,
		VeryLow:           3,
		Low:               5,
		Mid:               8,
		High:              10,
		JumpDest:          1,
		WarmAccess:        100,
		ColdAccountAccess: 2600,
		ColdSload:         2100,
		SstoreSet:         20000,
		SstoreReset:       2900,
		SstoreClearRefund: 4800,
		SstoreSentry:      2300,
		CallValue:         9000,
		CallStipend:       2300,
		NewAccount:        25000,
		Create:            32000,
		CodeDepositByte:   200,
		InitCodeWord:      2,
		Keccak:            30,
		KeccakWord:        6,
		CopyWord:          3,
		Log:               375,
		LogTopic:          375,
		LogDataByte:       8,
		Exp:               10,
		ExpByte:           50,
		BlockHash:         20,
		SelfDestruct:      5000,
		Memory:            3,
		MemoryQuadDivisor: quadDivisorDefault,
		MaxRefundQuotient: 5,
		MaxCodeSize:       24576,
		MaxInitCodeSize:   49152,
	}
}

func GasForFork(fork Fork) *GasTable {
	table := OsakaGas()
	if fork < Shanghai {
		table.InitCodeWord = 0
		table.MaxInitCodeSize = 0
	}
	if fork < London {
		table.SstoreClearRefund = 15000
		table.MaxRefundQuotient = 2
	}
	return table
}

func (g *GasTable) MemoryCost(words uint64) uint64 {
	if words > memoryWordsMax {
		return ^uint64(0)
	}
	return words*g.Memory + words*words/g.MemoryQuadDivisor
}

func toWords(size uint64) uint64 {
	return (size + wordBytes - 1) / wordBytes
}
