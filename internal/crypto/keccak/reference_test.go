package keccak_test

import (
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"golang.org/x/crypto/sha3"
)

func reference(msg []byte) primitives.Hash {
	ref := sha3.NewLegacyKeccak256()
	ref.Write(msg)
	return primitives.Hash(ref.Sum(nil))
}

func TestBatchMatchesReference(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	b := keccak.NewBatch()
	for trial := 0; trial < 400; trial++ {
		n := r.IntN(20)
		msgs := make([][]byte, n)
		for i := range msgs {
			size := r.IntN(700)
			if r.IntN(4) == 0 {
				size = keccak.BlockSize*(1+r.IntN(3)) - r.IntN(2)
			}
			msgs[i] = make([]byte, size)
			for k := range msgs[i] {
				msgs[i][k] = byte(r.Uint32())
			}
		}
		out := make([]primitives.Hash, n)
		b.Hash(msgs, out)
		for i := range msgs {
			if want := reference(msgs[i]); out[i] != want {
				t.Fatalf("trial %d msg %d (len %d): got %s want %s", trial, i, len(msgs[i]), out[i], want)
			}
		}
	}
}

func TestBatchZeroAlloc(t *testing.T) {
	b := keccak.NewBatch()
	msgs := make([][]byte, 8)
	for i := range msgs {
		msgs[i] = make([]byte, 532)
	}
	out := make([]primitives.Hash, 8)
	if allocs := testing.AllocsPerRun(100, func() { b.Hash(msgs, out) }); allocs != 0 {
		t.Fatalf("allocs per batch: %v", allocs)
	}
}

func benchBatch(b *testing.B, n, size int) {
	kb := keccak.NewBatch()
	msgs := make([][]byte, n)
	for i := range msgs {
		msgs[i] = make([]byte, size)
	}
	out := make([]primitives.Hash, n)
	b.SetBytes(int64(n * size))
	for b.Loop() {
		kb.Hash(msgs, out)
	}
}

func BenchmarkBatch8x532(b *testing.B) { benchBatch(b, 8, 532) }
func BenchmarkBatch2x532(b *testing.B) { benchBatch(b, 2, 532) }
func BenchmarkBatch8x64(b *testing.B)  { benchBatch(b, 8, 64) }

func BenchmarkHasher532(b *testing.B) {
	h := keccak.New()
	in := make([]byte, 532)
	b.SetBytes(532)
	for b.Loop() {
		h.Hash(in)
	}
}
