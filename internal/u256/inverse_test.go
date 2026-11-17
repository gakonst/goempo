package u256_test

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/u256"
)

func TestModInverseAgainstBig(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	p := mustHex(t, "0xfffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2f")
	n := mustHex(t, "0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	moduli := []u256.Int{p, n, u256.Max(), u256.FromUint64(3), u256.FromUint64(97)}
	for i := 0; i < 3000; i++ {
		m := moduli[i%len(moduli)]
		if i%7 == 6 {
			m = randomValue(r)
			m[0] |= 1
		}
		x := randomValue(r)
		got, ok := x.ModInverse(m)
		want := new(big.Int).ModInverse(toBig(x), toBig(m))
		if m == u256.One() {
			want = nil
		}
		if want == nil {
			if ok {
				t.Fatalf("inverse of %s mod %s should not exist", x.Hex(), m.Hex())
			}
			continue
		}
		if !ok || toBig(got).Cmp(want) != 0 {
			t.Fatalf("inverse of %s mod %s = %s %v, want %s", x.Hex(), m.Hex(), got.Hex(), ok, want.Text(16))
		}
	}
	if _, ok := u256.FromUint64(3).ModInverse(u256.FromUint64(10)); ok {
		t.Fatal("even modulus accepted")
	}
	if _, ok := u256.FromUint64(3).ModInverse(u256.FromUint64(9)); ok {
		t.Fatal("non-coprime accepted")
	}
}

func BenchmarkModInverse(b *testing.B) {
	n := mustHex(b, "0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	x := u256.Max().Rsh(3)
	for i := 0; i < b.N; i++ {
		x, _ = x.ModInverse(n)
	}
}

func mustHex(tb testing.TB, s string) u256.Int {
	tb.Helper()
	v, err := u256.FromHex(s)
	if err != nil {
		tb.Fatal(err)
	}
	return v
}
