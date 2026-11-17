package p256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"math/big"
)

const (
	ScalarLength       = 32
	PrecompileInput    = 160
	PrecompileGas      = 6900
	PrecompileGasR7212 = 3450
	uncompressedLength = 1 + 2*ScalarLength
	uncompressedPrefix = 0x04
)

const _ = uint(PrecompileInput - 5*ScalarLength)

const _ = uint(PrecompileGas - 2*PrecompileGasR7212)

var (
	curveOrder     = elliptic.P256().Params().N
	curveOrderHalf = new(big.Int).Rsh(curveOrder, 1)
)

type Signature struct {
	R [ScalarLength]byte
	S [ScalarLength]byte
}

type PublicKey struct {
	X [ScalarLength]byte
	Y [ScalarLength]byte
}

func IsLowS(s [ScalarLength]byte) bool {
	return new(big.Int).SetBytes(s[:]).Cmp(curveOrderHalf) <= 0
}

func scalarInRange(b [ScalarLength]byte) (*big.Int, bool) {
	v := new(big.Int).SetBytes(b[:])
	if v.Sign() == 0 {
		return nil, false
	}
	return v, v.Cmp(curveOrder) < 0
}

func (k PublicKey) parse() (*ecdsa.PublicKey, bool) {
	var enc [uncompressedLength]byte
	enc[0] = uncompressedPrefix
	copy(enc[1:1+ScalarLength], k.X[:])
	copy(enc[1+ScalarLength:], k.Y[:])
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), enc[:])
	if err != nil {
		return nil, false
	}
	return pub, true
}

func (k PublicKey) Valid() bool {
	_, ok := k.parse()
	return ok
}

func Verify(hash [ScalarLength]byte, sig Signature, key PublicKey) bool {
	r, ok := scalarInRange(sig.R)
	if !ok {
		return false
	}
	s, ok := scalarInRange(sig.S)
	if !ok {
		return false
	}
	pub, ok := key.parse()
	if !ok {
		return false
	}
	return ecdsa.Verify(pub, hash[:], r, s)
}

func VerifyPrecompile(input []byte) bool {
	if len(input) != PrecompileInput {
		return false
	}
	var hash [ScalarLength]byte
	var sig Signature
	var key PublicKey
	copy(hash[:], input[0:32])
	copy(sig.R[:], input[32:64])
	copy(sig.S[:], input[64:96])
	copy(key.X[:], input[96:128])
	copy(key.Y[:], input[128:160])
	return Verify(hash, sig, key)
}
