package secp256k1

import "math/bits"

const fieldLimbs = 4

type fe struct {
	l0 uint64
	l1 uint64
	l2 uint64
	l3 uint64
}

const reduceC = 0x1000003D1

var fieldP = fe{0xFFFFFFFEFFFFFC2F, 0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF}

func feFromUint64(v uint64) fe { return fe{l0: v} }

func (a fe) isZero() bool { return a.l0|a.l1|a.l2|a.l3 == 0 }

func (a fe) isOdd() bool { return a.l0&1 == 1 }

func (a fe) geP() bool {
	_, borrow := a.rawSub(fieldP)
	return !borrow
}

func (a fe) rawSub(b fe) (fe, bool) {
	var z fe
	var c uint64
	z.l0, c = bits.Sub64(a.l0, b.l0, 0)
	z.l1, c = bits.Sub64(a.l1, b.l1, c)
	z.l2, c = bits.Sub64(a.l2, b.l2, c)
	z.l3, c = bits.Sub64(a.l3, b.l3, c)
	return z, c != 0
}

func (a fe) add(b fe) fe {
	var z, r fe
	var c, borrow uint64
	z.l0, c = bits.Add64(a.l0, b.l0, 0)
	z.l1, c = bits.Add64(a.l1, b.l1, c)
	z.l2, c = bits.Add64(a.l2, b.l2, c)
	z.l3, c = bits.Add64(a.l3, b.l3, c)
	r.l0, borrow = bits.Sub64(z.l0, fieldP.l0, 0)
	r.l1, borrow = bits.Sub64(z.l1, fieldP.l1, borrow)
	r.l2, borrow = bits.Sub64(z.l2, fieldP.l2, borrow)
	r.l3, borrow = bits.Sub64(z.l3, fieldP.l3, borrow)
	keep := -(borrow &^ c)
	r.l0 ^= (r.l0 ^ z.l0) & keep
	r.l1 ^= (r.l1 ^ z.l1) & keep
	r.l2 ^= (r.l2 ^ z.l2) & keep
	r.l3 ^= (r.l3 ^ z.l3) & keep
	return r
}

func (a fe) sub(b fe) fe {
	var z fe
	var c, borrow uint64
	z.l0, borrow = bits.Sub64(a.l0, b.l0, 0)
	z.l1, borrow = bits.Sub64(a.l1, b.l1, borrow)
	z.l2, borrow = bits.Sub64(a.l2, b.l2, borrow)
	z.l3, borrow = bits.Sub64(a.l3, b.l3, borrow)
	m := -borrow
	z.l0, c = bits.Add64(z.l0, fieldP.l0&m, 0)
	z.l1, c = bits.Add64(z.l1, fieldP.l1&m, c)
	z.l2, c = bits.Add64(z.l2, fieldP.l2&m, c)
	z.l3, _ = bits.Add64(z.l3, fieldP.l3&m, c)
	return z
}

func (a fe) neg() fe { return fe{}.sub(a) }

func (a fe) double() fe { return a.add(a) }

func (a fe) mulGeneric(b fe) fe {
	var hi, lo, c, carry uint64
	var t0, t1, t2, t3, t4, t5, t6, t7 uint64
	carry = 0
	hi, lo = bits.Mul64(a.l0, b.l0)
	t0 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l0, b.l1)
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l0, b.l2)
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l0, b.l3)
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	t4 = carry
	carry = 0
	hi, lo = bits.Mul64(a.l1, b.l0)
	lo, c = bits.Add64(lo, t1, 0)
	hi += c
	t1 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l1)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l2)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l1, b.l3)
	lo, c = bits.Add64(lo, t4, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t4 = lo
	carry = hi
	t5 = carry
	carry = 0
	hi, lo = bits.Mul64(a.l2, b.l0)
	lo, c = bits.Add64(lo, t2, 0)
	hi += c
	t2 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l1)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t3 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l2)
	lo, c = bits.Add64(lo, t4, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t4 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l2, b.l3)
	lo, c = bits.Add64(lo, t5, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t5 = lo
	carry = hi
	t6 = carry
	carry = 0
	hi, lo = bits.Mul64(a.l3, b.l0)
	lo, c = bits.Add64(lo, t3, 0)
	hi += c
	t3 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l1)
	lo, c = bits.Add64(lo, t4, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t4 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l2)
	lo, c = bits.Add64(lo, t5, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t5 = lo
	carry = hi
	hi, lo = bits.Mul64(a.l3, b.l3)
	lo, c = bits.Add64(lo, t6, 0)
	hi += c
	lo, c = bits.Add64(lo, carry, 0)
	hi += c
	t6 = lo
	carry = hi
	t7 = carry
	return reduceWide(wide{t0, t1, t2, t3, t4, t5, t6, t7})
}

type wide [8]uint64

