package secp256k1

import (
	"errors"

	"github.com/gakonst/goempo/internal/u256"
)

var ErrSharedSecretIdentity = errors.New("secp256k1: shared point is the identity")

func (k PublicKey) point() (affine, error) {
	var xb, yb [ScalarLength]byte
	copy(xb[:], k[:ScalarLength])
	copy(yb[:], k[ScalarLength:])
	x, okx := feFromBytes(&xb)
	y, oky := feFromBytes(&yb)
	p := affine{x: x, y: y}
	if !okx || !oky || !p.onCurve() {
		return affine{}, ErrPublicKeyNotOnCurve
	}
	return p, nil
}

func SharedSecret(key PrivateKey, peer PublicKey) ([ScalarLength]byte, error) {
	d, err := key.scalar()
	if err != nil {
		return [ScalarLength]byte{}, err
	}
	p, err := peer.point()
	if err != nil {
		return [ScalarLength]byte{}, err
	}
	q := shamir(u256.Int{}, pointScalar{point: p.toJacobian(), scalar: d}).toAffine()
	if q.infinity {
		return [ScalarLength]byte{}, ErrSharedSecretIdentity
	}
	return q.x.bytes(), nil
}

func PublicKeyFromCompressed(b []byte) (PublicKey, error) {
	if len(b) != ScalarLength+1 {
		return PublicKey{}, ErrPublicKeyNotOnCurve
	}
	if b[0] != 2 && b[0] != 3 {
		return PublicKey{}, ErrPublicKeyNotOnCurve
	}
	var xb [ScalarLength]byte
	copy(xb[:], b[1:])
	x, ok := feFromBytes(&xb)
	if !ok {
		return PublicKey{}, ErrPublicKeyNotOnCurve
	}
	y, ok := x.sqr().mul(x).add(curveB).sqrt()
	if !ok {
		return PublicKey{}, ErrPublicKeyNotOnCurve
	}
	if y.isOdd() != (b[0] == 3) {
		y = y.neg()
	}
	return affine{x: x, y: y}.publicKey(), nil
}

func (k PublicKey) Compressed() [ScalarLength + 1]byte {
	var out [ScalarLength + 1]byte
	out[0] = 2 | k[PublicKeyLength-1]&1
	copy(out[1:], k[:ScalarLength])
	return out
}
