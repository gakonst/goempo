package types

import (
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/primitives"
)

func recoverSecp256k1(hash primitives.Hash, sig Signature) (primitives.Address, error) {
	if !secp256k1.IsLowS(sig.S) {
		return primitives.Address{}, ErrHighS
	}
	id := byte(0)
	if sig.YParity {
		id = 1
	}
	return secp256k1.RecoverAddress(hash, secp256k1.Signature{R: sig.R, S: sig.S, RecoveryID: id})
}
