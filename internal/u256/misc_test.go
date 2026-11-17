package u256_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/u256"
)

func TestBytesRoundTrip(t *testing.T) {
	var b [32]byte
	for i := range b {
		b[i] = byte(i + 1)
	}
	x := u256.FromBytes32(b)
	if x[3] != 0x0102030405060708 || x[0] != 0x191a1b1c1d1e1f20 {
		t.Fatalf("limb order: %x", x)
	}
	if x.Bytes32() != b {
		t.Fatalf("round trip")
	}
	if u256.FromBytes([]byte{1, 2}) != u256.FromUint64(0x0102) {
		t.Fatalf("short bytes")
	}
	dst := make([]byte, 40)
	x.PutBytes32(dst[8:])
	if [32]byte(dst[8:]) != b {
		t.Fatalf("put bytes")
	}
}

func TestHex(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want u256.Int
		hex  string
	}{
		{name: "zero", in: "0x0", want: u256.Zero(), hex: "0x0"},
		{name: "odd", in: "0x123", want: u256.FromUint64(0x123), hex: "0x123"},
		{name: "bare", in: "ff", want: u256.FromUint64(0xff), hex: "0xff"},
		{name: "max", in: "0x" + string(make64('f')), want: u256.Max(), hex: "0x" + string(make64('f'))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := u256.FromHex(test.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %x", got)
			}
			if got.Hex() != test.hex {
				t.Fatalf("hex %s", got.Hex())
			}
		})
	}
	bad := []string{"", "0x", "0xzz", "0x1" + string(make64('0'))}
	for _, s := range bad {
		if _, err := u256.FromHex(s); err == nil {
			t.Fatalf("FromHex(%q) accepted", s)
		}
	}
}

func make64(c byte) []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return b
}

func TestFromBytesTooLongPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no assertion")
		}
	}()
	u256.FromBytes(make([]byte, 33))
}

func TestMetrics(t *testing.T) {
	if u256.Zero().BitLen() != 0 || u256.Max().BitLen() != 256 || u256.FromUint64(256).ByteLen() != 2 {
		t.Fatal("bitlen")
	}
	if u256.Zero().Clz() != u256.FromUint64(256) || u256.Max().Clz() != u256.Zero() || u256.One().Clz() != u256.FromUint64(255) {
		t.Fatal("clz")
	}
	if !u256.FromUint64(5).IsUint64() || u256.MinSigned().IsUint64() {
		t.Fatal("isuint64")
	}
	if u256.Max().Cmp(u256.Zero()) != 1 || u256.Zero().Cmp(u256.One()) != -1 || u256.One().Cmp(u256.One()) != 0 {
		t.Fatal("cmp")
	}
	if _, over := u256.Max().AddOverflow(u256.One()); !over {
		t.Fatal("add overflow")
	}
	if _, under := u256.Zero().SubOverflow(u256.One()); !under {
		t.Fatal("sub overflow")
	}
	if u256.MinSigned().Abs() != u256.MinSigned() {
		t.Fatal("abs of min")
	}
}

func TestNoAllocation(t *testing.T) {
	a := u256.Max().Rsh(3)
	b := u256.FromUint64(12345).Lsh(70)
	m := u256.FromUint64(977).Lsh(130)
	allocs := testing.AllocsPerRun(100, func() {
		a = a.Div(b).Add(a.Mod(b)).Mul(a).Exp(b)
		a = a.MulFull(b).Mod(m).SDiv(b).SMod(m)
	})
	if allocs != 0 {
		t.Fatalf("%v allocations", allocs)
	}
}

func BenchmarkMul(b *testing.B) {
	x := u256.Max().Rsh(1)
	y := u256.Max().Rsh(5)
	for i := 0; i < b.N; i++ {
		x = x.Mul(y)
	}
}

func BenchmarkDiv(b *testing.B) {
	x := u256.Max()
	y := u256.Max().Rsh(77)
	var z u256.Int
	for i := 0; i < b.N; i++ {
		z = x.Div(y)
	}
	_ = z
}

func BenchmarkMulMod(b *testing.B) {
	x := u256.Max().Rsh(1)
	y := u256.Max().Rsh(5)
	m := u256.Max().Rsh(9)
	for i := 0; i < b.N; i++ {
		x = x.MulFull(y).Mod(m)
	}
}
