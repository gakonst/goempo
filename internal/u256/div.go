package u256

import (
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

type quotRem struct {
	quot Wide
	rem  Int
}

func wideLen(w *Wide) int {
	for i := WideLimbs - 1; i >= 0; i-- {
		if w[i] != 0 {
			return i + 1
		}
	}
	return 0
}

func intLen(x Int) int {
	for i := Limbs - 1; i >= 0; i-- {
		if x[i] != 0 {
			return i + 1
		}
	}
	return 0
}

func divWide(num Wide, den Int) quotRem {
	dlen := intLen(den)
	assert.Ok(dlen > 0, "u256: division by zero reached divWide")
	nlen := wideLen(&num)
	if nlen < dlen {
		return quotRem{quot: Wide{}, rem: num.Low()}
	}
	if dlen == 1 {
		return divSingle(&num, nlen, den[0])
	}
	return divKnuth(&num, nlen, den)
}

func divSingle(num *Wide, nlen int, d uint64) quotRem {
	assert.Ok(d != 0, "u256: zero single-limb divisor")
	assert.Ok(nlen <= WideLimbs, "u256: numerator length out of range")
	nlen = min(nlen, WideLimbs)
	var out quotRem
	var r uint64
	for i := nlen - 1; i >= 0; i-- {
		out.quot[i], r = bits.Div64(r, num[i], d)
	}
	out.rem = FromUint64(r)
	return out
}

type normalized struct {
	u     [WideLimbs + 1]uint64
	v     Int
	shift uint
	dlen  int
	ulen  int
}

func normalize(num *Wide, nlen int, den Int) normalized {
	var n normalized
	nlen = min(nlen, WideLimbs)
	n.dlen = min(intLen(den), Limbs)
	n.ulen = nlen
	n.shift = uint(bits.LeadingZeros64(den[n.dlen-1]))
	n.v = shlSmall(den, n.shift)
	if n.shift == 0 {
		copy(n.u[:nlen], num[:nlen])
		return n
	}
	var carry uint64
	for i := 0; i < nlen; i++ {
		n.u[i] = num[i]<<n.shift | carry
		carry = num[i] >> (limbBits - n.shift)
	}
	n.u[nlen] = carry
	return n
}

func divKnuth(num *Wide, nlen int, den Int) quotRem {
	n := normalize(num, nlen, den)
	dlen := min(n.dlen, Limbs)
	assert.Ok(dlen >= 2, "u256: knuth division needs two divisor limbs")
	assert.Ok(n.ulen >= dlen, "u256: numerator shorter than divisor")
	var out quotRem
	top := min(n.ulen-dlen, WideLimbs-2)
	for j := top; j >= 0; j-- {
		qhat := estimateQuotient(&n, j)
		out.quot[j] = mulSubCorrect(&n, j, qhat)
	}
	var rem Int
	for i := 0; i < dlen; i++ {
		rem[i] = n.u[i] >> n.shift
		if n.shift != 0 {
			rem[i] |= n.u[i+1] << (limbBits - n.shift)
		}
	}
	out.rem = rem
	return out
}

func estimateQuotient(n *normalized, j int) uint64 {
	dlen := n.dlen
	vtop := n.v[dlen-1]
	vnext := multiplier(n.v[dlen-2])
	utop := n.u[j+dlen]
	if utop >= vtop {
		return ^uint64(0)
	}
	qhat, rhat := bits.Div64(utop, n.u[j+dlen-1], vtop)
	unext := n.u[j+dlen-2]
	if !product(qhat, vnext).exceeds(wordPair{hi: rhat, lo: unext}) {
		return qhat
	}
	qhat--
	prev := rhat
	rhat += vtop
	if rhat < prev {
		return qhat
	}
	if product(qhat, vnext).exceeds(wordPair{hi: rhat, lo: unext}) {
		qhat--
	}
	return qhat
}

type wordPair struct {
	hi uint64
	lo uint64
}

type multiplier uint64

func product(a uint64, b multiplier) wordPair {
	hi, lo := bits.Mul64(a, uint64(b))
	return wordPair{hi: hi, lo: lo}
}

func (p wordPair) exceeds(q wordPair) bool {
	if p.hi != q.hi {
		return p.hi > q.hi
	}
	return p.lo > q.lo
}

func mulSubCorrect(n *normalized, j int, qhat uint64) uint64 {
	dlen := min(n.dlen, Limbs)
	var borrow, carry uint64
	for i := 0; i < dlen; i++ {
		hi, lo := bits.Mul64(qhat, n.v[i])
		var c uint64
		lo, c = bits.Add64(lo, carry, 0)
		hi += c
		var b uint64
		n.u[j+i], b = bits.Sub64(n.u[j+i], lo, borrow)
		borrow = b
		carry = hi
	}
	var b uint64
	n.u[j+dlen], b = bits.Sub64(n.u[j+dlen], carry, borrow)
	if b == 0 {
		return qhat
	}
	var c uint64
	for i := 0; i < dlen; i++ {
		n.u[j+i], c = bits.Add64(n.u[j+i], n.v[i], c)
	}
	n.u[j+dlen] += c
	return qhat - 1
}

func shlSmall(x Int, s uint) Int {
	if s == 0 {
		return x
	}
	return Int{
		x[0] << s,
		x[1]<<s | x[0]>>(limbBits-s),
		x[2]<<s | x[1]>>(limbBits-s),
		x[3]<<s | x[2]>>(limbBits-s),
	}
}
