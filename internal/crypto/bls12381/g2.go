package bls12381

type g2Affine struct {
	x        fp2
	y        fp2
	infinity bool
}

type g2Jac struct {
	x fp2
	y fp2
	z fp2
}

func (p g2Affine) onCurve() bool {
	if p.infinity {
		return true
	}
	return p.y.sqr() == p.x.sqr().mul(p.x).add(twistB)
}

func (p g2Affine) jac() g2Jac {
	if p.infinity {
		return g2Jac{}
	}
	return g2Jac{x: p.x, y: p.y, z: fp2One}
}

func (p g2Jac) isInfinity() bool { return p.z.isZero() }

func (p g2Jac) affine() g2Affine {
	if p.isInfinity() {
		return g2Affine{infinity: true}
	}
	zi := p.z.inv()
	zi2 := zi.sqr()
	return g2Affine{x: p.x.mul(zi2), y: p.y.mul(zi2.mul(zi))}
}

func (p g2Jac) neg() g2Jac { return g2Jac{x: p.x, y: p.y.neg(), z: p.z} }

func (p g2Jac) double() g2Jac {
	if p.isInfinity() {
		return p
	}
	a := p.x.sqr()
	b := p.y.sqr()
	c := b.sqr()
	d := p.x.add(b).sqr().sub(a).sub(c).double()
	e := a.double().add(a)
	x3 := e.sqr().sub(d.double())
	y3 := e.mul(d.sub(x3)).sub(c.double().double().double())
	z3 := p.y.mul(p.z).double()
	return g2Jac{x: x3, y: y3, z: z3}
}

func (p g2Jac) add(q g2Jac) g2Jac {
	if p.isInfinity() {
		return q
	}
	if q.isInfinity() {
		return p
	}
	z1z1 := p.z.sqr()
	z2z2 := q.z.sqr()
	u1 := p.x.mul(z2z2)
	u2 := q.x.mul(z1z1)
	s1 := p.y.mul(q.z).mul(z2z2)
	s2 := q.y.mul(p.z).mul(z1z1)
	h := u2.sub(u1)
	rr := s2.sub(s1)
	if h.isZero() {
		if rr.isZero() {
			return p.double()
		}
		return g2Jac{}
	}
	i := h.double().sqr()
	j := h.mul(i)
	rr = rr.double()
	v := u1.mul(i)
	x3 := rr.sqr().sub(j).sub(v.double())
	y3 := rr.mul(v.sub(x3)).sub(s1.mul(j).double())
	z3 := p.z.add(q.z).sqr().sub(z1z1).sub(z2z2).mul(h)
	return g2Jac{x: x3, y: y3, z: z3}
}

func (p g2Jac) equal(q g2Jac) bool {
	if p.isInfinity() || q.isInfinity() {
		return p.isInfinity() == q.isInfinity()
	}
	z1z1 := p.z.sqr()
	z2z2 := q.z.sqr()
	if p.x.mul(z2z2) != q.x.mul(z1z1) {
		return false
	}
	return p.y.mul(z2z2.mul(q.z)) == q.y.mul(z1z1.mul(p.z))
}

func (p g2Jac) mulBytes(k []byte) g2Jac {
	acc := g2Jac{}
	for i := 0; i < len(k); i++ {
		for bit := 7; bit >= 0; bit-- {
			acc = acc.double()
			if (k[i]>>uint(bit))&1 == 1 {
				acc = acc.add(p)
			}
		}
	}
	return acc
}

func (p g2Jac) mulUint64(k uint64) g2Jac {
	acc := g2Jac{}
	for bit := 63; bit >= 0; bit-- {
		acc = acc.double()
		if (k>>uint(bit))&1 == 1 {
			acc = acc.add(p)
		}
	}
	return acc
}

func (p g2Jac) psi() g2Jac {
	return g2Jac{x: p.x.conj().mul(psiX), y: p.y.conj().mul(psiY), z: p.z.conj()}
}

func (p g2Affine) inSubgroup() bool {
	if p.infinity {
		return true
	}
	q := p.jac()
	return q.psi().equal(q.mulUint64(curveXAbs).neg())
}

func decodeFp2(b []byte) (fp2, bool) {
	c0, ok0 := decodeFp(b[:EncodedFpLength])
	c1, ok1 := decodeFp(b[EncodedFpLength : 2*EncodedFpLength])
	return fp2{c0: c0, c1: c1}, ok0 && ok1
}

func decodeG2(b []byte) (g2Affine, bool) {
	x, okx := decodeFp2(b[:2*EncodedFpLength])
	y, oky := decodeFp2(b[2*EncodedFpLength : 4*EncodedFpLength])
	if !okx || !oky {
		return g2Affine{}, false
	}
	if x.isZero() && y.isZero() {
		return g2Affine{infinity: true}, true
	}
	p := g2Affine{x: x, y: y}
	return p, p.onCurve()
}

func (p g2Affine) encode(dst []byte) {
	if p.infinity {
		clear(dst[:4*EncodedFpLength])
		return
	}
	p.x.c0.putBytes(dst[:EncodedFpLength])
	p.x.c1.putBytes(dst[EncodedFpLength : 2*EncodedFpLength])
	p.y.c0.putBytes(dst[2*EncodedFpLength : 3*EncodedFpLength])
	p.y.c1.putBytes(dst[3*EncodedFpLength : 4*EncodedFpLength])
}
