package bls12381

type g1Affine struct {
	x        fp
	y        fp
	infinity bool
}

type g1Jac struct {
	x fp
	y fp
	z fp
}

func (p g1Affine) onCurve() bool {
	if p.infinity {
		return true
	}
	return p.y.sqr() == p.x.sqr().mul(p.x).add(curveB)
}

func (p g1Affine) jac() g1Jac {
	if p.infinity {
		return g1Jac{}
	}
	return g1Jac{x: p.x, y: p.y, z: fpOne}
}

func (p g1Jac) isInfinity() bool { return p.z.isZero() }

func (p g1Jac) affine() g1Affine {
	if p.isInfinity() {
		return g1Affine{infinity: true}
	}
	zi := p.z.inv()
	zi2 := zi.sqr()
	return g1Affine{x: p.x.mul(zi2), y: p.y.mul(zi2.mul(zi))}
}

func (p g1Jac) neg() g1Jac { return g1Jac{x: p.x, y: p.y.neg(), z: p.z} }

func (p g1Jac) double() g1Jac {
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
	return g1Jac{x: x3, y: y3, z: z3}
}

func (p g1Jac) add(q g1Jac) g1Jac {
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
		return g1Jac{}
	}
	i := h.double().sqr()
	j := h.mul(i)
	rr = rr.double()
	v := u1.mul(i)
	x3 := rr.sqr().sub(j).sub(v.double())
	y3 := rr.mul(v.sub(x3)).sub(s1.mul(j).double())
	z3 := p.z.add(q.z).sqr().sub(z1z1).sub(z2z2).mul(h)
	return g1Jac{x: x3, y: y3, z: z3}
}

func (p g1Jac) equal(q g1Jac) bool {
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

func (p g1Jac) mulBytes(k []byte) g1Jac {
	acc := g1Jac{}
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

func (p g1Jac) mulUint64(k uint64) g1Jac {
	acc := g1Jac{}
	for bit := 63; bit >= 0; bit-- {
		acc = acc.double()
		if (k>>uint(bit))&1 == 1 {
			acc = acc.add(p)
		}
	}
	return acc
}

func (p g1Affine) inSubgroup() bool {
	if p.infinity {
		return true
	}
	q := p.jac()
	phi := g1Jac{x: q.x.mul(cubeRootBeta), y: q.y, z: q.z}
	return phi.equal(q.mulUint64(curveXAbs).mulUint64(curveXAbs).neg())
}

func decodeG1(b []byte) (g1Affine, bool) {
	x, okx := decodeFp(b[:EncodedFpLength])
	y, oky := decodeFp(b[EncodedFpLength : 2*EncodedFpLength])
	if !okx || !oky {
		return g1Affine{}, false
	}
	if x.isZero() && y.isZero() {
		return g1Affine{infinity: true}, true
	}
	p := g1Affine{x: x, y: y}
	return p, p.onCurve()
}

func (p g1Affine) encode(dst []byte) {
	if p.infinity {
		clear(dst[:2*EncodedFpLength])
		return
	}
	p.x.putBytes(dst[:EncodedFpLength])
	p.y.putBytes(dst[EncodedFpLength : 2*EncodedFpLength])
}
