package bls12381

type g2Proj struct {
	x fp2
	y fp2
	z fp2
}

type line struct {
	constant fp2
	xCoeff   fp2
	yCoeff   fp2
}

func (r *g2Proj) doubleStep() line {
	a := r.x.mul(r.y).mulFp(twoInv)
	b := r.y.sqr()
	c := r.z.sqr()
	e := twistB.mul(c.double().add(c))
	f := e.double().add(e)
	g := b.add(f).mulFp(twoInv)
	h := r.y.add(r.z).sqr().sub(b.add(c))
	i := e.sub(b)
	j := r.x.sqr()
	e2 := e.sqr()
	r.x = a.mul(b.sub(f))
	r.y = g.sqr().sub(e2.double().add(e2))
	r.z = b.mul(h)
	return line{constant: i, xCoeff: j.double().add(j), yCoeff: h.neg()}
}

func (r *g2Proj) addStep(q g2Affine) line {
	theta := r.y.sub(q.y.mul(r.z))
	lambda := r.x.sub(q.x.mul(r.z))
	c := theta.sqr()
	d := lambda.sqr()
	e := lambda.mul(d)
	f := r.z.mul(c)
	g := r.x.mul(d)
	h := e.add(f).sub(g.double())
	r.x = lambda.mul(h)
	r.y = theta.mul(g.sub(h)).sub(e.mul(r.y))
	r.z = r.z.mul(e)
	j := theta.mul(q.x).sub(lambda.mul(q.y))
	return line{constant: j, xCoeff: theta.neg(), yCoeff: lambda}
}

func (f fp12) mulLine(l line, p g1Affine) fp12 {
	a := l.constant
	b := l.xCoeff.mulFp(p.x)
	c := l.yCoeff.mulFp(p.y)
	t0 := f.c0.mulBy01(fp6{c0: a, c1: b})
	t1 := f.c1.mulBy1(c)
	c1 := f.c0.add(f.c1).mulBy01(fp6{c0: a, c1: b.add(c)}).sub(t0).sub(t1)
	return fp12{c0: t0.add(t1.mulByV()), c1: c1}
}

type pair struct {
	p g1Affine
	q g2Affine
}

const maxPairs = 1 << 16

func millerLoop(pairs []pair) fp12 {
	f := fp12One
	count := min(len(pairs), maxPairs)
	if count == 0 {
		return f
	}
	ts := make([]g2Proj, count)
	for k := 0; k < count; k++ {
		ts[k] = g2Proj{x: pairs[k].q.x, y: pairs[k].q.y, z: fp2One}
	}
	for bit := 62; bit >= 0; bit-- {
		f = f.sqr()
		for k := 0; k < count; k++ {
			f = f.mulLine(ts[k].doubleStep(), pairs[k].p)
		}
		if (uint64(curveXAbs)>>uint(bit))&1 == 0 {
			continue
		}
		for k := 0; k < count; k++ {
			f = f.mulLine(ts[k].addStep(pairs[k].q), pairs[k].p)
		}
	}
	return f.conj()
}

func finalExponentiation(in fp12) fp12 {
	t := in.conj().mul(in.inv())
	t = t.frob2().mul(t)
	a := t.expX().mul(t.conj())
	a = a.expX().mul(a.conj())
	b := a.expX().mul(a.frob1())
	c := b.expX().expX().mul(b.frob2()).mul(b.conj())
	return c.mul(t.sqr()).mul(t)
}

func pairingProduct(pairs []pair) fp12 {
	live := make([]pair, 0, len(pairs))
	for _, pr := range pairs {
		if pr.p.infinity || pr.q.infinity {
			continue
		}
		live = append(live, pr)
	}
	return finalExponentiation(millerLoop(live))
}
