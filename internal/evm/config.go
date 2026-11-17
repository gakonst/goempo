package evm

import "github.com/gakonst/goempo/internal/u256"

type Fork uint8

const (
	Berlin Fork = iota + 1
	London
	Paris
	Shanghai
	Cancun
	Prague
	Osaka
)

var forkNames = map[string]Fork{
	"Berlin":   Berlin,
	"London":   London,
	"Paris":    Paris,
	"Merge":    Paris,
	"Shanghai": Shanghai,
	"Cancun":   Cancun,
	"Prague":   Prague,
	"Osaka":    Osaka,
}

func ParseFork(name string) (Fork, bool) {
	fork, ok := forkNames[name]
	return fork, ok
}

type Config struct {
	Fork        Fork
	ChainID     u256.Int
	Gas         *GasTable
	Table       *JumpTable
	Precompiles PrecompileLookup
}

func NewConfig(fork Fork, chainID u256.Int) *Config {
	gas := GasForFork(fork)
	return &Config{
		Fork:        fork,
		ChainID:     chainID,
		Gas:         gas,
		Table:       NewJumpTable(fork, gas),
		Precompiles: EthereumPrecompiles(fork),
	}
}
