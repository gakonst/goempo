package evm

import "github.com/gakonst/goempo/internal/u256"

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
	addCryptoPrecompiles(s, fork)
	return s
}

type linearCost struct {
	base uint64
	word uint64
}

func (c linearCost) of(input []byte) uint64 {
	return c.base + toWords(uint64(len(input)))*c.word
}
