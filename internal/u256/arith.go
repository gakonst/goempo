package u256

import "math/bits"

const WideLimbs = 2 * Limbs

type Wide [WideLimbs]uint64

func (x Int) Add(y Int) Int {
	var z Int
	var c uint64
	z[0], c = bits.Add64(x[0], y[0], 0)
	z[1], c = bits.Add64(x[1], y[1], c)
	z[2], c = bits.Add64(x[2], y[2], c)
	z[3], _ = bits.Add64(x[3], y[3], c)
	return z
}

func (x Int) AddOverflow(y Int) (Int, bool) {
	var z Int
	var c uint64
	z[0], c = bits.Add64(x[0], y[0], 0)
	z[1], c = bits.Add64(x[1], y[1], c)
	z[2], c = bits.Add64(x[2], y[2], c)
	z[3], c = bits.Add64(x[3], y[3], c)
	return z, c != 0
}

func (x Int) Sub(y Int) Int {
	var z Int
	var b uint64
	z[0], b = bits.Sub64(x[0], y[0], 0)
	z[1], b = bits.Sub64(x[1], y[1], b)
	z[2], b = bits.Sub64(x[2], y[2], b)
	z[3], _ = bits.Sub64(x[3], y[3], b)
	return z
}

func (x Int) SubOverflow(y Int) (Int, bool) {
	var z Int
	var b uint64
	z[0], b = bits.Sub64(x[0], y[0], 0)
	z[1], b = bits.Sub64(x[1], y[1], b)
	z[2], b = bits.Sub64(x[2], y[2], b)
	z[3], b = bits.Sub64(x[3], y[3], b)
	return z, b != 0
}

func (x Int) Neg() Int { return Int{}.Sub(x) }

func (x Int) Mul(y Int) Int {
	var z Int
	for i := 0; i < Limbs; i++ {
		var carry uint64
		for k := i; k < Limbs; k++ {
			hi, lo := bits.Mul64(x[i], y[k-i])
			var c uint64
			lo, c = bits.Add64(lo, z[k], 0)
			hi += c
			lo, c = bits.Add64(lo, carry, 0)
			hi += c
			z[k] = lo
			carry = hi
		}
	}
	return z
}

func (x Int) MulFull(y Int) Wide {
	var z Wide
	for i := 0; i < Limbs; i++ {
		var carry uint64
		for j := 0; j < Limbs; j++ {
			hi, lo := bits.Mul64(x[i], y[j])
			var c uint64
			lo, c = bits.Add64(lo, z[i+j], 0)
			hi += c
			lo, c = bits.Add64(lo, carry, 0)
			hi += c
			z[i+j] = lo
			carry = hi
		}
		z[i+Limbs] = carry
	}
	return z
}

func (x Int) AddFull(y Int) Wide {
	z, carry := x.AddOverflow(y)
	var w Wide
	copy(w[:Limbs], z[:])
	if carry {
		w[Limbs] = 1
	}
	return w
}

func (w Wide) Low() Int { return Int{w[0], w[1], w[2], w[3]} }

func (w Wide) High() Int { return Int{w[4], w[5], w[6], w[7]} }

func (w Wide) Mod(m Int) Int {
	if m.IsZero() {
		return Int{}
	}
	if w.High().IsZero() {
		return w.Low().Mod(m)
	}
	return divWide(w, m).rem
}

func (x Int) Div(y Int) Int {
	if y.IsZero() {
		return Int{}
	}
	if x.Lt(y) {
		return Int{}
	}
	if y.IsUint64() {
		if x.IsUint64() {
			return FromUint64(x[0] / y[0])
		}
	}
	var w Wide
	copy(w[:Limbs], x[:])
	return divWide(w, y).quot.Low()
}

func (x Int) Mod(y Int) Int {
	if y.IsZero() {
		return Int{}
	}
	if x.Lt(y) {
		return x
	}
	if y.IsUint64() {
		if x.IsUint64() {
			return FromUint64(x[0] % y[0])
		}
	}
	var w Wide
	copy(w[:Limbs], x[:])
	return divWide(w, y).rem
}

func (x Int) Abs() Int {
	if x.IsNegative() {
		return x.Neg()
	}
	return x
}

func (x Int) SDiv(y Int) Int {
	if y.IsZero() {
		return Int{}
	}
	q := x.Abs().Div(y.Abs())
	if x.IsNegative() != y.IsNegative() {
		return q.Neg()
	}
	return q
}

func (x Int) SMod(y Int) Int {
	if y.IsZero() {
		return Int{}
	}
	r := x.Abs().Mod(y.Abs())
	if x.IsNegative() {
		return r.Neg()
	}
	return r
}

func (x Int) Exp(e Int) Int {
	result := One()
	base := x
	for limb := 0; limb < Limbs; limb++ {
		word := e[limb]
		for bit := 0; bit < limbBits; bit++ {
			if word&1 == 1 {
				result = result.Mul(base)
			}
			word >>= 1
			if word == 0 {
				if e.highLimbsZero(limb) {
					return result
				}
			}
			base = base.Mul(base)
		}
	}
	return result
}

func (x Int) highLimbsZero(limb int) bool {
	for i := limb + 1; i < Limbs; i++ {
		if x[i] != 0 {
			return false
		}
	}
	return true
}
