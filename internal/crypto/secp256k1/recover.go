package secp256k1

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	ScalarLength    = 32
	PublicKeyLength = 64
	SignatureLength = 65
	EcrecoverInput  = 128
	addressOffset   = 12
)

const _ = uint(EcrecoverInput - 4*ScalarLength)

const _ = uint(SignatureLength - 2*ScalarLength - 1)

const _ = uint(PublicKeyLength - 2*ScalarLength)

var (
	ErrInvalidSignature    = errors.New("secp256k1: r or s outside [1, n)")
	ErrInvalidRecoveryID   = errors.New("secp256k1: recovery id is not 0 or 1")
	ErrNoCurvePoint        = errors.New("secp256k1: r is not the x-coordinate of a curve point")
	ErrInvalidPublicKey    = errors.New("secp256k1: recovered point is the identity")
	ErrInvalidPrivateKey   = errors.New("secp256k1: private key outside [1, n)")
	ErrPublicKeyNotOnCurve = errors.New("secp256k1: public key is not on the curve")
)

var (
	curveOrder     = u256.Int{0xBFD25E8CD0364141, 0xBAAEDCE6AF48A03B, 0xFFFFFFFFFFFFFFFE, 0xFFFFFFFFFFFFFFFF}
	curveOrderHalf = curveOrder.Rsh(1)
)

func Order() u256.Int { return curveOrder }

type Signature struct {
	R          [ScalarLength]byte
	S          [ScalarLength]byte
	RecoveryID byte
}

type PublicKey [PublicKeyLength]byte

type PrivateKey [ScalarLength]byte

func SignatureFromBytes(b []byte) (Signature, bool) {
	if len(b) != SignatureLength {
		return Signature{}, false
	}
	var sig Signature
	copy(sig.R[:], b[:ScalarLength])
	copy(sig.S[:], b[ScalarLength:2*ScalarLength])
	sig.RecoveryID = b[2*ScalarLength]
	return sig, true
}

func (sig Signature) Bytes() [SignatureLength]byte {
	var b [SignatureLength]byte
	copy(b[:ScalarLength], sig.R[:])
	copy(b[ScalarLength:2*ScalarLength], sig.S[:])
	b[2*ScalarLength] = sig.RecoveryID
	return b
}

func IsLowS(s [ScalarLength]byte) bool {
	return !u256.FromBytes32(s).Gt(curveOrderHalf)
}

func (sig Signature) ScalarsValid() bool {
	r := u256.FromBytes32(sig.R)
	s := u256.FromBytes32(sig.S)
	if r.IsZero() || s.IsZero() {
		return false
	}
	return r.Lt(curveOrder) && s.Lt(curveOrder)
}

func hashScalar(hash primitives.Hash) u256.Int {
	z := u256.FromBytes32(hash)
	if z.Lt(curveOrder) {
		return z
	}
	return z.Sub(curveOrder)
}

func RecoverPublicKey(hash primitives.Hash, sig Signature) (PublicKey, error) {
	if sig.RecoveryID > 1 {
		return PublicKey{}, ErrInvalidRecoveryID
	}
	r := scalarFromBytes(&sig.R)
	s := scalarFromBytes(&sig.S)
	if !r.valid() || !s.valid() {
		return PublicKey{}, ErrInvalidSignature
	}
	x, ok := feFromBytes(&sig.R)
	assert.Ok(ok, "secp256k1: r below n is below p")
	y, ok := x.sqr().mul(x).add(curveB).sqrt()
	if !ok {
		return PublicKey{}, ErrNoCurvePoint
	}
	if y.isOdd() != (sig.RecoveryID == 1) {
		y = y.neg()
	}
	rinv := r.inv()
	hb := [32]byte(hash)
	z := scalarFromBytes(&hb).reduceOnce()
	u1 := z.mul(rinv).neg()
	u2 := s.mul(rinv)
	var wk recoverWork
	q := wk.ecmult(u1, u2, affine{x: x, y: y}).toAffine()
	if q.infinity {
		return PublicKey{}, ErrInvalidPublicKey
	}
	return q.publicKey(), nil
}

func (p affine) publicKey() PublicKey {
	var k PublicKey
	x := p.x.bytes()
	y := p.y.bytes()
	copy(k[:ScalarLength], x[:])
	copy(k[ScalarLength:], y[:])
	return k
}

func RecoverAddress(hash primitives.Hash, sig Signature) (primitives.Address, error) {
	k, err := RecoverPublicKey(hash, sig)
	if err != nil {
		return primitives.Address{}, err
	}
	return k.Address(), nil
}

func (k PublicKey) Address() primitives.Address {
	h := keccak.Sum256(k[:])
	return primitives.BytesToAddress(h[addressOffset:])
}

func (k PublicKey) Valid() bool {
	var xb, yb [ScalarLength]byte
	copy(xb[:], k[:ScalarLength])
	copy(yb[:], k[ScalarLength:])
	x, okx := feFromBytes(&xb)
	y, oky := feFromBytes(&yb)
	if !okx || !oky {
		return false
	}
	return affine{x: x, y: y}.onCurve()
}

func Ecrecover(input []byte) ([ScalarLength]byte, bool) {
	var in [EcrecoverInput]byte
	copy(in[:], input)
	for i := ScalarLength; i < 2*ScalarLength-1; i++ {
		if in[i] != 0 {
			return [ScalarLength]byte{}, false
		}
	}
	v := in[2*ScalarLength-1]
	if v != 27 && v != 28 {
		return [ScalarLength]byte{}, false
	}
	var hash primitives.Hash
	copy(hash[:], in[:ScalarLength])
	sig := Signature{RecoveryID: v - 27}
	copy(sig.R[:], in[2*ScalarLength:3*ScalarLength])
	copy(sig.S[:], in[3*ScalarLength:])
	addr, err := RecoverAddress(hash, sig)
	if err != nil {
		return [ScalarLength]byte{}, false
	}
	var out [ScalarLength]byte
	copy(out[addressOffset:], addr[:])
	return out, true
}

func beUint64(b []byte) uint64 {
	assert.Ok(len(b) >= 8, "secp256k1: short limb")
	return uint64(b[7]) | uint64(b[6])<<8 | uint64(b[5])<<16 | uint64(b[4])<<24 |
		uint64(b[3])<<32 | uint64(b[2])<<40 | uint64(b[1])<<48 | uint64(b[0])<<56
}

func putBeUint64(b []byte, v uint64) {
	assert.Ok(len(b) >= 8, "secp256k1: short limb")
	for i := 0; i < 8; i++ {
		b[7-i] = byte(v >> (8 * i))
	}
}
