package keccak_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
)

func TestKnownVectors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{name: "abc", in: "abc", want: "0x4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
		{name: "rlp empty list", in: "\xc0", want: "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347"},
		{name: "rlp empty string", in: "\x80", want: "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := keccak.Sum256([]byte(test.in))
			if got.Hex() != test.want {
				t.Fatalf("got %s want %s", got, test.want)
			}
		})
	}
}

func TestHasherReuseMatchesSum256(t *testing.T) {
	h := keccak.New()
	in := make([]byte, 1000)
	for i := 0; i < len(in); i++ {
		in[i] = byte(i * 7)
		want := keccak.Sum256(in[:i])
		if got := h.Hash(in[:i]); got != want {
			t.Fatalf("length %d: got %s want %s", i, got, want)
		}
	}
	h.Reset()
	h.Write(in[:300])
	h.Write(in[300:])
	if h.Sum() != keccak.Sum256(in) {
		t.Fatal("split writes")
	}
	if keccak.Sum256Concat(in[:5], in[5:]) != keccak.Sum256(in) {
		t.Fatal("concat")
	}
}

func TestHasherZeroAlloc(t *testing.T) {
	h := keccak.New()
	in := make([]byte, 500)
	var out primitives.Hash
	allocs := testing.AllocsPerRun(100, func() {
		h.Reset()
		h.Write(in)
		h.SumInto(&out)
	})
	if allocs != 0 {
		t.Fatalf("allocs per hash: %v", allocs)
	}
}

func BenchmarkHasher32(b *testing.B) {
	h := keccak.New()
	in := make([]byte, 32)
	var out primitives.Hash
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		h.Reset()
		h.Write(in)
		h.SumInto(&out)
	}
}
