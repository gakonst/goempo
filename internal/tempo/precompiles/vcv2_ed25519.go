package precompiles

import (
	"crypto/sha512"
	"math/big"
)

type fe struct{ n *big.Int }

type edPoint struct {
	x, y, z, t fe
}

type edSigned struct {
	pub [32]byte
	sig []byte
	msg []byte
}

var (
	edP     = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	edL, _  = new(big.Int).SetString("7237005577332262213973186563042994240857116359379907606001950938285454250989", 10)
	edD     = feInt(-121665).div(feInt(121666))
	edD2    = edD.add(edD)
	edSqrtM = fe{n: new(big.Int).Exp(big.NewInt(2), new(big.Int).Rsh(new(big.Int).Sub(edP, big.NewInt(1)), 2), edP)}
	edBase  = edBasePoint()
)

const edCofactorDoublings = 3

func feOf(v *big.Int) fe { return fe{n: new(big.Int).Mod(v, edP)} }

func feInt(v int64) fe { return feOf(big.NewInt(v)) }

func (a fe) mul(b fe) fe { return feOf(new(big.Int).Mul(a.n, b.n)) }

func (a fe) add(b fe) fe { return feOf(new(big.Int).Add(a.n, b.n)) }

func (a fe) sub(b fe) fe { return feOf(new(big.Int).Sub(a.n, b.n)) }

func (a fe) neg() fe { return feInt(0).sub(a) }

func (a fe) pow(e *big.Int) fe { return fe{n: new(big.Int).Exp(a.n, e, edP)} }

func (a fe) inv() fe { return a.pow(new(big.Int).Sub(edP, big.NewInt(2))) }

func (a fe) div(b fe) fe { return a.mul(b.inv()) }

func (a fe) equal(b fe) bool { return a.n.Cmp(b.n) == 0 }

func (a fe) isZero() bool { return a.n.Sign() == 0 }

func edLE(b []byte) *big.Int {
	be := make([]byte, len(b))
	for i := range b {
		be[len(b)-1-i] = b[i]
	}
	return new(big.Int).SetBytes(be)
}

type edXY struct{ x, y fe }

func edAffine(c edXY) edPoint {
	return edPoint{x: c.x, y: c.y, z: feInt(1), t: c.x.mul(c.y)}
}

func edIdentity() edPoint { return edAffine(edXY{x: feInt(0), y: feInt(1)}) }

func edRecoverX(y fe, sign uint) (fe, bool) {
	y2 := y.mul(y)
	u := y2.sub(feInt(1))
	v := edD.mul(y2).add(feInt(1))
	v3 := v.mul(v).mul(v)
	v7 := v3.mul(v3).mul(v)
	exp := new(big.Int).Rsh(new(big.Int).Sub(edP, big.NewInt(5)), 3)
	x := u.mul(v3).mul(u.mul(v7).pow(exp))
	vx2 := v.mul(x.mul(x))
	switch {
	case vx2.equal(u):
	case vx2.equal(u.neg()):
		x = x.mul(edSqrtM)
	default:
		return fe{}, false
	}
	if !x.isZero() && x.n.Bit(0) != sign {
		x = x.neg()
	}
	return x, true
}

func edBasePoint() edPoint {
	y := feInt(4).div(feInt(5))
	x, _ := edRecoverX(y, 0)
	return edAffine(edXY{x: x, y: y})
}

func edDecompress(b [32]byte) (edPoint, bool) {
	sign := uint(b[31] >> 7)
	raw := b
	raw[31] &= 0x7f
	y := feOf(edLE(raw[:]))
	x, ok := edRecoverX(y, sign)
	if !ok {
		return edPoint{}, false
	}
	return edAffine(edXY{x: x, y: y}), true
}

func (p edPoint) add(q edPoint) edPoint {
	a := p.y.sub(p.x).mul(q.y.sub(q.x))
	b := p.y.add(p.x).mul(q.y.add(q.x))
	c := p.t.mul(edD2).mul(q.t)
	d := p.z.add(p.z).mul(q.z)
	e, f, g, h := b.sub(a), d.sub(c), d.add(c), b.add(a)
	return edPoint{x: e.mul(f), y: g.mul(h), z: f.mul(g), t: e.mul(h)}
}

func (p edPoint) neg() edPoint {
	return edPoint{x: p.x.neg(), y: p.y, z: p.z, t: p.t.neg()}
}

func (p edPoint) scalarMul(k *big.Int) edPoint {
	acc := edIdentity()
	for i := k.BitLen() - 1; i >= 0; i-- {
		acc = acc.add(acc)
		if k.Bit(i) == 1 {
			acc = acc.add(p)
		}
	}
	return acc
}

func (p edPoint) isIdentity() bool {
	return p.x.isZero() && p.y.equal(p.z)
}

func ed25519PublicKeyDecodes(pub [32]byte) bool {
	_, ok := edDecompress(pub)
	return ok
}

func ed25519VerifyZIP215(s edSigned) bool {
	if len(s.sig) != 64 {
		return false
	}
	a, ok := edDecompress(s.pub)
	if !ok {
		return false
	}
	var rBytes [32]byte
	copy(rBytes[:], s.sig[:32])
	r, ok := edDecompress(rBytes)
	if !ok {
		return false
	}
	scalar := edLE(s.sig[32:])
	if scalar.Cmp(edL) >= 0 {
		return false
	}
	h := sha512.New()
	h.Write(s.sig[:32])
	h.Write(s.pub[:])
	h.Write(s.msg)
	k := new(big.Int).Mod(edLE(h.Sum(nil)), edL)
	check := edBase.scalarMul(scalar).add(a.scalarMul(k).neg()).add(r.neg())
	for i := 0; i < edCofactorDoublings; i++ {
		check = check.add(check)
	}
	return check.isIdentity()
}
