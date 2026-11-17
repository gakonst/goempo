package evm

import (
	"github.com/gakonst/goempo/internal/crypto/p256"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
)

func addCryptoPrecompiles(s *PrecompileSet, fork Fork) {
	s.Set(PrecompileAddress(0x01), PurePrecompile{P: ecrecoverPrecompile{}})
	if fork >= Osaka {
		s.Set(PrecompileAddress(0x100), PurePrecompile{P: p256VerifyPrecompile{}})
	}
}

type ecrecoverPrecompile struct{}

func (ecrecoverPrecompile) RequiredGas(_ []byte) uint64 { return 3000 }

func (ecrecoverPrecompile) Compute(input []byte) ([]byte, bool) {
	out, ok := secp256k1.Ecrecover(input)
	if !ok {
		return nil, true
	}
	return out[:], true
}

type p256VerifyPrecompile struct{}

func (p256VerifyPrecompile) RequiredGas(_ []byte) uint64 { return p256.PrecompileGas }

func (p256VerifyPrecompile) Compute(input []byte) ([]byte, bool) {
	if !p256.VerifyPrecompile(input) {
		return nil, true
	}
	var out [32]byte
	out[31] = 1
	return out[:], true
}
