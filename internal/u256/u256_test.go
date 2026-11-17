package u256_test

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type truth bool

var (
	two256 = new(big.Int).Lsh(big.NewInt(1), 256)
	mask   = new(big.Int).Sub(two256, big.NewInt(1))
	two255 = new(big.Int).Lsh(big.NewInt(1), 255)
)

func toBig(x u256.Int) *big.Int {
	b := x.Bytes32()
	return new(big.Int).SetBytes(b[:])
}

func toSigned(x u256.Int) *big.Int {
	v := toBig(x)
	if v.Cmp(two255) >= 0 {
		v.Sub(v, two256)
	}
	return v
}

func fromBig(v *big.Int) u256.Int {
	w := new(big.Int).And(v, mask)
	var buf [32]byte
	w.FillBytes(buf[:])
	return u256.FromBytes32(buf)
}

func boolInt(b truth) *big.Int {
	if b {
		return big.NewInt(1)
	}
	return big.NewInt(0)
}

type bigPair struct {
	a *big.Int
	b *big.Int
}

func refBinary(name string, in bigPair) *big.Int {
	a, b := in.a, in.b
	sa, sb := signed(a), signed(b)
	r := new(big.Int)
	switch name {
	case "add":
		return r.Add(a, b)
	case "sub":
		return r.Sub(a, b)
	case "mul":
		return r.Mul(a, b)
	case "div":
		if b.Sign() == 0 {
			return r
		}
		return r.Quo(a, b)
	case "mod":
		if b.Sign() == 0 {
			return r
		}
		return r.Rem(a, b)
	case "sdiv":
		if b.Sign() == 0 {
			return r
		}
		return r.Quo(sa, sb)
	case "smod":
		if b.Sign() == 0 {
			return r
		}
		return r.Rem(sa, sb)
	case "exp":
		return r.Exp(a, b, two256)
	case "and":
		return r.And(a, b)
	case "or":
		return r.Or(a, b)
	case "xor":
		return r.Xor(a, b)
	case "lt":
		return boolInt(truth(a.Cmp(b) < 0))
	case "gt":
		return boolInt(truth(a.Cmp(b) > 0))
	case "slt":
		return boolInt(truth(sa.Cmp(sb) < 0))
	case "sgt":
		return boolInt(truth(sa.Cmp(sb) > 0))
	case "eq":
		return boolInt(truth(a.Cmp(b) == 0))
	case "byte":
		if a.Cmp(big.NewInt(32)) >= 0 {
			return r
		}
		r.Rsh(b, uint(8*(31-a.Int64())))
		return r.And(r, big.NewInt(0xff))
	case "shl":
		if a.Cmp(big.NewInt(256)) >= 0 {
			return r
		}
		return r.Lsh(b, uint(a.Int64()))
	case "shr":
		if a.Cmp(big.NewInt(256)) >= 0 {
			return r
		}
		return r.Rsh(b, uint(a.Int64()))
	case "sar":
		sv := signed(b)
		if a.Cmp(big.NewInt(256)) >= 0 {
			if sv.Sign() < 0 {
				return big.NewInt(-1)
			}
			return r
		}
		return r.Rsh(sv, uint(a.Int64()))
	case "signextend":
		if a.Cmp(big.NewInt(31)) >= 0 {
			return new(big.Int).Set(b)
		}
		bit := uint(8*a.Int64() + 7)
		low := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bit+1), big.NewInt(1))
		if b.Bit(int(bit)) == 1 {
			return r.Or(b, new(big.Int).Xor(low, mask))
		}
		return r.And(b, low)
	}
	assert.Fail("unknown op %s", name)
	return nil
}

func signed(v *big.Int) *big.Int {
	s := new(big.Int).Set(v)
	if s.Cmp(two255) >= 0 {
		s.Sub(s, two256)
	}
	return s
}

func fromBool(b truth) u256.Int {
	if b {
		return u256.One()
	}
	return u256.Zero()
}

type binaryOp struct {
	name string
	fn   func(a, b u256.Int) u256.Int
}

