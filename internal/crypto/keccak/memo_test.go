package keccak

import (
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
)

func direct(b []byte) primitives.Hash {
	return New().hashDirect(b)
}

func TestMemoMatchesDirectHitOrMiss(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	inputs := make([][]byte, 512)
	for i := range inputs {
		inputs[i] = make([]byte, i%(memoInputMax+20))
		for j := range inputs[i] {
			inputs[i][j] = byte(rng.Uint32())
		}
	}
	h := New()
	for round := 0; round < 4; round++ {
		for _, in := range inputs {
			want := direct(in)
			if got := Sum256(in); got != want {
				t.Fatalf("Sum256 len %d round %d", len(in), round)
			}
			if got := h.Hash(in); got != want {
				t.Fatalf("Hash len %d round %d", len(in), round)
			}
			if len(in) > 1 {
				if got := Sum256Concat(in[:1], in[1:]); got != want {
					t.Fatalf("Sum256Concat len %d round %d", len(in), round)
				}
			}
		}
	}
}

func TestMemoNearIdenticalInputs(t *testing.T) {
	var a, b [32]byte
	for i := range a {
		a[i], b[i] = 1, 1
	}
	b[31] = 2
	for round := 0; round < 3; round++ {
		for _, in := range [][]byte{a[:], b[:], a[:31], b[:31], a[:1]} {
			if Sum256(in) != direct(in) {
				t.Fatalf("len %d round %d", len(in), round)
			}
		}
	}
}

// Two inputs forced into the same slot evict each other and stay exact.
func TestMemoSlotEviction(t *testing.T) {
	m := new(memo)
	x := []byte("first input")
	var y []byte
	for i := 0; ; i++ {
		assertLess(t, i, 1<<24)
		cand := []byte{byte(i), byte(i >> 8), byte(i >> 16), 9}
		if m.entry(cand) == m.entry(x) {
			y = cand
			break
		}
	}
	e := m.entry(x)
	store(e, x)
	if e.holds(y) {
		t.Fatal("an entry holds only its own input")
	}
	store(e, y)
	if e.holds(x) || !e.holds(y) || e.digest != direct(y) {
		t.Fatal("eviction")
	}
}

func store(e *memoEntry, b []byte) {
	e.length = uint8(len(b))
	copy(e.key[:], b)
	e.digest = direct(b)
}

func assertLess(t *testing.T, a, b int) {
	t.Helper()
	if a >= b {
		t.Fatalf("%d >= %d", a, b)
	}
}

func TestMemoConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, 1))
			var buf [memoInputMax]byte
			for i := 0; i < 20000; i++ {
				n := 1 + rng.IntN(memoInputMax)
				for j := 0; j < n; j++ {
					buf[j] = byte(rng.IntN(4))
				}
				if Sum256(buf[:n]) != direct(buf[:n]) {
					errs <- "mismatch"
					return
				}
			}
		}(uint64(g))
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}

func TestMemoZeroAlloc(t *testing.T) {
	if raceEnabled {
		t.Skip("sync.Pool drops items under the race detector")
	}
	in := make([]byte, 64)
	key := make([]byte, 32)
	Sum256(in)
	allocs := testing.AllocsPerRun(1000, func() {
		Sum256(in)
		Sum256Concat(key, in[:32])
	})
	if allocs != 0 {
		t.Fatalf("allocs per memo hash: %v", allocs)
	}
}

func BenchmarkSum256Concat64Hit(b *testing.B) {
	key := make([]byte, 32)
	slot := make([]byte, 32)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		slot[31] = byte(i & 15)
		Sum256Concat(key, slot)
	}
}

func BenchmarkHash64Miss(b *testing.B) {
	h := New()
	in := make([]byte, 64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		in[0], in[1], in[2] = byte(i), byte(i>>8), byte(i>>16)
		h.Hash(in)
	}
}

func BenchmarkHash64Direct(b *testing.B) {
	h := New()
	in := make([]byte, 64)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		in[0], in[1], in[2] = byte(i), byte(i>>8), byte(i>>16)
		h.hashDirect(in)
	}
}
