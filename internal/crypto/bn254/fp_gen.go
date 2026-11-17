package bn254

import "math/bits"

const fpLimbs = 4

const fpBytes = 32

const fpInv = 0x87d20782e4866389

type fp struct {
	l0 uint64
	l1 uint64
	l2 uint64
	l3 uint64
}

var fpModulus = fp{l0: 0x3c208c16d87cfd47, l1: 0x97816a916871ca8d, l2: 0xb85045b68181585d, l3: 0x30644e72e131a029}

var fpR2 = fp{l0: 0xf32cfc5b538afa89, l1: 0xb5e71911d44501fb, l2: 0x47ab1eff0a417ff6, l3: 0x06d89f71cab8351f}

var fpOne = fp{l0: 0xd35d438dc58f0d9d, l1: 0x0a78eb28f5c70b3d, l2: 0x666ea36f7879462c, l3: 0x0e0a77c19a07df2f}

var fpPMinus2 = fp{l0: 0x3c208c16d87cfd45, l1: 0x97816a916871ca8d, l2: 0xb85045b68181585d, l3: 0x30644e72e131a029}

func (a fp) rawSub(b fp) (fp, bool) {
	var z fp
	var c uint64
	z.l0, c = bits.Sub64(a.l0, b.l0, 0)
	z.l1, c = bits.Sub64(a.l1, b.l1, c)
	z.l2, c = bits.Sub64(a.l2, b.l2, c)
	z.l3, c = bits.Sub64(a.l3, b.l3, c)
	return z, c != 0
}

func (a fp) add(b fp) fp {
	var z, r fp
	var c, borrow uint64
	z.l0, c = bits.Add64(a.l0, b.l0, 0)
	z.l1, c = bits.Add64(a.l1, b.l1, c)
	z.l2, c = bits.Add64(a.l2, b.l2, c)
	z.l3, c = bits.Add64(a.l3, b.l3, c)
	r.l0, borrow = bits.Sub64(z.l0, fpModulus.l0, 0)
	r.l1, borrow = bits.Sub64(z.l1, fpModulus.l1, borrow)
	r.l2, borrow = bits.Sub64(z.l2, fpModulus.l2, borrow)
	r.l3, borrow = bits.Sub64(z.l3, fpModulus.l3, borrow)
	keep := -(borrow &^ c)
	r.l0 ^= (r.l0 ^ z.l0) & keep
	r.l1 ^= (r.l1 ^ z.l1) & keep
	r.l2 ^= (r.l2 ^ z.l2) & keep
	r.l3 ^= (r.l3 ^ z.l3) & keep
	return r
}

func (a fp) sub(b fp) fp {
	var z fp
	var c, borrow uint64
	z.l0, borrow = bits.Sub64(a.l0, b.l0, 0)
	z.l1, borrow = bits.Sub64(a.l1, b.l1, borrow)
	z.l2, borrow = bits.Sub64(a.l2, b.l2, borrow)
	z.l3, borrow = bits.Sub64(a.l3, b.l3, borrow)
	m := -borrow
	z.l0, c = bits.Add64(z.l0, fpModulus.l0&m, 0)
	z.l1, c = bits.Add64(z.l1, fpModulus.l1&m, c)
	z.l2, c = bits.Add64(z.l2, fpModulus.l2&m, c)
	z.l3, _ = bits.Add64(z.l3, fpModulus.l3&m, c)
	return z
}

func (a fp) neg() fp { return fp{}.sub(a) }

func (a fp) double() fp { return a.add(a) }

func (a fp) isZero() bool { return a.l0|a.l1|a.l2|a.l3 == 0 }

func (a fp) mul(b fp) fp {
	var hi, lo, c, carry, m uint64
	var t0, t1, t2, t3, t4, t5 uint64
	carry = 0
	hi, lo = bits.Mul64(a.l0, b.l0)
	lo, c = bits.Add64(lo, t0, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l0)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l0)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l0)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	t4, c = bits.Add64(t4, carry, 0)
	t5 = c
	m = t0 * fpInv
	hi, lo = bits.Mul64(m, fpModulus.l0)
	_, c = bits.Add64(lo, t0, 0)
	carry = hi + c
	hi, lo = bits.Mul64(m, fpModulus.l1)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l2)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l3)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	t3, c = bits.Add64(t4, carry, 0)
	t4 = t5 + c
	carry = 0
	hi, lo = bits.Mul64(a.l0, b.l1)
	lo, c = bits.Add64(lo, t0, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l1)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l1)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l1)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	t4, c = bits.Add64(t4, carry, 0)
	t5 = c
	m = t0 * fpInv
	hi, lo = bits.Mul64(m, fpModulus.l0)
	_, c = bits.Add64(lo, t0, 0)
	carry = hi + c
	hi, lo = bits.Mul64(m, fpModulus.l1)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l2)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l3)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	t3, c = bits.Add64(t4, carry, 0)
	t4 = t5 + c
	carry = 0
	hi, lo = bits.Mul64(a.l0, b.l2)
	lo, c = bits.Add64(lo, t0, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l2)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l2)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l2)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	t4, c = bits.Add64(t4, carry, 0)
	t5 = c
	m = t0 * fpInv
	hi, lo = bits.Mul64(m, fpModulus.l0)
	_, c = bits.Add64(lo, t0, 0)
	carry = hi + c
	hi, lo = bits.Mul64(m, fpModulus.l1)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l2)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l3)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	t3, c = bits.Add64(t4, carry, 0)
	t4 = t5 + c
	carry = 0
	hi, lo = bits.Mul64(a.l0, b.l3)
	lo, c = bits.Add64(lo, t0, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l3)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l3)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l3)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	t4, c = bits.Add64(t4, carry, 0)
	t5 = c
	m = t0 * fpInv
	hi, lo = bits.Mul64(m, fpModulus.l0)
	_, c = bits.Add64(lo, t0, 0)
	carry = hi + c
	hi, lo = bits.Mul64(m, fpModulus.l1)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l2)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(m, fpModulus.l3)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	t3, c = bits.Add64(t4, carry, 0)
	t4 = t5 + c
	z := fp{l0: t0, l1: t1, l2: t2, l3: t3}
	r, borrow := z.rawSub(fpModulus)
	if !borrow || t4 != 0 {
		return r
	}
	return z
}

func (a fp) sqr() fp { return a.mul(a) }
