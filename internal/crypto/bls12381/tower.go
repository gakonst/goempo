package bls12381

type fp2 struct {
	c0 fp
	c1 fp
}

var fp2One = fp2{c0: fpOne}

func (a fp2) add(b fp2) fp2 { return fp2{c0: a.c0.add(b.c0), c1: a.c1.add(b.c1)} }

func (a fp2) sub(b fp2) fp2 { return fp2{c0: a.c0.sub(b.c0), c1: a.c1.sub(b.c1)} }

func (a fp2) neg() fp2 { return fp2{c0: a.c0.neg(), c1: a.c1.neg()} }

func (a fp2) double() fp2 { return fp2{c0: a.c0.double(), c1: a.c1.double()} }

func (a fp2) conj() fp2 { return fp2{c0: a.c0, c1: a.c1.neg()} }

func (a fp2) isZero() bool { return a.c0.isZero() && a.c1.isZero() }

func (a fp2) mul(b fp2) fp2 {
	t0 := a.c0.mul(b.c0)
	t1 := a.c1.mul(b.c1)
	t2 := a.c0.add(a.c1).mul(b.c0.add(b.c1))
	return fp2{c0: t0.sub(t1), c1: t2.sub(t0).sub(t1)}
}

func (a fp2) sqr() fp2 {
	t := a.c0.mul(a.c1)
	return fp2{c0: a.c0.add(a.c1).mul(a.c0.sub(a.c1)), c1: t.double()}
}

func (a fp2) mulFp(b fp) fp2 { return fp2{c0: a.c0.mul(b), c1: a.c1.mul(b)} }

func (a fp2) mulByXi() fp2 { return fp2{c0: a.c0.sub(a.c1), c1: a.c0.add(a.c1)} }

func (a fp2) inv() fp2 {
	n := a.c0.sqr().add(a.c1.sqr()).inv()
	return fp2{c0: a.c0.mul(n), c1: a.c1.neg().mul(n)}
}

func (a fp2) exp(e fp) fp2 {
	l := e.limbs()
	z := fp2One
	for i := fpLimbs - 1; i >= 0; i-- {
		for bit := limbBits - 1; bit >= 0; bit-- {
			z = z.sqr()
			if (l[i]>>uint(bit))&1 == 1 {
				z = z.mul(a)
			}
		}
	}
	return z
}

func (a fp2) sqrt() (fp2, bool) {
	if a.isZero() {
		return a, true
	}
	a1 := a.exp(expPMinus3Over4)
	alpha := a1.sqr().mul(a)
	x0 := a1.mul(a)
	minusOne := fp2One.neg()
	var y fp2
	if alpha == minusOne {
		y = fp2{c0: x0.c1.neg(), c1: x0.c0}
	} else {
		y = alpha.add(fp2One).exp(expPMinus1Over2).mul(x0)
	}
	return y, y.sqr() == a
}

func (a fp2) sgn0() bool {
	c0 := a.c0.canonical()
	if c0.l0&1 == 1 {
		return true
	}
	if !c0.isZero() {
		return false
	}
	return a.c1.canonical().l0&1 == 1
}

type fp6 struct {
	c0 fp2
	c1 fp2
	c2 fp2
}

func (a fp6) add(b fp6) fp6 { return fp6{c0: a.c0.add(b.c0), c1: a.c1.add(b.c1), c2: a.c2.add(b.c2)} }

func (a fp6) sub(b fp6) fp6 { return fp6{c0: a.c0.sub(b.c0), c1: a.c1.sub(b.c1), c2: a.c2.sub(b.c2)} }

func (a fp6) neg() fp6 { return fp6{c0: a.c0.neg(), c1: a.c1.neg(), c2: a.c2.neg()} }

func (a fp6) mulByV() fp6 { return fp6{c0: a.c2.mulByXi(), c1: a.c0, c2: a.c1} }

