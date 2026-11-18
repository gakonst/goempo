package precompiles

import (
	"encoding/binary"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
)

func TestKeccakMemoExact(t *testing.T) {
	var buf [96]byte
	for n := 0; n <= len(buf); n++ {
		for round := 0; round < 3; round++ {
			binary.LittleEndian.PutUint64(buf[:], uint64(n*7+round))
			got := memoKeccak(buf[:n])
			if want := keccak.Sum256(buf[:n]); got != want {
				t.Fatalf("len %d round %d: memo %x, keccak %x", n, round, got, want)
			}
		}
	}
}

func TestKeccakMemoPrefixes(t *testing.T) {
	// Inputs that differ only in length or in trailing zeros must not collide.
	var buf [64]byte
	for n := 1; n <= 64; n++ {
		if got, want := memoKeccak(buf[:n]), keccak.Sum256(buf[:n]); got != want {
			t.Fatalf("zero input len %d: memo %x, keccak %x", n, got, want)
		}
	}
}

func TestKeccakMemoConcurrent(t *testing.T) {
	const workers = 8
	const keys = 4 * memoEntries
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for w := 0; w < workers; w++ {
		wg.Go(func() {
			var buf [64]byte
			for i := 0; i < keys; i++ {
				binary.LittleEndian.PutUint64(buf[32:], uint64((i*31+w)%(keys/8)))
				if memoKeccak(buf[:]) != keccak.Sum256(buf[:]) {
					errs[w] = errMemoMismatch
					return
				}
			}
		})
	}
	wg.Wait()
	for w := range errs {
		if errs[w] != nil {
			t.Fatalf("worker %d: %v", w, errs[w])
		}
	}
}

type memoError string

func (e memoError) Error() string { return string(e) }

const errMemoMismatch = memoError("memo returned a wrong digest")
