package trie_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
)

type memStore struct {
	nodes  map[string][]byte
	reads  int
	writes int
}

func newMemStore() *memStore { return &memStore{nodes: map[string][]byte{}} }

var errMissing = errors.New("missing node")

func (m *memStore) ReadNode(path []byte) ([]byte, error) {
	m.reads++
	enc, ok := m.nodes[string(path)]
	if !ok {
		return nil, errMissing
	}
	return enc, nil
}

func (m *memStore) WriteNode(n trie.StoredNode) {
	m.writes++
	m.nodes[string(n.Path)] = n.Encoded
}

func hashedKey(r *rand.Rand) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(r.Uint32())
	}
	if r.IntN(3) == 0 {
		k[1] = 0
		k[2] = 0
	}
	return k
}

func TestStoredTrieMatchesRebuild(t *testing.T) {
	for seed := uint64(0); seed < 30; seed++ {
		r := rand.New(rand.NewPCG(seed, 11))
		store := newMemStore()
		tr := trie.Open(trie.EmptyRoot, store)
		model := map[string][]byte{}
		var keys [][]byte
		for block := 0; block < 40; block++ {
			for op := 0; op < 1+r.IntN(30); op++ {
				var k []byte
				if len(keys) > 0 && r.IntN(2) == 0 {
					k = keys[r.IntN(len(keys))]
				} else {
					k = hashedKey(r)
					keys = append(keys, k)
				}
				if r.IntN(3) == 0 {
					if err := tr.Unset(k); err != nil {
						t.Fatal(err)
					}
					delete(model, string(k))
					continue
				}
				v := randomValue(r)
				if err := tr.Set(k, v); err != nil {
					t.Fatal(err)
				}
				model[string(k)] = v
			}
			root := tr.Commit(store)
			if want := rebuild(model).Hash(); root != want {
				t.Fatalf("seed %d block %d: root %s want %s", seed, block, root, want)
			}
			switch r.IntN(3) {
			case 0:
				tr = trie.Open(root, store)
			case 1:
				tr.Evict(r.IntN(3))
			}
		}
		fresh := trie.Open(tr.Commit(store), store)
		for _, k := range sortedKeys(model) {
			got, err := fresh.Find([]byte(k))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, model[k]) {
				t.Fatalf("seed %d: find %x", seed, k)
			}
		}
		absent, err := fresh.Find(make([]byte, 32))
		if err != nil || absent != nil {
			t.Fatalf("absent key: %x %v", absent, err)
		}
	}
}

func TestStoredTrieDetectsCorruptNode(t *testing.T) {
	store := newMemStore()
	tr := trie.Open(trie.EmptyRoot, store)
	r := rand.New(rand.NewPCG(3, 4))
	for range 200 {
		if err := tr.Set(hashedKey(r), randomValue(r)); err != nil {
			t.Fatal(err)
		}
	}
	root := tr.Commit(store)
	for _, path := range sortedKeys(store.nodes) {
		enc := store.nodes[path]
		if len(path) == 1 {
			bad := bytes.Clone(enc)
			bad[len(bad)-1] ^= 1
			store.nodes[path] = bad
		}
	}
	reopened := trie.Open(root, store)
	failures := 0
	r = rand.New(rand.NewPCG(3, 4))
	for range 200 {
		k := hashedKey(r)
		randomValue(r)
		if _, err := reopened.Find(k); errors.Is(err, trie.ErrNodeHash) {
			failures++
		}
	}
	if failures == 0 {
		t.Fatal("corrupted nodes were not detected")
	}
}

func TestCommitWritesOnlyChangedPaths(t *testing.T) {
	store := newMemStore()
	tr := trie.Open(trie.EmptyRoot, store)
	r := rand.New(rand.NewPCG(9, 9))
	var keys [][]byte
	for range 5000 {
		k := hashedKey(r)
		keys = append(keys, k)
		if err := tr.Set(k, randomValue(r)); err != nil {
			t.Fatal(err)
		}
	}
	root := tr.Commit(store)
	tr = trie.Open(root, store)
	store.reads, store.writes = 0, 0
	if err := tr.Set(keys[17], []byte("changed")); err != nil {
		t.Fatal(err)
	}
	tr.Commit(store)
	if store.writes > 6 || store.reads > 6 {
		t.Fatalf("one update read %d and wrote %d nodes", store.reads, store.writes)
	}
	if tr.Nodes() > 100 {
		t.Fatalf("lazy trie loaded %d nodes", tr.Nodes())
	}
	var empty primitives.Hash
	if root == empty {
		t.Fatal("zero root")
	}
}
