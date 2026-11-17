package secp256k1

import (
	"math/big"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

type xorshift uint64

func (x *xorshift) next() uint64 {
	*x ^= *x << 13
	*x ^= *x >> 7
	*x ^= *x << 17
	return uint64(*x)
}

func (x *xorshift) words() [4]uint64 {
	return [4]uint64{x.next(), x.next(), x.next(), x.next()}
}

func limbsBig(w [4]uint64) *big.Int {
	z := new(big.Int)
	for i := 3; i >= 0; i-- {
		z.Lsh(z, 64)
		z.Or(z, new(big.Int).SetUint64(w[i]))
	}
	return z
}

func bigLimbs(t *testing.T, z *big.Int) [4]uint64 {
	t.Helper()
	if z.Sign() < 0 || z.BitLen() > 256 {
		t.Fatalf("value %v out of range", z)
	}
	var w [4]uint64
	m := new(big.Int).SetUint64(^uint64(0))
	v := new(big.Int).Set(z)
	for i := range w {
		w[i] = new(big.Int).And(v, m).Uint64()
		v.Rsh(v, 64)
	}
	return w
}

var (
	bigN = limbsBig(scalarN)
	bigP = limbsBig([4]uint64{fieldP.l0, fieldP.l1, fieldP.l2, fieldP.l3})
)

func feWords(a fe) [4]uint64 { return [4]uint64{a.l0, a.l1, a.l2, a.l3} }

func edgeWords() [][4]uint64 {
	return [][4]uint64{
		{0, 0, 0, 0},
		{1, 0, 0, 0},
		{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)},
		{fieldP.l0 - 1, fieldP.l1, fieldP.l2, fieldP.l3},
		{scalarN[0] - 1, scalarN[1], scalarN[2], scalarN[3]},
		{0, 0, 0, 1 << 63},
	}
}

func TestFieldSqrMatchesMul(t *testing.T) {
	rng := xorshift(0x9E3779B97F4A7C15)
	inputs := edgeWords()
	for range 20000 {
		inputs = append(inputs, rng.words())
	}
	for _, w := range inputs {
		v := new(big.Int).Mod(limbsBig(w), bigP)
		a := fe{}
		l := bigLimbs(t, v)
		a.l0, a.l1, a.l2, a.l3 = l[0], l[1], l[2], l[3]
		want := bigLimbs(t, new(big.Int).Mod(new(big.Int).Mul(v, v), bigP))
		if got := feWords(a.sqr()); got != want {
			t.Fatalf("sqr(%x) = %x, want %x", w, got, want)
		}
		if got := feWords(a.mul(a)); got != want {
			t.Fatalf("mul(%x) = %x, want %x", w, got, want)
		}
		if got := feWords(a.sqrGeneric()); got != want {
			t.Fatalf("sqrGeneric(%x) = %x, want %x", w, got, want)
		}
		if got := feWords(a.mulGeneric(a)); got != want {
			t.Fatalf("mulGeneric(%x) = %x, want %x", w, got, want)
		}
		bv := new(big.Int).Mod(new(big.Int).Rsh(new(big.Int).Mul(v, big.NewInt(0x5DEECE66D)), 3), bigP)
		bl := bigLimbs(t, bv)
		b := fe{l0: bl[0], l1: bl[1], l2: bl[2], l3: bl[3]}
		prod := bigLimbs(t, new(big.Int).Mod(new(big.Int).Mul(v, bv), bigP))
		if got := feWords(a.mul(b)); got != prod {
			t.Fatalf("mul(%x, %x) = %x, want %x", w, bl, got, prod)
		}
		if got := feWords(a.mulGeneric(b)); got != prod {
			t.Fatalf("mulGeneric(%x, %x) = %x, want %x", w, bl, got, prod)
		}
	}
}

func TestScalarArithmeticMatchesBig(t *testing.T) {
	rng := xorshift(0xD1B54A32D192ED03)
	inputs := edgeWords()
	for range 5000 {
		inputs = append(inputs, rng.words())
	}
	for i, w := range inputs {
		av := new(big.Int).Mod(limbsBig(w), bigN)
		bv := new(big.Int).Mod(limbsBig(inputs[(i*7+3)%len(inputs)]), bigN)
		a, b := scalar(bigLimbs(t, av)), scalar(bigLimbs(t, bv))
		mul := bigLimbs(t, new(big.Int).Mod(new(big.Int).Mul(av, bv), bigN))
		if got, want := a.mul(a), scalar(bigLimbs(t, new(big.Int).Mod(new(big.Int).Mul(av, av), bigN))); got != want {
			t.Fatalf("mul %x squared = %x, want %x", a, got, want)
		}
		if montMul(a, a) != montMulGeneric(a, a) {
			t.Fatalf("montMul %x squared differs from generic", a)
		}
		if montMul(a, b) != montMulGeneric(a, b) {
			t.Fatalf("montMul %x %x differs from generic", a, b)
		}
		if n := 1 + i%9; montSqrN(a, n) != montSqrNGeneric(a, n) {
			t.Fatalf("montSqrN %x %d differs from generic", a, n)
		}
		fa := fe{l0: a[0], l1: a[1], l2: a[2], l3: a[3] >> 1}
		if n := 1 + i%23; fa.sqrN(n) != fa.sqrNGeneric(n) {
			t.Fatalf("sqrN %x %d differs from generic", a, n)
		}
		if got := a.mul(b); got != scalar(mul) {
			t.Fatalf("mul %x %x = %x, want %x", a, b, got, mul)
		}
		add := bigLimbs(t, new(big.Int).Mod(new(big.Int).Add(av, bv), bigN))
		if got := a.add(b); got != scalar(add) {
			t.Fatalf("add %x %x = %x, want %x", a, b, got, add)
		}
		neg := bigLimbs(t, new(big.Int).Mod(new(big.Int).Neg(av), bigN))
		if got := a.neg(); got != scalar(neg) {
			t.Fatalf("neg %x = %x, want %x", a, got, neg)
		}
		if got, want := a.isHigh(), av.Cmp(new(big.Int).Rsh(bigN, 1)) > 0; got != want {
			t.Fatalf("isHigh %x = %v", a, got)
		}
		raw := scalar(w)
		if got, want := raw.reduceOnce(), scalar(bigLimbs(t, new(big.Int).Mod(limbsBig(w), bigN))); got != want {
			t.Fatalf("reduceOnce %x = %x, want %x", raw, got, want)
		}
		if a.isZero() || i%5 != 0 {
			continue
		}
		inv := bigLimbs(t, new(big.Int).ModInverse(av, bigN))
		if got := a.inv(); got != scalar(inv) {
			t.Fatalf("inv %x = %x, want %x", a, got, inv)
		}
	}
	if scalarNHalf != scalar(u256.Int(scalarN).Rsh(1)) {
		t.Fatal("half order")
	}
}

