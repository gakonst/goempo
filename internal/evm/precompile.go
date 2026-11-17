package evm

import (
	"crypto/sha256"

	"github.com/gakonst/goempo/internal/u256"
)

type PrecompileCall struct {
	EVM         *EVM
	Kind        CallKind
	Caller      Address
	Address     Address
	CodeAddress Address
	Value       u256.Int
	Input       []byte
	Gas         uint64
	Static      bool
}

type PrecompileResult struct {
	Output  []byte
	GasUsed uint64
	Status  Status
}

type Precompile interface {
	Run(call *PrecompileCall) PrecompileResult
}

type PrecompileLookup interface {
	Lookup(addr Address) (Precompile, bool)
}

type Pure interface {
	RequiredGas(input []byte) uint64
	Compute(input []byte) ([]byte, bool)
}

type PurePrecompile struct {
	P Pure
}

func (p PurePrecompile) Run(call *PrecompileCall) PrecompileResult {
	need := p.P.RequiredGas(call.Input)
	if need > call.Gas {
		return PrecompileResult{GasUsed: call.Gas, Status: StatusOutOfGas}
	}
	out, ok := p.P.Compute(call.Input)
	if !ok {
		return PrecompileResult{GasUsed: call.Gas, Status: StatusPrecompileFailure}
	}
	return PrecompileResult{Output: out, GasUsed: need, Status: StatusReturn}
}

type PrecompileSet struct {
	byAddress map[Address]Precompile
	addresses []Address
}

func NewPrecompileSet() *PrecompileSet {
	return &PrecompileSet{byAddress: make(map[Address]Precompile)}
}

func (s *PrecompileSet) Set(addr Address, p Precompile) {
	_, exists := s.byAddress[addr]
	if !exists {
		s.addresses = append(s.addresses, addr)
	}
	s.byAddress[addr] = p
}

func (s *PrecompileSet) Lookup(addr Address) (Precompile, bool) {
	p, ok := s.byAddress[addr]
	return p, ok
}

func (s *PrecompileSet) Addresses() []Address {
	return s.addresses
}

func PrecompileAddress(n uint64) Address {
	var a Address
	for i := 0; i < 8; i++ {
		a[len(a)-1-i] = byte(n >> (8 * uint(i)))
	}
	return a
}

func EthereumPrecompiles(fork Fork) *PrecompileSet {
	s := NewPrecompileSet()
	for _, n := range ethereumPrecompileNumbers(fork) {
		s.Set(PrecompileAddress(n), missingPrecompile{})
	}
	s.Set(PrecompileAddress(0x02), PurePrecompile{P: sha256Precompile{}})
	s.Set(PrecompileAddress(0x04), PurePrecompile{P: identityPrecompile{}})
	addCryptoPrecompiles(s, fork)
	return s
}

func ethereumPrecompileNumbers(fork Fork) []uint64 {
	numbers := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if fork >= Cancun {
		numbers = append(numbers, 0x0a)
	}
	if fork >= Prague {
		numbers = append(numbers, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11)
	}
	if fork >= Osaka {
		numbers = append(numbers, 0x100)
	}
	return numbers
}

type missingPrecompile struct{}

func (missingPrecompile) Run(call *PrecompileCall) PrecompileResult {
	return PrecompileResult{GasUsed: call.Gas, Status: StatusPrecompileFailure}
}

type linearCost struct {
	base uint64
	word uint64
}

func (c linearCost) of(input []byte) uint64 {
	return c.base + toWords(uint64(len(input)))*c.word
}

type sha256Precompile struct{}

func (sha256Precompile) RequiredGas(input []byte) uint64 {
	return linearCost{base: 60, word: 12}.of(input)
}

func (sha256Precompile) Compute(input []byte) ([]byte, bool) {
	sum := sha256.Sum256(input)
	return sum[:], true
}

type identityPrecompile struct{}

func (identityPrecompile) RequiredGas(input []byte) uint64 {
	return linearCost{base: 15, word: 3}.of(input)
}

func (identityPrecompile) Compute(input []byte) ([]byte, bool) {
	out := make([]byte, len(input))
	copy(out, input)
	return out, true
}
