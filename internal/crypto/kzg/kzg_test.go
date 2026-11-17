package kzg_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/kzg"
)

func decode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGethVector(t *testing.T) {
	data, err := os.ReadFile("testdata/pointEvaluation.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct {
		Input    string
		Expected string
		Gas      uint64
	}
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		out, err := kzg.PointEvaluation(decode(t, v.Input))
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(out[:]) != v.Expected || v.Gas != kzg.Gas {
			t.Fatalf("got %x", out)
		}
	}
}

func TestEESTVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/eest_point_evaluation.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	valid := 0
	for _, line := range lines {
		f := strings.Fields(line)
		want := f[0] == "1"
		out, err := kzg.PointEvaluation(decode(t, f[2]))
		if (err == nil) != want {
			t.Fatalf("input %s: err %v, want success %v", f[2], err, want)
		}
		if want {
			valid++
			if out != kzg.Output() {
				t.Fatalf("output %x", out)
			}
		}
	}
	if len(lines) < 100 || valid < 40 {
		t.Fatalf("%d vectors, %d valid", len(lines), valid)
	}
}