func TestLambdaSplit(t *testing.T) {
	rng := xorshift(0xA0761D6478BD642F)
	lam := limbsBig(glvLambda)
	if new(big.Int).Exp(lam, big.NewInt(3), bigN).Cmp(big.NewInt(1)) != 0 {
		t.Fatal("lambda is not a cube root of unity mod n")
	}
	if new(big.Int).Mod(new(big.Int).Add(lam, limbsBig(glvMinusLambda)), bigN).Sign() != 0 {
		t.Fatal("minus lambda")
	}
	beta := limbsBig(feWords(feBeta))
	if new(big.Int).Exp(beta, big.NewInt(3), bigP).Cmp(big.NewInt(1)) != 0 {
		t.Fatal("beta is not a cube root of unity mod p")
	}
	inputs := edgeWords()
	for range 20000 {
		inputs = append(inputs, rng.words())
	}
	for _, w := range inputs {
		k := scalar(w).reduceOnce()
		r1, r2 := k.splitLambda()
		h1, h2 := r1.half(), r2.half()
		sum := new(big.Int).Add(limbsBig(r1), new(big.Int).Mul(limbsBig(r2), lam))
		if sum.Mod(sum, bigN).Cmp(limbsBig(k)) != 0 {
			t.Fatalf("split of %x does not recombine", k)
		}
		_, _ = h1, h2
	}
}

func TestLambdaEndomorphism(t *testing.T) {
	lp := baseMul(u256.Int(glvLambda)).toAffine()
	if lp.x != generator.x.mul(feBeta) || lp.y != generator.y {
		t.Fatal("lambda*G != (beta*Gx, Gy)")
	}
}

func TestWNAFRecombines(t *testing.T) {
	rng := xorshift(0xE7037ED1A0B428DB)
	for range 20000 {
		lo, hi := rng.next(), rng.next()
		for _, window := range []uint{pointWindow, baseWindow} {
			var w wnaf
			w.encode(lo, hi, window)
			sum := new(big.Int)
			for i := w.length - 1; i >= 0; i-- {
				sum.Lsh(sum, 1)
				sum.Add(sum, big.NewInt(int64(w.digits[i])))
				if d := int(w.digits[i]); d != 0 && (d%2 == 0 || d >= 1<<(window-1) || d <= -(1<<(window-1))) {
					t.Fatalf("digit %d outside window %d", d, window)
				}
			}
			want := limbsBig([4]uint64{lo, hi})
			if sum.Cmp(want) != 0 {
				t.Fatalf("wNAF of %x:%x recombines to %v", hi, lo, sum)
			}
		}
	}
}

func TestEcmultMatchesShamir(t *testing.T) {
	rng := xorshift(0x8EBC6AF09C88C6E3)
	p := generator
	for i := range 300 {
		g := scalar(rng.words()).reduceOnce()
		k := scalar(rng.words()).reduceOnce()
		switch i {
		case 0:
			g = scalar{}
		case 1:
			k = scalar{}
		case 2:
			g, k = scalar{1}, scalarN.neg()
		}
		var wk recoverWork
		got := wk.ecmult(g, k, p).toAffine()
		want := shamir(u256.Int(g), pointScalar{point: p.toJacobian(), scalar: u256.Int(k)}).toAffine()
		if got != want {
			t.Fatalf("ecmult(%x, %x) differs", g, k)
		}
		p = want
		if p.infinity {
			p = generator
		}
	}
}

func BenchmarkFieldMul(b *testing.B) {
	a := generator.x
	c := generator.y
	for b.Loop() {
		a = a.mul(c)
	}
	_ = a
}

func BenchmarkFieldSqr(b *testing.B) {
	a := generator.x
	for b.Loop() {
		a = a.sqr()
	}
	_ = a
}

func BenchmarkFieldInv(b *testing.B) {
	a := generator.x
	for b.Loop() {
		a = a.inv()
	}
	_ = a
}

func BenchmarkScalarMul(b *testing.B) {
	a := glvG1
	for b.Loop() {
		a = a.mul(glvG2)
	}
	_ = a
}

func BenchmarkScalarInv(b *testing.B) {
	a := glvG1
	for b.Loop() {
		a = a.inv()
	}
	_ = a
}

func BenchmarkScalarModInverseU256(b *testing.B) {
	a := u256.Int(glvG1)
	for b.Loop() {
		a, _ = a.ModInverse(curveOrder)
	}
	_ = a
}

func BenchmarkEcmult(b *testing.B) {
	var wk recoverWork
	for b.Loop() {
		_ = wk.ecmult(glvG1, glvG2, generator)
	}
}

var _ = primitives.Hash{}
