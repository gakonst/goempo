package bls12381

var (
	sswuNegBOverA1 = sswuB1.neg().mul(sswuA1.inv())
	sswuNegBOverA2 = sswuB2.neg().mul(sswuA2.inv())
)

func sswuG1(u fp) g1Affine {
	zu2 := sswuZ1.mul(u.sqr())
	tv1 := zu2.sqr().add(zu2)
	var x1 fp
	if tv1.isZero() {
		x1 = sswuB1.mul(sswuZ1.mul(sswuA1).inv())
	} else {
		x1 = sswuNegBOverA1.mul(fpOne.add(tv1.inv()))
	}
	g := func(x fp) fp { return x.sqr().mul(x).add(sswuA1.mul(x)).add(sswuB1) }
	x := x1
	y, ok := g(x1).sqrt()
	if !ok {
		x = zu2.mul(x1)
		y, _ = g(x).sqrt()
	}
	if u.sgn0() != y.sgn0() {
		y = y.neg()
	}
	return g1Affine{x: x, y: y}
}

type leading bool

const (
	monic    leading = true
	withLead leading = false
)

func hornerFp(k []fp, x fp, lead leading) fp {
	acc := fpOne
	top := len(k)
	if lead == withLead {
		top--
		acc = k[top]
	}
	for i := top - 1; i >= 0; i-- {
		acc = acc.mul(x).add(k[i])
	}
	return acc
}

func iso11(p g1Affine) g1Jac {
	if p.infinity {
		return g1Jac{}
	}
	xn := hornerFp(iso11K1[:], p.x, withLead)
	xd := hornerFp(iso11K2[:], p.x, monic)
	yn := hornerFp(iso11K3[:], p.x, withLead)
	yd := hornerFp(iso11K4[:], p.x, monic)
	if xd.isZero() || yd.isZero() {
		return g1Jac{}
	}
	yd2 := yd.sqr()
	xd2 := xd.sqr()
	return g1Jac{x: xn.mul(xd).mul(yd2), y: p.y.mul(yn).mul(xd2.mul(xd)).mul(yd2), z: xd.mul(yd)}
}

func mapToG1(u fp) g1Affine {
	return iso11(sswuG1(u)).mulUint64(g1Cofactor).affine()
}

func sswuG2(u fp2) g2Affine {
	zu2 := sswuZ2.mul(u.sqr())
	tv1 := zu2.sqr().add(zu2)
	var x1 fp2
	if tv1.isZero() {
		x1 = sswuB2.mul(sswuZ2.mul(sswuA2).inv())
	} else {
		x1 = sswuNegBOverA2.mul(fp2One.add(tv1.inv()))
	}
	g := func(x fp2) fp2 { return x.sqr().mul(x).add(sswuA2.mul(x)).add(sswuB2) }
	x := x1
	y, ok := g(x1).sqrt()
	if !ok {
		x = zu2.mul(x1)
		y, _ = g(x).sqrt()
	}
	if u.sgn0() != y.sgn0() {
		y = y.neg()
	}
	return g2Affine{x: x, y: y}
}

func hornerFp2(k []fp2, x fp2, lead leading) fp2 {
	acc := fp2One
	top := len(k)
	if lead == withLead {
		top--
		acc = k[top]
	}
	for i := top - 1; i >= 0; i-- {
		acc = acc.mul(x).add(k[i])
	}
	return acc
}

func iso3(p g2Affine) g2Jac {
	if p.infinity {
		return g2Jac{}
	}
	xn := hornerFp2(iso3K1[:], p.x, withLead)
	xd := hornerFp2(iso3K2[:], p.x, monic)
	yn := hornerFp2(iso3K3[:], p.x, withLead)
	yd := hornerFp2(iso3K4[:], p.x, monic)
	if xd.isZero() || yd.isZero() {
		return g2Jac{}
	}
	yd2 := yd.sqr()
	xd2 := xd.sqr()
	return g2Jac{x: xn.mul(xd).mul(yd2), y: p.y.mul(yn).mul(xd2.mul(xd)).mul(yd2), z: xd.mul(yd)}
}

func mapToG2(u fp2) g2Affine {
	return iso3(sswuG2(u)).mulBytes(g2CofactorBytes[:]).affine()
}