func binaryOps() []binaryOp {
	return []binaryOp{
		{name: "add", fn: func(a, b u256.Int) u256.Int { return a.Add(b) }},
		{name: "sub", fn: func(a, b u256.Int) u256.Int { return a.Sub(b) }},
		{name: "mul", fn: func(a, b u256.Int) u256.Int { return a.Mul(b) }},
		{name: "div", fn: func(a, b u256.Int) u256.Int { return a.Div(b) }},
		{name: "mod", fn: func(a, b u256.Int) u256.Int { return a.Mod(b) }},
		{name: "sdiv", fn: func(a, b u256.Int) u256.Int { return a.SDiv(b) }},
		{name: "smod", fn: func(a, b u256.Int) u256.Int { return a.SMod(b) }},
		{name: "exp", fn: func(a, b u256.Int) u256.Int { return a.Exp(b) }},
		{name: "and", fn: func(a, b u256.Int) u256.Int { return a.And(b) }},
		{name: "or", fn: func(a, b u256.Int) u256.Int { return a.Or(b) }},
		{name: "xor", fn: func(a, b u256.Int) u256.Int { return a.Xor(b) }},
		{name: "lt", fn: func(a, b u256.Int) u256.Int { return fromBool(truth(a.Lt(b))) }},
		{name: "gt", fn: func(a, b u256.Int) u256.Int { return fromBool(truth(a.Gt(b))) }},
		{name: "slt", fn: func(a, b u256.Int) u256.Int { return fromBool(truth(a.Slt(b))) }},
		{name: "sgt", fn: func(a, b u256.Int) u256.Int { return fromBool(truth(a.Sgt(b))) }},
		{name: "eq", fn: func(a, b u256.Int) u256.Int { return fromBool(truth(a.Eq(b))) }},
		{name: "byte", fn: func(a, b u256.Int) u256.Int { return b.Byte(a) }},
		{name: "shl", fn: func(a, b u256.Int) u256.Int { return b.Shl(a) }},
		{name: "shr", fn: func(a, b u256.Int) u256.Int { return b.Shr(a) }},
		{name: "sar", fn: func(a, b u256.Int) u256.Int { return b.Sar(a) }},
		{name: "signextend", fn: func(a, b u256.Int) u256.Int { return b.SignExtend(a) }},
	}
}

type operands struct {
	a u256.Int
	b u256.Int
	m u256.Int
}

func checkBinary(t *testing.T, in operands) {
	t.Helper()
	a, b := in.a, in.b
	ba, bb := toBig(a), toBig(b)
	for _, op := range binaryOps() {
		got := op.fn(a, b)
		want := fromBig(refBinary(op.name, bigPair{a: ba, b: bb}))
		if got != want {
			t.Fatalf("%s(%s, %s) = %s, want %s", op.name, a.Hex(), b.Hex(), got.Hex(), want.Hex())
		}
	}
}

func checkTernary(t *testing.T, in operands) {
	t.Helper()
	a, b, m := in.a, in.b, in.m
	ba, bb, bm := toBig(a), toBig(b), toBig(m)
	wantAdd, wantMul := new(big.Int), new(big.Int)
	if bm.Sign() != 0 {
		wantAdd.Add(ba, bb).Mod(wantAdd, bm)
		wantMul.Mul(ba, bb).Mod(wantMul, bm)
	}
	if got := a.AddFull(b).Mod(m); got != fromBig(wantAdd) {
		t.Fatalf("addmod(%s, %s, %s) = %s, want %s", a.Hex(), b.Hex(), m.Hex(), got.Hex(), wantAdd.Text(16))
	}
	if got := a.MulFull(b).Mod(m); got != fromBig(wantMul) {
		t.Fatalf("mulmod(%s, %s, %s) = %s, want %s", a.Hex(), b.Hex(), m.Hex(), got.Hex(), wantMul.Text(16))
	}
	full := a.MulFull(b)
	prod := new(big.Int).Mul(ba, bb)
	if toBig(full.Low()).Cmp(new(big.Int).And(prod, mask)) != 0 {
		t.Fatalf("mulfull low mismatch")
	}
	if toBig(full.High()).Cmp(new(big.Int).Rsh(prod, 256)) != 0 {
		t.Fatalf("mulfull high mismatch")
	}
}

func edgeValues() []u256.Int {
	limbs := []uint64{0, 1, 2, 1 << 63, 1<<63 - 1, ^uint64(0), ^uint64(0) - 1}
	out := []u256.Int{u256.MinSigned(), u256.Max(), u256.FromUint64(31), u256.FromUint64(32),
		u256.FromUint64(255), u256.FromUint64(256), u256.FromUint64(257), u256.FromUint64(30)}
	for _, l := range limbs {
		for pos := 0; pos < 4; pos++ {
			var x u256.Int
			x[pos] = l
			out = append(out, x, x.Neg(), x.Not())
		}
	}
	return out
}

