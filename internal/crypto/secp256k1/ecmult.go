package secp256k1

import (
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

const (
	wnafMax      = 130
	pointWindow  = 5
	baseWindow   = 8
	pointEntries = 1 << (pointWindow - 2)
	baseEntries  = 1 << (baseWindow - 2)
	halfBits     = 128
)

const _ = uint(wnafMax - halfBits - 2)

var feBeta = fe{0xC1396C28719501EE, 0x9CF0497512F58995, 0x6E64479EAC3434E9, 0x7AE96A2B657C0710}

type baseTables struct {
	g [baseEntries]affine
	h [baseEntries]affine
}

var precomputed = buildBaseTables()

func buildBaseTables() *baseTables {
	t := &baseTables{}
	oddMultiples(&t.g, generator.toJacobian())
	h := generator.toJacobian()
	for range halfBits {
		h = h.double()
	}
	oddMultiples(&t.h, h)
	return t
}

func oddMultiples(out *[baseEntries]affine, p jacobian) {
	twice := p.double()
	acc := p
	for i := range out {
		out[i] = acc.toAffine()
		acc = acc.add(twice)
	}
}

type wnaf struct {
	digits [wnafMax]int8
	length int
}

func (w *wnaf) encode(lo, hi uint64, window uint) {
	v0, v1, v2 := lo, hi, uint64(0)
	mask := uint64(1)<<window - 1
	half := int64(1) << (window - 1)
	w.length = 0
	for i := range wnafMax {
		if v0|v1|v2 == 0 {
			break
		}
		w.digits[i] = 0
		if v0&1 == 1 {
			d := int64(v0 & mask)
			if d >= half {
				d -= int64(mask) + 1
			}
			w.digits[i] = int8(d)
			var c uint64
			if d > 0 {
				v0, c = bits.Sub64(v0, uint64(d), 0)
				v1, c = bits.Sub64(v1, 0, c)
				v2, _ = bits.Sub64(v2, 0, c)
			} else {
				v0, c = bits.Add64(v0, uint64(-d), 0)
				v1, c = bits.Add64(v1, 0, c)
				v2, _ = bits.Add64(v2, 0, c)
			}
			w.length = i + 1
		}
		v0 = v0>>1 | v1<<63
		v1 = v1>>1 | v2<<63
		v2 >>= 1
	}
	assert.Ok(v0|v1|v2 == 0, "secp256k1: wNAF longer than its bound")
}

func pickJacobian(t *[pointEntries]jacobian, d int8, neg bool) jacobian {
	if d < 0 {
		p := t[(-d-1)/2]
		if !neg {
			p.y = p.y.neg()
		}
		return p
	}
	p := t[(d-1)/2]
	if neg {
		p.y = p.y.neg()
	}
	return p
}

func pickAffine(t *[baseEntries]affine, d int8) affine {
	if d < 0 {
		p := t[(-d-1)/2]
		p.y = p.y.neg()
		return p
	}
	return t[(d-1)/2]
}

type recoverWork struct {
	p1   wnaf
	p2   wnaf
	g    wnaf
	h    wnaf
	tbl  [pointEntries]jacobian
	ltbl [pointEntries]jacobian
}

func (wk *recoverWork) ecmult(gScalar, pScalar scalar, p affine) jacobian {
	a, b := pScalar.splitLambda()
	ha, hb := a.half(), b.half()
	wk.p1.encode(ha.lo, ha.hi, pointWindow)
	wk.p2.encode(hb.lo, hb.hi, pointWindow)
	wk.g.encode(gScalar[0], gScalar[1], baseWindow)
	wk.h.encode(gScalar[2], gScalar[3], baseWindow)
	pj := p.toJacobian()
	twice := pj.double()
	wk.tbl[0] = pj
	for i := 1; i < pointEntries; i++ {
		wk.tbl[i] = wk.tbl[i-1].add(twice)
	}
	for i := range wk.tbl {
		q := wk.tbl[i]
		wk.ltbl[i] = jacobian{x: q.x.mul(feBeta), y: q.y, z: q.z}
	}
	top := max(wk.p1.length, wk.p2.length, wk.g.length, wk.h.length)
	acc := jacobian{}
	for i := top - 1; i >= 0; i-- {
		acc = acc.double()
		if d := wk.p1.digits[i]; d != 0 && i < wk.p1.length {
			acc = acc.add(pickJacobian(&wk.tbl, d, ha.neg))
		}
		if d := wk.p2.digits[i]; d != 0 && i < wk.p2.length {
			acc = acc.add(pickJacobian(&wk.ltbl, d, hb.neg))
		}
		if d := wk.g.digits[i]; d != 0 && i < wk.g.length {
			acc = acc.addAffine(pickAffine(&precomputed.g, d))
		}
		if d := wk.h.digits[i]; d != 0 && i < wk.h.length {
			acc = acc.addAffine(pickAffine(&precomputed.h, d))
		}
	}
	return acc
}
