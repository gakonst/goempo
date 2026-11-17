package modexp_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/modexp"
)

type gethVector struct {
	Input    string
	Expected string
	Name     string
	Gas      uint64
}

func load(t *testing.T, name string) []gethVector {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var out []gethVector
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) < 10 {
		t.Fatalf("%s: %d vectors", name, len(out))
	}
	return out
}

func decode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type ruleCase struct {
	file  string
	rules modexp.Rules
}

func TestGethVectors(t *testing.T) {
	cases := []ruleCase{{file: "modexp_eip2565", rules: modexp.Prague}, {file: "modexp_eip7883", rules: modexp.Osaka}}
	for _, c := range cases {
		for _, v := range load(t, c.file) {
			in := decode(t, v.Input)
			gas, err := modexp.Gas(in, c.rules)
			if err != nil {
				t.Fatalf("%s %s: gas error %v", c.file, v.Name, err)
			}
			if gas != v.Gas {
				t.Fatalf("%s %s: gas %d want %d", c.file, v.Name, gas, v.Gas)
			}
			out, err := modexp.Run(in, c.rules)
			if err != nil {
				t.Fatalf("%s %s: %v", c.file, v.Name, err)
			}
			if hex.EncodeToString(out) != v.Expected {
				t.Fatalf("%s %s: got %x want %s", c.file, v.Name, out, v.Expected)
			}
		}
	}
}

func header(lengths [3]uint64) []byte {
	in := make([]byte, 96)
	for i, l := range lengths {
		new(big.Int).SetUint64(l).FillBytes(in[i*32 : (i+1)*32])
	}
	return in
}

func TestLengthBounds(t *testing.T) {
	long := header([3]uint64{1025, 1, 1})
	if _, err := modexp.Gas(long, modexp.Osaka); err != modexp.ErrLengthTooLarge {
		t.Fatalf("osaka base 1025: %v", err)
	}
	if _, err := modexp.Run(long, modexp.Osaka); err != modexp.ErrLengthTooLarge {
		t.Fatalf("osaka run base 1025: %v", err)
	}
	gas, err := modexp.Gas(long, modexp.Prague)
	if err != nil || gas != 129*129/3 {
		t.Fatalf("prague gas for a 1025-byte base: %d %v", gas, err)
	}
	out, err := modexp.Run(long, modexp.Prague)
	if err != nil || len(out) != 1 || out[0] != 0 {
		t.Fatalf("prague run with input ending in the base: %x %v", out, err)
	}
	exp := header([3]uint64{0, 1025, 0})
	if _, err := modexp.Gas(exp, modexp.Osaka); err != modexp.ErrLengthTooLarge {
		t.Fatal("osaka exponent 1025 accepted")
	}
	huge := header([3]uint64{0, 0, 0})
	huge[32] = 1
	g, err := modexp.Gas(huge, modexp.Prague)
	if err != nil || g != modexp.PragueGasMin {
		t.Fatalf("prague exponent length above 2^64 with empty base and modulus: %d %v", g, err)
	}
	if out, err := modexp.Run(huge, modexp.Prague); err != nil || len(out) != 0 {
		t.Fatal("empty base and modulus must return empty")
	}
	bad := header([3]uint64{0, 0, 0})
	bad[0] = 1
	if _, err := modexp.Gas(bad, modexp.Prague); err != modexp.ErrLengthTooLarge {
		t.Fatal("prague base length above 2^64 accepted")
	}
}

func TestSemantics(t *testing.T) {
	in := append(header([3]uint64{1, 1, 1}), 3, 5, 7)
	out, err := modexp.Run(in, modexp.Osaka)
	if err != nil || len(out) != 1 || out[0] != 5 {
		t.Fatalf("3^5 mod 7 = %x %v", out, err)
	}
	one := append(header([3]uint64{1, 1, 2}), 3, 5, 0, 1)
	out, err = modexp.Run(one, modexp.Osaka)
	if err != nil || len(out) != 2 || out[0]|out[1] != 0 {
		t.Fatalf("mod 1 = %x %v", out, err)
	}
	zeroExp := append(header([3]uint64{1, 0, 1}), 3, 7)
	out, err = modexp.Run(zeroExp, modexp.Osaka)
	if err != nil || out[0] != 1 {
		t.Fatalf("x^0 mod 7 = %x %v", out, err)
	}
	short := append(header([3]uint64{1, 1, 1}), 3, 5)
	out, err = modexp.Run(short, modexp.Osaka)
	if err != nil || out[0] != 0 {
		t.Fatalf("missing modulus = %x %v", out, err)
	}
	gas, err := modexp.Gas(nil, modexp.Osaka)
	if err != nil || gas != modexp.OsakaGasMin {
		t.Fatalf("empty input gas %d %v", gas, err)
	}
}

func FuzzAgainstBig(f *testing.F) {
	f.Add([]byte{3}, []byte{5}, []byte{7})
	f.Fuzz(func(t *testing.T, b, e, m []byte) {
		b, e, m = b[:min(len(b), 64)], e[:min(len(e), 64)], m[:min(len(m), 64)]
		in := header([3]uint64{uint64(len(b)), uint64(len(e)), uint64(len(m))})
		in = append(append(append(in, b...), e...), m...)
		out, err := modexp.Run(in, modexp.Osaka)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 && len(m) == 0 {
			return
		}
		mod := new(big.Int).SetBytes(m)
		want := new(big.Int)
		if mod.Sign() != 0 {
			want.Exp(new(big.Int).SetBytes(b), new(big.Int).SetBytes(e), mod)
		}
		if new(big.Int).SetBytes(out).Cmp(want) != 0 || len(out) != len(m) {
			t.Fatalf("got %x want %x", out, want)
		}
	})
}
