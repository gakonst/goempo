package trie_test

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type recordedWrite struct {
	path    string
	encoded string
}

type recorder struct {
	*memStore
	log []recordedWrite
}

func (r *recorder) WriteNode(n trie.StoredNode) {
	r.log = append(r.log, recordedWrite{path: string(n.Path), encoded: string(n.Encoded)})
	r.memStore.WriteNode(n)
}

// TestParallelCommitMatchesSerial commits the same updates once in one large
// batch (hashed on workers) and once hashed every few updates (always on the
// caller), over several rounds of a stored trie, and requires the same roots
// and the same node writes in the same order.
func TestParallelCommitMatchesSerial(t *testing.T) {
	for _, batch := range []int{300, 5000, 20000} {
		r := rand.New(rand.NewPCG(uint64(batch), 9))
		wide, narrow := &recorder{memStore: newMemStore()}, &recorder{memStore: newMemStore()}
		var wideRoot, narrowRoot primitives.Hash
		wideRoot, narrowRoot = trie.EmptyRoot, trie.EmptyRoot
		keys := make([][]byte, 0, 3*batch)
		for round := 0; round < 3; round++ {
			pt := trie.Open(wideRoot, wide)
			st := trie.Open(narrowRoot, narrow)
			for i := 0; i < batch; i++ {
				var key []byte
				if len(keys) > 0 && r.IntN(3) == 0 {
					key = keys[r.IntN(len(keys))]
				} else {
					key = hashedKey(r)
					keys = append(keys, key)
				}
				value := make([]byte, 1+r.IntN(80))
				for k := range value {
					value[k] = byte(r.Uint32())
				}
				if r.IntN(10) == 0 {
					value = nil
				}
				if err := pt.Set(key, value); err != nil {
					t.Fatal(err)
				}
				if err := st.Set(key, value); err != nil {
					t.Fatal(err)
				}
				if i%50 == 49 {
					st.Hash()
				}
			}
			wide.log, narrow.log = wide.log[:0], narrow.log[:0]
			wideRoot, narrowRoot = pt.Commit(wide), st.Commit(narrow)
			if wideRoot != narrowRoot {
				t.Fatalf("batch %d round %d: root %s, serial %s", batch, round, wideRoot, narrowRoot)
			}
			if len(wide.log) != len(narrow.log) {
				t.Fatalf("batch %d round %d: %d writes, serial %d", batch, round, len(wide.log), len(narrow.log))
			}
			for i := range wide.log {
				if wide.log[i] != narrow.log[i] {
					t.Fatalf("batch %d round %d: write %d differs", batch, round, i)
				}
			}
		}
		fresh := trie.New()
		check := trie.Open(wideRoot, wide)
		seen := map[string]bool{}
		for i := len(keys) - 1; i >= 0; i-- {
			if seen[string(keys[i])] {
				continue
			}
			seen[string(keys[i])] = true
			v, err := check.Find(keys[i])
			if err != nil {
				t.Fatal(err)
			}
			if v != nil {
				fresh.Update(keys[i], bytes.Clone(v))
			}
		}
		if fresh.Hash() != wideRoot {
			t.Fatalf("batch %d: rebuilt root %s, committed %s", batch, fresh.Hash(), wideRoot)
		}
	}
}

func TestOrderedRootLarge(t *testing.T) {
	for _, n := range []int{1, 2, 127, 128, 129, 300, 5000} {
		items := make([][]byte, n)
		model := trie.New()
		for i := range items {
			items[i] = bytes.Repeat([]byte{byte(i), byte(i >> 8)}, 1+i%300)
			model.Update(rlpIndex(i), items[i])
		}
		if got, want := trie.OrderedRoot(items), model.Hash(); got != want {
			t.Fatalf("n=%d: got %s want %s", n, got, want)
		}
	}
}

func rlpIndex(i int) []byte {
	switch {
	case i == 0:
		return []byte{0x80}
	case i < 0x80:
		return []byte{byte(i)}
	case i < 0x100:
		return []byte{0x81, byte(i)}
	default:
		return []byte{0x82, byte(i >> 8), byte(i)}
	}
}

func BenchmarkCommitStored(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	store := newMemStore()
	tr := trie.Open(trie.EmptyRoot, store)
	for i := 0; i < 200000; i++ {
		tr.Update(hashedKey(r), []byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31})
	}
	tr.Commit(store)
	keys := make([][]byte, 2000)
	for i := range keys {
		keys[i] = hashedKey(r)
	}
	round := 0
	b.ResetTimer()
	for b.Loop() {
		round++
		for i := range keys {
			tr.Update(keys[i], []byte{byte(i), byte(round), 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9})
		}
		tr.Commit(store)
	}
}
