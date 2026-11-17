package secp256k1

import (
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

const scalarLimbs = 4

type scalar [scalarLimbs]uint64

var (
	scalarN       = scalar{0xBFD25E8CD0364141, 0xBAAEDCE6AF48A03B, 0xFFFFFFFFFFFFFFFE, 0xFFFFFFFFFFFFFFFF}
	scalarNHalf   = scalar{0xDFE92F46681B20A0, 0x5D576E7357A4501D, 0xFFFFFFFFFFFFFFFF, 0x7FFFFFFFFFFFFFFF}
	scalarNMinus2 = scalar{0xBFD25E8CD036413F, 0xBAAEDCE6AF48A03B, 0xFFFFFFFFFFFFFFFE, 0xFFFFFFFFFFFFFFFF}
)

func scalarFromBytes(b *[32]byte) scalar {
	return scalar{beUint64(b[24:32]), beUint64(b[16:24]), beUint64(b[8:16]), beUint64(b[0:8])}
}

func (a scalar) bytes() [32]byte {
	var b [32]byte
	putBeUint64(b[0:8], a[3])
	putBeUint64(b[8:16], a[2])
	putBeUint64(b[16:24], a[1])
	putBeUint64(b[24:32], a[0])
	return b
}

func (a scalar) isZero() bool { return a[0]|a[1]|a[2]|a[3] == 0 }

func (a scalar) lt(b scalar) bool {
	_, borrow := a.rawSub(b)
	return borrow != 0
}

func (a scalar) rawSub(b scalar) (scalar, uint64) {
	var z scalar
	var c uint64
	z[0], c = bits.Sub64(a[0], b[0], 0)
	z[1], c = bits.Sub64(a[1], b[1], c)
	z[2], c = bits.Sub64(a[2], b[2], c)
	z[3], c = bits.Sub64(a[3], b[3], c)
	return z, c
}

func (a scalar) valid() bool { return !a.isZero() && a.lt(scalarN) }

func (a scalar) isHigh() bool { return scalarNHalf.lt(a) }

func (a scalar) reduceOnce() scalar {
	z, borrow := a.rawSub(scalarN)
	if borrow != 0 {
		return a
	}
	return z
}

func (a scalar) add(b scalar) scalar {
	var z scalar
	var c uint64
	z[0], c = bits.Add64(a[0], b[0], 0)
	z[1], c = bits.Add64(a[1], b[1], c)
	z[2], c = bits.Add64(a[2], b[2], c)
	z[3], c = bits.Add64(a[3], b[3], c)
	if c != 0 {
		z, _ = z.rawSub(scalarN)
		return z
	}
	return z.reduceOnce()
}

func (a scalar) neg() scalar {
	if a.isZero() {
		return a
	}
	z, _ := scalarN.rawSub(a)
	return z
}

func (a scalar) mulWide(b scalar) wide {
	var t wide
	for i := 0; i < scalarLimbs; i++ {
		var carry uint64
		for j := 0; j < scalarLimbs; j++ {
			hi, lo := bits.Mul64(a[i], b[j])
			var c uint64
			lo, c = bits.Add64(lo, t[i+j], 0)
			hi += c
			lo, c = bits.Add64(lo, carry, 0)
			hi += c
			t[i+j] = lo
			carry = hi
		}
		t[i+scalarLimbs] = carry
	}
	return t
}

func (a scalar) mul(b scalar) scalar {
	return montMul(montMul(a, b), montR2)
}

const scalarMontInvConst = 0x4B0DFF665588B13F

// montInv is -n^-1 mod 2^64, and montR2 is 2^512 mod n.
var (
	montInv = scalarMontInv()
	montR2  = scalarMontR2()
	montOne = scalarMontOne()
)

func scalarMontInv() uint64 {
	x := uint64(1)
	for range 6 {
		x *= 2 - scalarN[0]*x
	}
	assert.Ok(x*scalarN[0] == 1, "secp256k1: n^-1 mod 2^64")
	assert.Ok(-x == scalarMontInvConst, "secp256k1: assembly n' matches")
	return -x
}

func scalarMontOne() scalar {
	z, _ := scalar{}.rawSub(scalarN)
	return z
}

func scalarMontR2() scalar {
	z := scalarMontOne()
	for range 256 {
		z = z.add(z)
	}
	return z
}

// montMulGeneric returns a*b/2^256 mod n for a, b below n: the full
// product, then four word-by-word reductions into a nine-word accumulator.
func montMulGeneric(a, b scalar) scalar {
	w := a.mulWide(b)
	var top uint64
	for i := 0; i < scalarLimbs; i++ {
		m := w[i] * montInv
		var carry uint64
		for j := 0; j < scalarLimbs; j++ {
			hi, lo := bits.Mul64(m, scalarN[j])
			var c uint64
			lo, c = bits.Add64(lo, w[i+j], 0)
			hi += c
			lo, c = bits.Add64(lo, carry, 0)
			hi += c
			w[i+j] = lo
			carry = hi
		}
		for k := i + scalarLimbs; k < len(w) && carry != 0; k++ {
			w[k], carry = bits.Add64(w[k], carry, 0)
		}
		top += carry
	}
	z := scalar{w[4], w[5], w[6], w[7]}
	if top != 0 {
		z, _ = z.rawSub(scalarN)
		return z
	}
	return z.reduceOnce()
}

func (a scalar) sqr() scalar { return a.mul(a) }

func montSqrNGeneric(a scalar, n int) scalar {
	for range n {
		a = montMulGeneric(a, a)
	}
	return a
}

const invWindowBits = 4

const invWindowSize = 1 << invWindowBits

const _ = uint(64%invWindowBits + 0)

// inv returns a^-1 mod n by Fermat, in Montgomery form.
func (a scalar) inv() scalar {
	assert.Ok(!a.isZero(), "secp256k1: zero has no inverse")
	var table [invWindowSize]scalar
	table[0] = montOne
	table[1] = montMul(a, montR2)
	for i := 2; i < invWindowSize; i++ {
		table[i] = montMul(table[i-1], table[1])
	}
	z := montOne
	for limb := scalarLimbs - 1; limb >= 0; limb-- {
		for shift := 64 - invWindowBits; shift >= 0; shift -= invWindowBits {
			z = montSqrN(z, invWindowBits)
			if w := (scalarNMinus2[limb] >> uint(shift)) & (invWindowSize - 1); w != 0 {
				z = montMul(z, table[w])
			}
		}
	}
	return montMul(z, scalar{1})
}

func mulShift384(a, b scalar) scalar {
	t := a.mulWide(b)
	z := scalar{t[6], t[7]}
	var c uint64
	z[0], c = bits.Add64(z[0], t[5]>>63, 0)
	z[1], c = bits.Add64(z[1], 0, c)
	z[2] = c
	return z
}

var (
	glvMinusB1     = scalar{0x6F547FA90ABFE4C3, 0xE4437ED6010E8828, 0, 0}
	glvMinusB2     = scalar{0xD765CDA83DB1562C, 0x8A280AC50774346D, 0xFFFFFFFFFFFFFFFE, 0xFFFFFFFFFFFFFFFF}
	glvG1          = scalar{0xE893209A45DBB031, 0x3DAA8A1471E8CA7F, 0xE86C90E49284EB15, 0x3086D221A7D46BCD}
	glvG2          = scalar{0x1571B4AE8AC47F71, 0x221208AC9DF506C6, 0x6F547FA90ABFE4C4, 0xE4437ED6010E8828}
	glvMinusLambda = scalar{0xE0CFC810B51283CF, 0xA880B9FC8EC739C2, 0x5AD9E3FD77ED9BA4, 0xAC9C52B33FA3CF1F}
	glvLambda      = scalar{0xDF02967C1B23BD72, 0x122E22EA20816678, 0xA5261C028812645A, 0x5363AD4CC05C30E0}
)

func (a scalar) splitLambda() (scalar, scalar) {
	c1 := mulShift384(a, glvG1).mul(glvMinusB1)
	c2 := mulShift384(a, glvG2).mul(glvMinusB2)
	r2 := c1.add(c2)
	r1 := r2.mul(glvMinusLambda).add(a)
	return r1, r2
}

type halfScalar struct {
	lo  uint64
	hi  uint64
	neg bool
}

func (a scalar) half() halfScalar {
	h := halfScalar{}
	if a.isHigh() {
		a = a.neg()
		h.neg = true
	}
	assert.Ok(a[2]|a[3] == 0, "secp256k1: lambda split exceeds 128 bits")
	h.lo, h.hi = a[0], a[1]
	return h
}
