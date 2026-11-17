package bn254_test

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/bn254"
)

type gethVector struct {
	Input    string
	Expected string
	Name     string
	Gas      uint64
}

func loadGeth(t *testing.T, name string) []gethVector {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []gethVector
	if err := json.NewDecoder(zr).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("%s: no vectors", name)
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

func TestAddVectors(t *testing.T) {
	for _, v := range loadGeth(t, "bn256Add") {
		out, err := bn254.Add(decode(t, v.Input))
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		if hex.EncodeToString(out[:]) != v.Expected {
			t.Fatalf("%s: got %x want %s", v.Name, out, v.Expected)
		}
		if v.Gas != bn254.AddGas {
			t.Fatalf("%s: gas %d", v.Name, v.Gas)
		}
	}
}

func TestScalarMulVectors(t *testing.T) {
	for _, v := range loadGeth(t, "bn256ScalarMul") {
		out, err := bn254.ScalarMul(decode(t, v.Input))
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		if hex.EncodeToString(out[:]) != v.Expected {
			t.Fatalf("%s: got %x want %s", v.Name, out, v.Expected)
		}
	}
}

func TestPairingVectors(t *testing.T) {
	for _, v := range loadGeth(t, "bn256Pairing") {
		in := decode(t, v.Input)
		ok, err := bn254.PairingCheck(in)
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		want := v.Expected[len(v.Expected)-1] == '1'
		if ok != want {
			t.Fatalf("%s: got %v want %v", v.Name, ok, want)
		}
		if bn254.PairingGas(len(in)) != v.Gas {
			t.Fatalf("%s: gas %d want %d", v.Name, bn254.PairingGas(len(in)), v.Gas)
		}
	}
}

func TestInvalidInputs(t *testing.T) {
	p := make([]byte, 64)
	p[31] = 1
	p[63] = 3
	if _, err := bn254.Add(p); err == nil {
		t.Fatal("(1, 3) is not on the curve")
	}
	modulus := decode(t, "30644e72e131a029b85045b68181585d97816a916871ca8d3c208c16d87cfd47")
	q := append(append([]byte{}, modulus...), make([]byte, 32)...)
	if _, err := bn254.ScalarMul(q); err == nil {
		t.Fatal("x = p accepted")
	}
	if _, err := bn254.PairingCheck(make([]byte, 191)); err != bn254.ErrPairingLength {
		t.Fatal("bad length accepted")
	}
	ok, err := bn254.PairingCheck(nil)
	if err != nil || !ok {
		t.Fatal("empty pairing must succeed")
	}
	g := make([]byte, 64)
	g[31], g[63] = 1, 2
	out, err := bn254.Add(g)
	if err != nil || out[31] != 1 || out[63] != 2 {
		t.Fatalf("short input must be right padded: %x %v", out, err)
	}
}

func TestG2SubgroupCheck(t *testing.T) {
	data, err := os.ReadFile("testdata/g2_subgroup.txt")
	if err != nil {
		t.Fatal(err)
	}
	g1 := make([]byte, 64)
	g1[31], g1[63] = 1, 2
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 12 {
		t.Fatalf("%d lines", len(lines))
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		in := append(append([]byte{}, g1...), decode(t, fields[1])...)
		_, err := bn254.PairingCheck(in)
		if fields[0] == "in" && err != nil {
			t.Fatalf("subgroup point rejected: %v", err)
		}
		if fields[0] == "out" && err != bn254.ErrInvalidPoint {
			t.Fatalf("point outside G2 accepted: %v", err)
		}
	}
}

func BenchmarkPairing2(b *testing.B) {
	vs := loadGeth(&testing.T{}, "bn256Pairing")
	var in []byte
	for _, v := range vs {
		if len(v.Input) == 2*2*bn254.PairLength {
			decoded, err := hex.DecodeString(v.Input)
			if err != nil {
				b.Fatal(err)
			}
			in = decoded
			break
		}
	}
	for i := 0; i < b.N; i++ {
		if _, err := bn254.PairingCheck(in); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScalarMul(b *testing.B) {
	in, err := hex.DecodeString("2bd3e6d0f3b142924f5ca7b49ce5b9d54c4703d7ae5648e61d02268b1a0a9fb721611ce0a6af85915e2f1d70300909ce2e49dfad4a4619c8390cae66cefdb20400000000000000000000000000000000000000000000000011138ce750fa15c2")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, err := bn254.ScalarMul(in); err != nil {
			b.Fatal(err)
		}
	}
}
