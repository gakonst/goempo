package secp256k1

type affine struct {
	x        fe
	y        fe
	infinity bool
}

type jacobian struct {
	x fe
	y fe
	z fe
}

var curveB = feFromUint64(7)

var generator = affine{
	x: fe{0x59F2815B16F81798, 0x029BFCDB2DCE28D9, 0x55A06295CE870B07, 0x79BE667EF9DCBBAC},
	y: fe{0x9C47D08FFB10D4B8, 0xFD17B448A6855419, 0x5DA4FBFC0E1108A8, 0x483ADA7726A3C465},
}

func (p affine) onCurve() bool {
	if p.infinity {
		return false
	}
	return p.y.sqr() == p.x.sqr().mul(p.x).add(curveB)
}

func (p affine) toJacobian() jacobian {
	if p.infinity {
		return jacobian{}
	}
	return jacobian{x: p.x, y: p.y, z: feFromUint64(1)}
}

func (p jacobian) isInfinity() bool { return p.z.isZero() }

func (p jacobian) toAffine() affine {
	if p.isInfinity() {
		return affine{infinity: true}
	}
	zinv := p.z.inv()
	zinv2 := zinv.sqr()
	return affine{x: p.x.mul(zinv2), y: p.y.mul(zinv2.mul(zinv))}
}

func (p jacobian) neg() jacobian { return jacobian{x: p.x, y: p.y.neg(), z: p.z} }

func (p jacobian) double() jacobian {
	if p.isInfinity() {
		return p
	}
	if p.y.isZero() {
		return jacobian{}
	}
	a := p.x.sqr()
	b := p.y.sqr()
	c := b.sqr()
	d := p.x.add(b).sqr().sub(a).sub(c).double()
	e := a.double().add(a)
	f := e.sqr()
	x3 := f.sub(d.double())
	c8 := c.double().double().double()
	y3 := e.mul(d.sub(x3)).sub(c8)
	z3 := p.y.mul(p.z).double()
	return jacobian{x: x3, y: y3, z: z3}
}

func (p jacobian) add(q jacobian) jacobian {
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
		return jacobian{}
	}
	i := h.double().sqr()
	j := h.mul(i)
	rr = rr.double()
	v := u1.mul(i)
	x3 := rr.sqr().sub(j).sub(v.double())
	y3 := rr.mul(v.sub(x3)).sub(s1.mul(j).double())
	z3 := p.z.add(q.z).sqr().sub(z1z1).sub(z2z2).mul(h)
	return jacobian{x: x3, y: y3, z: z3}
}

func (p jacobian) addAffine(q affine) jacobian {
	if q.infinity {
		return p
	}
	if p.isInfinity() {
		return q.toJacobian()
	}
	z1z1 := p.z.sqr()
	u2 := q.x.mul(z1z1)
	s2 := q.y.mul(p.z).mul(z1z1)
	h := u2.sub(p.x)
	rr := s2.sub(p.y)
	if h.isZero() {
		if rr.isZero() {
			return p.double()
		}
		return jacobian{}
	}
	hh := h.sqr()
	i := hh.double().double()
	j := h.mul(i)
	rr = rr.double()
	v := p.x.mul(i)
	x3 := rr.sqr().sub(j).sub(v.double())
	y3 := rr.mul(v.sub(x3)).sub(p.y.mul(j).double())
	z3 := p.z.add(h).sqr().sub(z1z1).sub(hh)
	return jacobian{x: x3, y: y3, z: z3}
}
