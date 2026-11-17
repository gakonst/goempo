package bn254

type g2Proj struct {
	x fp2
	y fp2
	z fp2
}

type lineCoeffs struct {
	c0 fp2
	c3 fp2
	c4 fp2
}

func (r *g2Proj) doubleStep() lineCoeffs {
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
	return lineCoeffs{c0: h.neg(), c3: j.double().add(j), c4: i}
}

func (r *g2Proj) addStep(q g2Affine) lineCoeffs {
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
	return lineCoeffs{c0: lambda, c3: theta.neg(), c4: j}
}

func (f fp12) mulLine(l lineCoeffs, p g1Affine) fp12 {
	c0 := l.c0.mulFp(p.y)
	c3 := l.c3.mulFp(p.x)
	a := f.c0.mulBy0(c0)
	b := f.c1.mulBy01(fp6{c0: c3, c1: l.c4})
	c1 := f.c0.add(f.c1).mulBy01(fp6{c0: c0.add(c3), c1: l.c4}).sub(a).sub(b)
	return fp12{c0: a.add(b.mulByV()), c1: c1}
}

func (a fp6) mulBy0(b0 fp2) fp6 {
	return fp6{c0: a.c0.mul(b0), c1: a.c1.mul(b0), c2: a.c2.mul(b0)}
}

func (a fp6) mulBy01(b fp6) fp6 {
	b0, b1 := b.c0, b.c1
	t0 := a.c0.mul(b0)
	t1 := a.c1.mul(b1)
	c0 := a.c2.mul(b1).mulByXi().add(t0)
	c1 := a.c0.add(a.c1).mul(b0.add(b1)).sub(t0).sub(t1)
	c2 := a.c2.mul(b0).add(t1)
	return fp6{c0: c0, c1: c1, c2: c2}
}

type pair struct {
	p g1Affine
	q g2Affine
}

func frobeniusTwist(q g2Affine, gamma *frobeniusTable, conjugate conjugation) g2Affine {
	x, y := q.x, q.y
	if conjugate {
		x, y = x.conj(), y.conj()
	}
	return g2Affine{x: x.mul(gamma[2]), y: y.mul(gamma[3])}
}

const maxPairs = 1 << 16

func millerLoop(pairs []pair) fp12 {
	f := fp12One
	if len(pairs) == 0 {
		return f
	}
	count := min(len(pairs), maxPairs)
	ts := make([]g2Proj, count)
	negQ := make([]g2Affine, count)
	for k := 0; k < count; k++ {
		q := pairs[k].q
		ts[k] = g2Proj{x: q.x, y: q.y, z: fp2One}
		negQ[k] = g2Affine{x: q.x, y: q.y.neg()}
	}
	for i := len(ateLoopNAF) - 2; i >= 0; i-- {
		f = f.sqr()
		for k := 0; k < count; k++ {
			f = f.mulLine(ts[k].doubleStep(), pairs[k].p)
			switch ateLoopNAF[i] {
			case 1:
				f = f.mulLine(ts[k].addStep(pairs[k].q), pairs[k].p)
			case -1:
				f = f.mulLine(ts[k].addStep(negQ[k]), pairs[k].p)
			}
		}
	}
	for k := 0; k < count; k++ {
		q1 := frobeniusTwist(pairs[k].q, &frobGamma1, conjugated)
		q2 := frobeniusTwist(pairs[k].q, &frobGamma2, unchanged)
		q2.y = q2.y.neg()
		f = f.mulLine(ts[k].addStep(q1), pairs[k].p)
		f = f.mulLine(ts[k].addStep(q2), pairs[k].p)
	}
	return f
}

func finalExponentiation(in fp12) fp12 {
	t1 := in.conj().mul(in.inv())
	t1 = t1.mul(t1.frob2())
	fp1 := t1.frob1()
	fp2v := t1.frob2()
	fp3 := t1.frob3()
	fu := t1.expU()
	fu2 := fu.expU()
	fu3 := fu2.expU()
	y3 := fu.frob1().conj()
	fu2p := fu2.frob1()
	fu3p := fu3.frob1()
	y2 := fu2.frob2()
	y0 := fp1.mul(fp2v).mul(fp3)
	y1 := t1.conj()
	y5 := fu2.conj()
	y4 := fu.mul(fu2p).conj()
	y6 := fu3.mul(fu3p).conj()
	t0 := y6.sqr().mul(y4).mul(y5)
	t1 = y3.mul(y5).mul(t0)
	t0 = t0.mul(y2)
	t1 = t1.sqr().mul(t0).sqr()
	t0 = t1.mul(y1)
	t1 = t1.mul(y0)
	return t0.sqr().mul(t1)
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
