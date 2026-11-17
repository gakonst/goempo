package bls12381_test

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
)

type gethVector struct {
	Input         string
	Expected      string
	ExpectedError string
	Name          string
	Gas           uint64
}

func load(t *testing.T, name string) []gethVector {
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

type precompile struct {
	file string
	run  func(in []byte) ([]byte, error)
	gas  func(in []byte) uint64
}

func fixed(g uint64) func(in []byte) uint64 { return func(in []byte) uint64 { return g } }

func precompiles() []precompile {
	return []precompile{
		{file: "blsG1Add", gas: fixed(bls12381.G1AddGas), run: func(in []byte) ([]byte, error) { o, err := bls12381.G1Add(in); return o[:], err }},
		{file: "blsG2Add", gas: fixed(bls12381.G2AddGas), run: func(in []byte) ([]byte, error) { o, err := bls12381.G2Add(in); return o[:], err }},
		{file: "blsG1Mul", gas: func(in []byte) uint64 { return bls12381.G1MSMGas(len(in)) }, run: func(in []byte) ([]byte, error) { o, err := bls12381.G1MSM(in); return o[:], err }},
		{file: "blsG1MultiExp", gas: func(in []byte) uint64 { return bls12381.G1MSMGas(len(in)) }, run: func(in []byte) ([]byte, error) { o, err := bls12381.G1MSM(in); return o[:], err }},
		{file: "blsG2MultiExp", gas: func(in []byte) uint64 { return bls12381.G2MSMGas(len(in)) }, run: func(in []byte) ([]byte, error) { o, err := bls12381.G2MSM(in); return o[:], err }},
		{file: "blsPairing", gas: func(in []byte) uint64 { return bls12381.PairingGas(len(in)) }, run: func(in []byte) ([]byte, error) { o, err := bls12381.PairingCheck(in); return o[:], err }},
		{file: "blsMapG1", gas: fixed(bls12381.MapG1Gas), run: func(in []byte) ([]byte, error) { o, err := bls12381.MapFpToG1(in); return o[:], err }},
		{file: "blsMapG2", gas: fixed(bls12381.MapG2Gas), run: func(in []byte) ([]byte, error) { o, err := bls12381.MapFp2ToG2(in); return o[:], err }},
	}
}

func TestGethVectors(t *testing.T) {
	for _, pc := range precompiles() {
		for _, v := range load(t, pc.file) {
			in := decode(t, v.Input)
			out, err := pc.run(in)
			if err != nil {
				t.Fatalf("%s %s: %v", pc.file, v.Name, err)
			}
			if hex.EncodeToString(out) != v.Expected {
				t.Fatalf("%s %s: got %x want %s", pc.file, v.Name, out, v.Expected)
			}
			if g := pc.gas(in); g != v.Gas {
				t.Fatalf("%s %s: gas %d want %d", pc.file, v.Name, g, v.Gas)
			}
		}
	}
}

func TestGethFailures(t *testing.T) {
	failing := map[string]string{
		"blsG1Add": "fail-blsG1Add", "blsG2Add": "fail-blsG2Add", "blsG1MultiExp": "fail-blsG1MultiExp",
		"blsG2MultiExp": "fail-blsG2MultiExp", "blsPairing": "fail-blsPairing", "blsMapG1": "fail-blsMapG1", "blsMapG2": "fail-blsMapG2",
	}
	for _, pc := range precompiles() {
		name, ok := failing[pc.file]
		if !ok {
			continue
		}
		for _, v := range load(t, name) {
			if _, err := pc.run(decode(t, v.Input)); err == nil {
				t.Fatalf("%s %s: accepted, want %s", name, v.Name, v.ExpectedError)
			}
		}
	}
}
