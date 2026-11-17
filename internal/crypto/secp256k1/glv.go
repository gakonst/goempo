package secp256k1

import (
	"sync"

	"github.com/gakonst/goempo/internal/u256"
)

const (
	glvWindows   = 128 / windowBits
	glvWideBits  = 8
	glvWideSize  = 1 << glvWideBits
	glvWideMask  = glvWideSize - 1
	glvWideRatio = glvWideBits / windowBits
)

const _ = uint(glvWindows*windowBits - 128)

const _ = uint(glvWideRatio*windowBits - glvWideBits)

var (
	glvLambda  = u256.Int{0xDF02967C1B23BD72, 0x122E22EA20816678, 0xA5261C028812645A, 0x5363AD4CC05C30E0}
	glvBeta    = fe{0xC1396C28719501EE, 0x9CF0497512F58995, 0x6E64479EAC3434E9, 0x7AE96A2B657C0710}
	glvG1      = u256.Int{0xE893209A45DBB031, 0x3DAA8A1471E8CA7F, 0xE86C90E49284EB15, 0x3086D221A7D46BCD}
	glvG2      = u256.Int{0x1571B4AE8AC47F71, 0x221208AC9DF506C6, 0x6F547FA90ABFE4C4, 0xE4437ED6010E8828}
	glvMinusB1 = u256.Int{0x6F547FA90ABFE4C3, 0xE4437ED6010E8828, 0, 0}
	glvMinusB2 = u256.Int{0xD765CDA83DB1562C, 0x8A280AC50774346D, 0xFFFFFFFFFFFFFFFE, 0xFFFFFFFFFFFFFFFF}
)

type wideTable [glvWideSize]affine

type glvTables struct {
	g       wideTable
	lambdaG wideTable
}

var glvBaseTables = sync.OnceValue(buildGLVTables)

func buildGLVTables() *glvTables {
	t := &glvTables{}
	t.g[0] = affine{infinity: true}
	t.lambdaG[0] = affine{infinity: true}
	acc := jacobian{}
	for i := 1; i < glvWideSize; i++ {
		acc = acc.addAffine(generator)
		t.g[i] = acc.toAffine()
		t.lambdaG[i] = affine{x: t.g[i].x.mul(glvBeta), y: t.g[i].y}
	}
	return t
}

type sign bool

const (
	positive sign = false
	negative sign = true
)

type halfScalar struct {
	mag  u256.Int
	sign sign
}

type glvSplit struct {
	k1 halfScalar
	k2 halfScalar
}

func (s glvSplit) fits() bool { return s.k1.fits() && s.k2.fits() }

type scalarN u256.Int

func (h halfScalar) fits() bool { return h.mag[2]|h.mag[3] == 0 }

func (a scalarN) mul(b u256.Int) scalarN {
	return scalarN(u256.Int(a).MulFull(b).Mod(curveOrder))
}

func (a scalarN) add(b scalarN) scalarN {
	s, carry := u256.Int(a).AddOverflow(u256.Int(b))
	if carry || !s.Lt(curveOrder) {
		return scalarN(s.Sub(curveOrder))
	}
	return scalarN(s)
}

func (a scalarN) neg() scalarN {
	if u256.Int(a).IsZero() {
		return a
	}
	return scalarN(curveOrder.Sub(u256.Int(a)))
}

func (a scalarN) mulShift384(g u256.Int) scalarN {
	w := u256.Int(a).MulFull(g)
	r := u256.Int{w[6], w[7], 0, 0}
	return scalarN(r.Add(u256.Int{w[5] >> 63, 0, 0, 0}))
}

func (a scalarN) signed() halfScalar {
	x := u256.Int(a)
	if x.Gt(curveOrderHalf) {
		return halfScalar{mag: curveOrder.Sub(x), sign: negative}
	}
	return halfScalar{mag: x, sign: positive}
}

func splitScalar(k u256.Int) glvSplit {
	c1 := scalarN(k).mulShift384(glvG1).mul(glvMinusB1)
	c2 := scalarN(k).mulShift384(glvG2).mul(glvMinusB2)
	r2 := c1.add(c2)
	r1 := r2.mul(glvLambda).neg().add(scalarN(k))
	return glvSplit{k1: r1.signed(), k2: r2.signed()}
}

func (p affine) withSign(s sign) affine {
	if s == negative && !p.infinity {
		p.y = p.y.neg()
	}
	return p
}

func (p jacobian) withSign(s sign) jacobian {
	if s == negative {
		p.y = p.y.neg()
	}
	return p
}

func wideNibble(k u256.Int, window int) int {
	limb := window / (64 / glvWideBits)
	shift := uint(window%(64/glvWideBits)) * glvWideBits
	return int((k[limb] >> shift) & glvWideMask)
}

func shamirGLV(gScalar u256.Int, other pointScalar) jacobian {
	a := splitScalar(gScalar)
	b := splitScalar(other.scalar)
	if !a.fits() || !b.fits() {
		return shamir(gScalar, other)
	}
	base := glvBaseTables()
	table := buildTable(other.point)
	var lambdaTable jacobianTable
	for i := range table {
		lambdaTable[i] = jacobian{x: table[i].x.mul(glvBeta), y: table[i].y, z: table[i].z}
	}
	acc := jacobian{}
	for w := glvWindows - 1; w >= 0; w-- {
		for d := 0; d < windowBits; d++ {
			acc = acc.double()
		}
		if w%glvWideRatio == 0 {
			acc = acc.addAffine(base.g[wideNibble(a.k1.mag, w/glvWideRatio)].withSign(a.k1.sign))
			acc = acc.addAffine(base.lambdaG[wideNibble(a.k2.mag, w/glvWideRatio)].withSign(a.k2.sign))
		}
		acc = acc.add(table[nibble(b.k1.mag, w)].withSign(b.k1.sign))
		acc = acc.add(lambdaTable[nibble(b.k2.mag, w)].withSign(b.k2.sign))
	}
	return acc
}
