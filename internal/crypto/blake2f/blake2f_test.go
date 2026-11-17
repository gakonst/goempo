package blake2f_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/blake2f"
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
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var out []gethVector
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) < 4 {
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

func TestEIP152Vectors(t *testing.T) {
	for _, v := range load(t, "blake2F.json") {
		in := decode(t, v.Input)
		gas, err := blake2f.Gas(in)
		if err != nil || gas != v.Gas {
			t.Fatalf("%s: gas %d %v want %d", v.Name, gas, err, v.Gas)
		}
		out, err := blake2f.Compress(in)
		if err != nil {
			t.Fatalf("%s: %v", v.Name, err)
		}
		if hex.EncodeToString(out[:]) != v.Expected {
			t.Fatalf("%s: got %x", v.Name, out)
		}
	}
}

func TestFailures(t *testing.T) {
	for _, v := range load(t, "fail-blake2f.json") {
		_, err := blake2f.Compress(decode(t, v.Input))
		if err == nil {
			t.Fatalf("%s: accepted", v.Name)
		}
	}
}