func reduceWide(t wide) fe {
	t0, t1, t2, t3, t4, t5, t6, t7 := t[0], t[1], t[2], t[3], t[4], t[5], t[6], t[7]
	var c, c2 uint64
	h0, l0 := bits.Mul64(t4, reduceC)
	h1, l1 := bits.Mul64(t5, reduceC)
	h2, l2 := bits.Mul64(t6, reduceC)
	h3, l3 := bits.Mul64(t7, reduceC)
	var z fe
	z.l0, c = bits.Add64(t0, l0, 0)
	z.l1, c = bits.Add64(t1, l1, c)
	z.l2, c = bits.Add64(t2, l2, c)
	z.l3, c = bits.Add64(t3, l3, c)
	top := h3 + c
	z.l1, c2 = bits.Add64(z.l1, h0, 0)
	z.l2, c2 = bits.Add64(z.l2, h1, c2)
	z.l3, c2 = bits.Add64(z.l3, h2, c2)
	top += c2
	fh, fl := bits.Mul64(top, reduceC)
	z.l0, c = bits.Add64(z.l0, fl, 0)
	z.l1, c = bits.Add64(z.l1, fh, c)
	z.l2, c = bits.Add64(z.l2, 0, c)
	z.l3, c = bits.Add64(z.l3, 0, c)
	z.l0, c = bits.Add64(z.l0, reduceC*c, 0)
	z.l1, c = bits.Add64(z.l1, 0, c)
	z.l2, c = bits.Add64(z.l2, 0, c)
	z.l3, _ = bits.Add64(z.l3, 0, c)
	reduced, borrow := z.rawSub(fieldP)
	if !borrow {
		return reduced
	}
	return z
}

func (a fe) sqrGeneric() fe {
	var c uint64
	h01, l01 := bits.Mul64(a.l0, a.l1)
	h02, l02 := bits.Mul64(a.l0, a.l2)
	h03, l03 := bits.Mul64(a.l0, a.l3)
	h12, l12 := bits.Mul64(a.l1, a.l2)
	h13, l13 := bits.Mul64(a.l1, a.l3)
	h23, l23 := bits.Mul64(a.l2, a.l3)
	t1 := l01
	t2, c := bits.Add64(l02, h01, 0)
	t3, c := bits.Add64(l03, h02, c)
	t4, _ := bits.Add64(h03, 0, c)
	t3, c = bits.Add64(t3, l12, 0)
	t4, c = bits.Add64(t4, h12, c)
	t5, _ := bits.Add64(h13, 0, c)
	t4, c = bits.Add64(t4, l13, 0)
	t5, c = bits.Add64(t5, l23, c)
	t6, _ := bits.Add64(h23, 0, c)
	t7 := t6 >> 63
	t6 = t6<<1 | t5>>63
	t5 = t5<<1 | t4>>63
	t4 = t4<<1 | t3>>63
	t3 = t3<<1 | t2>>63
	t2 = t2<<1 | t1>>63
	t1 <<= 1
	h00, l00 := bits.Mul64(a.l0, a.l0)
	h11, l11 := bits.Mul64(a.l1, a.l1)
	h22, l22 := bits.Mul64(a.l2, a.l2)
	h33, l33 := bits.Mul64(a.l3, a.l3)
	t1, c = bits.Add64(t1, h00, 0)
	t2, c = bits.Add64(t2, l11, c)
	t3, c = bits.Add64(t3, h11, c)
	t4, c = bits.Add64(t4, l22, c)
	t5, c = bits.Add64(t5, h22, c)
	t6, c = bits.Add64(t6, l33, c)
	t7, _ = bits.Add64(t7, h33, c)
	return reduceWide(wide{l00, t1, t2, t3, t4, t5, t6, t7})
}

func (a fe) sqrNGeneric(n int) fe {
	z := a
	for i := 0; i < n; i++ {
		z = z.sqr()
	}
	return z
}

type powers struct {
	x2   fe
	x22  fe
	x223 fe
}

func (a fe) powChain() powers {
	x2 := a.sqr().mul(a)
	x3 := x2.sqr().mul(a)
	x6 := x3.sqrN(3).mul(x3)
	x9 := x6.sqrN(3).mul(x3)
	x11 := x9.sqrN(2).mul(x2)
	x22 := x11.sqrN(11).mul(x11)
	x44 := x22.sqrN(22).mul(x22)
	x88 := x44.sqrN(44).mul(x44)
	x176 := x88.sqrN(88).mul(x88)
	x220 := x176.sqrN(44).mul(x44)
	x223 := x220.sqrN(3).mul(x3)
	return powers{x2: x2, x22: x22, x223: x223}
}

func (a fe) inv() fe {
	pw := a.powChain()
	t := pw.x223.sqrN(23).mul(pw.x22)
	t = t.sqrN(5).mul(a)
	t = t.sqrN(3).mul(pw.x2)
	return t.sqrN(2).mul(a)
}

func (a fe) sqrt() (fe, bool) {
	pw := a.powChain()
	t := pw.x223.sqrN(23).mul(pw.x22)
	t = t.sqrN(6).mul(pw.x2)
	r := t.sqrN(2)
	return r, r.sqr() == a
}

func feFromBytes(b *[32]byte) (fe, bool) {
	z := fe{l3: beUint64(b[0:8]), l2: beUint64(b[8:16]), l1: beUint64(b[16:24]), l0: beUint64(b[24:32])}
	return z, !z.geP()
}

func (a fe) bytes() [32]byte {
	var b [32]byte
	putBeUint64(b[0:8], a.l3)
	putBeUint64(b[8:16], a.l2)
	putBeUint64(b[16:24], a.l1)
	putBeUint64(b[24:32], a.l0)
	return b
}
