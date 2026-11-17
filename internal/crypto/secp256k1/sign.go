package secp256k1

import (
	"crypto/hmac"
	"crypto/sha256"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

const nonceAttempts = 64

func (k PrivateKey) scalar() (u256.Int, error) {
	d := u256.FromBytes32(k)
	if d.IsZero() || !d.Lt(curveOrder) {
		return u256.Int{}, ErrInvalidPrivateKey
	}
	return d, nil
}

func (k PrivateKey) PublicKey() (PublicKey, error) {
	d, err := k.scalar()
	if err != nil {
		return PublicKey{}, err
	}
	return baseMul(d).toAffine().publicKey(), nil
}

func (k PrivateKey) Address() (primitives.Address, error) {
	pub, err := k.PublicKey()
	if err != nil {
		return primitives.Address{}, err
	}
	return pub.Address(), nil
}

type nonceGen struct {
	key [sha256.Size]byte
	v   [sha256.Size]byte
}

func newNonceGen(in signingScalars) nonceGen {
	var g nonceGen
	for i := range g.v {
		g.v[i] = 1
	}
	var seed [2 * ScalarLength]byte
	in.d.PutBytes32(seed[:ScalarLength])
	in.z.PutBytes32(seed[ScalarLength:])
	g.update(0, seed[:])
	g.update(1, seed[:])
	return g
}

func (g *nonceGen) mac(parts ...[]byte) [sha256.Size]byte {
	m := hmac.New(sha256.New, g.key[:])
	for _, p := range parts {
		m.Write(p)
	}
	var out [sha256.Size]byte
	copy(out[:], m.Sum(nil))
	return out
}

func (g *nonceGen) update(tag byte, seed []byte) {
	g.key = g.mac(g.v[:], []byte{tag}, seed)
	g.v = g.mac(g.v[:])
}

func (g *nonceGen) next() u256.Int {
	g.v = g.mac(g.v[:])
	return u256.FromBytes32(g.v)
}

func (g *nonceGen) retry() {
	g.key = g.mac(g.v[:], []byte{0})
	g.v = g.mac(g.v[:])
}

func Sign(hash primitives.Hash, key PrivateKey) (Signature, error) {
	d, err := key.scalar()
	if err != nil {
		return Signature{}, err
	}
	in := signingScalars{d: d, z: hashScalar(hash)}
	gen := newNonceGen(in)
	attempt := 0
	for ; attempt < nonceAttempts; attempt++ {
		k := gen.next()
		sig, ok := in.sign(k)
		if ok {
			return sig, nil
		}
		gen.retry()
	}
	return Signature{}, ErrInvalidSignature
}

type signingScalars struct {
	d u256.Int
	z u256.Int
}

func (in signingScalars) sign(k u256.Int) (Signature, bool) {
	if k.IsZero() || !k.Lt(curveOrder) {
		return Signature{}, false
	}
	p := baseMul(k).toAffine()
	rx := u256.FromBytes32(p.x.bytes())
	var recovery byte
	if p.y.isOdd() {
		recovery = 1
	}
	if !rx.Lt(curveOrder) {
		return Signature{}, false
	}
	r := rx
	if r.IsZero() {
		return Signature{}, false
	}
	kinv, ok := k.ModInverse(curveOrder)
	if !ok {
		return Signature{}, false
	}
	sum := in.z.AddFull(r.MulFull(in.d).Mod(curveOrder)).Mod(curveOrder)
	s := kinv.MulFull(sum).Mod(curveOrder)
	if s.IsZero() {
		return Signature{}, false
	}
	if s.Gt(curveOrderHalf) {
		s = curveOrder.Sub(s)
		recovery ^= 1
	}
	return Signature{R: r.Bytes32(), S: s.Bytes32(), RecoveryID: recovery}, true
}