func (a fp6) mul(b fp6) fp6 {
	t0 := a.c0.mul(b.c0)
	t1 := a.c1.mul(b.c1)
	t2 := a.c2.mul(b.c2)
	c0 := a.c1.add(a.c2).mul(b.c1.add(b.c2)).sub(t1).sub(t2).mulByXi().add(t0)
	c1 := a.c0.add(a.c1).mul(b.c0.add(b.c1)).sub(t0).sub(t1).add(t2.mulByXi())
	c2 := a.c0.add(a.c2).mul(b.c0.add(b.c2)).sub(t0).sub(t2).add(t1)
	return fp6{c0: c0, c1: c1, c2: c2}
}

func (a fp6) sqr() fp6 { return a.mul(a) }

func (a fp6) mulBy01(b fp6) fp6 {
	t0 := a.c0.mul(b.c0)
	t1 := a.c1.mul(b.c1)
	c0 := a.c2.mul(b.c1).mulByXi().add(t0)
	c1 := a.c0.add(a.c1).mul(b.c0.add(b.c1)).sub(t0).sub(t1)
	c2 := a.c2.mul(b.c0).add(t1)
	return fp6{c0: c0, c1: c1, c2: c2}
}

func (a fp6) mulBy1(b1 fp2) fp6 {
	return fp6{c0: a.c2.mul(b1).mulByXi(), c1: a.c0.mul(b1), c2: a.c1.mul(b1)}
}

func (a fp6) inv() fp6 {
	c0 := a.c0.sqr().sub(a.c1.mul(a.c2).mulByXi())
	c1 := a.c2.sqr().mulByXi().sub(a.c0.mul(a.c1))
	c2 := a.c1.sqr().sub(a.c0.mul(a.c2))
	f := a.c0.mul(c0).add(a.c2.mul(c1).add(a.c1.mul(c2)).mulByXi()).inv()
	return fp6{c0: c0.mul(f), c1: c1.mul(f), c2: c2.mul(f)}
}

type fp12 struct {
	c0 fp6
	c1 fp6
}

var fp12One = fp12{c0: fp6{c0: fp2One}}

func (a fp12) mul(b fp12) fp12 {
	t0 := a.c0.mul(b.c0)
	t1 := a.c1.mul(b.c1)
	c1 := a.c0.add(a.c1).mul(b.c0.add(b.c1)).sub(t0).sub(t1)
	return fp12{c0: t0.add(t1.mulByV()), c1: c1}
}

func (a fp12) sqr() fp12 {
	ab := a.c0.mul(a.c1)
	c0 := a.c0.add(a.c1).mul(a.c0.add(a.c1.mulByV())).sub(ab).sub(ab.mulByV())
	return fp12{c0: c0, c1: ab.add(ab)}
}

func (a fp12) conj() fp12 { return fp12{c0: a.c0, c1: a.c1.neg()} }

func (a fp12) inv() fp12 {
	t := a.c0.sqr().sub(a.c1.sqr().mulByV()).inv()
	return fp12{c0: a.c0.mul(t), c1: a.c1.mul(t).neg()}
}

func (a fp12) isOne() bool { return a == fp12One }

type frobeniusTable = [6]fp2

type conjugation bool

const (
	conjugated conjugation = true
	unchanged  conjugation = false
)

func (a fp12) frobenius(gamma *frobeniusTable, conjugate conjugation) fp12 {
	f := func(x fp2) fp2 {
		if conjugate {
			return x.conj()
		}
		return x
	}
	return fp12{
		c0: fp6{c0: f(a.c0.c0).mul(gamma[0]), c1: f(a.c0.c1).mul(gamma[2]), c2: f(a.c0.c2).mul(gamma[4])},
		c1: fp6{c0: f(a.c1.c0).mul(gamma[1]), c1: f(a.c1.c1).mul(gamma[3]), c2: f(a.c1.c2).mul(gamma[5])},
	}
}

func (a fp12) frob1() fp12 { return a.frobenius(&frobGamma1, conjugated) }

func (a fp12) frob2() fp12 { return a.frobenius(&frobGamma2, unchanged) }

func (a fp12) expX() fp12 {
	z := fp12One
	for bit := 63; bit >= 0; bit-- {
		z = z.sqr()
		if (uint64(curveXAbs)>>uint(bit))&1 == 1 {
			z = z.mul(a)
		}
	}
	return z.conj()
}