func randomValue(r *rand.Rand) u256.Int {
	var x u256.Int
	switch r.IntN(6) {
	case 0:
		return u256.FromUint64(r.Uint64N(300))
	case 1:
		x = u256.Int{r.Uint64(), r.Uint64(), r.Uint64(), r.Uint64()}
		return x.Rsh(uint(r.IntN(256)))
	case 2:
		x = u256.Int{r.Uint64(), r.Uint64(), r.Uint64(), r.Uint64()}
		return x.Lsh(uint(r.IntN(256)))
	case 3:
		x = u256.Int{r.Uint64(), r.Uint64(), r.Uint64(), r.Uint64()}
		return x.Rsh(uint(r.IntN(256))).Neg()
	case 4:
		pick := []uint64{0, 1, 1 << 63, 1<<63 - 1, ^uint64(0), ^uint64(0) - 1}
		for i := range x {
			x[i] = pick[r.IntN(len(pick))]
		}
		return x
	default:
		return u256.Int{r.Uint64(), r.Uint64(), r.Uint64(), r.Uint64()}
	}
}

func TestEdgePairs(t *testing.T) {
	edges := edgeValues()
	for _, a := range edges {
		for _, b := range edges {
			checkBinary(t, operands{a: a, b: b})
		}
	}
}

func TestEdgeTriples(t *testing.T) {
	edges := edgeValues()
	r := rand.New(rand.NewPCG(1, 2))
	for _, a := range edges {
		for _, b := range edges {
			checkTernary(t, operands{a: a, b: b, m: edges[r.IntN(len(edges))]})
			checkTernary(t, operands{a: a, b: b, m: randomValue(r)})
		}
	}
}

func TestRandomAgainstBig(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for i := 0; i < 20000; i++ {
		in := operands{a: randomValue(r), b: randomValue(r), m: randomValue(r)}
		checkBinary(t, in)
		checkTernary(t, in)
	}
}

func TestDivisionLimbPatterns(t *testing.T) {
	pick := []uint64{0, 1, 1 << 63, 1<<63 - 1, ^uint64(0)}
	var values []u256.Int
	for i := 0; i < 625; i++ {
		v := i
		var x u256.Int
		for l := 0; l < 4; l++ {
			x[l] = pick[v%5]
			v /= 5
		}
		values = append(values, x)
	}
	for _, a := range values {
		for _, b := range values {
			if b.IsZero() {
				continue
			}
			q, rem := a.Div(b), a.Mod(b)
			wq, wr := new(big.Int).QuoRem(toBig(a), toBig(b), new(big.Int))
			if toBig(q).Cmp(wq) != 0 {
				t.Fatalf("div(%s, %s) = %s, want %s", a.Hex(), b.Hex(), q.Hex(), wq.Text(16))
			}
			if toBig(rem).Cmp(wr) != 0 {
				t.Fatalf("mod(%s, %s) = %s, want %s", a.Hex(), b.Hex(), rem.Hex(), wr.Text(16))
			}
		}
	}
}

func TestWideModPatterns(t *testing.T) {
	pick := []uint64{0, 1, 1 << 63, ^uint64(0)}
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 50000; i++ {
		var w u256.Wide
		for l := range w {
			w[l] = pick[r.IntN(len(pick))]
		}
		var m u256.Int
		for l := range m {
			m[l] = pick[r.IntN(len(pick))]
		}
		if r.IntN(2) == 0 {
			m[3] = r.Uint64()
		}
		wb := new(big.Int).Add(new(big.Int).Lsh(toBig(w.High()), 256), toBig(w.Low()))
		want := new(big.Int)
		if !m.IsZero() {
			want.Mod(wb, toBig(m))
		}
		if got := w.Mod(m); toBig(got).Cmp(want) != 0 {
			t.Fatalf("wide mod %x by %s = %s, want %s", w, m.Hex(), got.Hex(), want.Text(16))
		}
	}
}

func FuzzOps(f *testing.F) {
	f.Add([]byte{1}, []byte{2}, []byte{3})
	f.Add([]byte{0x80}, []byte{0xff, 0xff}, []byte{})
	f.Fuzz(func(t *testing.T, ra, rb, rm []byte) {
		a := u256.FromBytes(ra[:min(len(ra), 32)])
		b := u256.FromBytes(rb[:min(len(rb), 32)])
		m := u256.FromBytes(rm[:min(len(rm), 32)])
		in := operands{a: a, b: b, m: m}
		checkBinary(t, in)
		checkTernary(t, in)
	})
}
