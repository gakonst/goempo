package trie_test

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
)

type batchStore struct {
	nodes    map[string][]byte
	forks    atomic.Int32
	reads    atomic.Int32
	failures [16]error
}

type batchStoreReader struct{ store *batchStore }

func (s *batchStore) ReadNode(path []byte) ([]byte, error) { return batchStoreReader{s}.ReadNode(path) }
func (r batchStoreReader) ReadNode(path []byte) ([]byte, error) {
	r.store.reads.Add(1)
	if len(path) > 0 && r.store.failures[path[0]] != nil {
		return nil, r.store.failures[path[0]]
	}
	raw, ok := r.store.nodes[string(path)]
	if !ok {
		return nil, errMissing
	}
	return raw, nil
}
func (s *batchStore) ForkReader() trie.NodeReader { s.forks.Add(1); return batchStoreReader{s} }
func (s *batchStore) WriteNode(n trie.StoredNode) { s.nodes[string(n.Path)] = bytes.Clone(n.Encoded) }

func batchKey(i int) primitives.Hash {
	var k primitives.Hash
	k[0] = byte(i * 17)
	k[30] = byte(i >> 8)
	k[31] = byte(i)
	return k
}

func TestApplyBatchModelReopenProof(t *testing.T) {
	for seed := uint64(1); seed <= 4; seed++ {
		rng := rand.New(rand.NewPCG(seed, 101))
		store := &batchStore{nodes: map[string][]byte{}}
		batched := trie.Open(trie.EmptyRoot, store)
		serial := trie.New()
		model := map[string][]byte{}
		for round := 0; round < 12; round++ {
			count := 512
			if round%4 == 1 {
				count = trie.BatchMin - 1
			}
			if round%4 == 2 {
				count = trie.BatchMin
			}
			if round%4 == 3 {
				count = trie.BatchMin + 1
			}
			mutations := make([]trie.Mutation, count)
			for i := range mutations {
				key := batchKey(rng.IntN(768))
				value := bytes.Repeat([]byte{byte(round + i + 1)}, 1+rng.IntN(40))
				if (round+i)%3 == 0 {
					value = nil
				}
				mutations[i] = trie.Mutation{Key: key, Value: value}
				if err := serial.Set(key[:], value); err != nil {
					t.Fatal(err)
				}
				if len(value) == 0 {
					delete(model, string(key[:]))
				} else {
					model[string(key[:])] = bytes.Clone(value)
				}
			}
			if err := batched.ApplyBatch(mutations); err != nil {
				t.Fatalf("seed%d round%d %v", seed, round, err)
			}
			root := batched.Commit(store)
			if root != serial.Hash() || root != recycleRoot(model) {
				t.Fatalf("seed%d round%d root mismatch", seed, round)
			}
			recycleAccounting(t, batched)
			for i := 0; i < 16; i++ {
				key := batchKey(i * 37)
				proof, err := batched.Prove(key[:])
				if err != nil {
					t.Fatal(err)
				}
				value, err := trie.VerifyProof(root, key[:], proof)
				if err != nil || !bytes.Equal(value, model[string(key[:])]) {
					t.Fatal("proof mismatch", err)
				}
			}
			if round%2 == 0 {
				batched = trie.Open(root, store)
			} else {
				batched.Evict(1)
			}
		}
		if store.forks.Load() == 0 {
			t.Fatal("parallel path not exercised")
		}
	}
}

func TestApplyBatchCollapseAndFallback(t *testing.T) {
	for _, remaining := range []int{0, 1, 2, 16} {
		for _, prefix := range []byte{0, 1} {
			tree := trie.New()
			serial := trie.New()
			mutations := make([]trie.Mutation, 512)
			for i := range mutations {
				key := batchKey(i)
				if prefix == 1 {
					key[0] = 0x11
				}
				mutations[i] = trie.Mutation{Key: key, Value: []byte{0x81, byte(i)}}
			}
			for _, m := range mutations {
				tree.Update(m.Key[:], m.Value)
				serial.Update(m.Key[:], m.Value)
			}
			for i := range mutations {
				if i >= remaining {
					mutations[i].Value = nil
				}
				serial.Update(mutations[i].Key[:], mutations[i].Value)
			}
			if err := tree.ApplyBatch(mutations); err != nil {
				t.Fatal(err)
			}
			if tree.Hash() != serial.Hash() {
				t.Fatalf("remaining%d prefix%d", remaining, prefix)
			}
			recycleAccounting(t, tree)
		}
	}
	// All duplicate mutations retain caller order within one bucket.
	tree := trie.New()
	serial := trie.New()
	for i := 0; i < 32; i++ {
		key := batchKey(i)
		tree.Update(key[:], []byte{1})
		serial.Update(key[:], []byte{1})
	}
	ops := make([]trie.Mutation, 256)
	for i := range ops {
		ops[i] = trie.Mutation{Key: batchKey(7), Value: []byte{byte(i)}}
		serial.Update(ops[i].Key[:], ops[i].Value)
	}
	if err := tree.ApplyBatch(ops); err != nil || tree.Hash() != serial.Hash() {
		t.Fatal("duplicate order", err)
	}
}

func TestApplyBatchDeterministicErrors(t *testing.T) {
	store := &batchStore{nodes: map[string][]byte{}}
	tree := trie.Open(trie.EmptyRoot, store)
	ops := make([]trie.Mutation, 512)
	for i := range ops {
		ops[i] = trie.Mutation{Key: batchKey(i), Value: bytes.Repeat([]byte{byte(i + 1)}, 48)}
		tree.Update(ops[i].Key[:], ops[i].Value)
	}
	root := tree.Commit(store)
	low := errors.New("low nibble")
	high := errors.New("high nibble")
	store.failures[2] = low
	store.failures[13] = high
	for attempt := 0; attempt < 20; attempt++ {
		opened := trie.Open(root, store)
		if err := opened.ApplyBatch(ops); !errors.Is(err, low) {
			t.Fatalf("error %v", err)
		}
	}
	store.failures = [16]error{}
	for _, corruption := range []string{"hash", "missing"} {
		saved := store.nodes[string([]byte{3})]
		if saved == nil {
			t.Fatal("fixture lacks child")
		}
		if corruption == "hash" {
			bad := bytes.Clone(saved)
			bad[len(bad)-1] ^= 1
			store.nodes[string([]byte{3})] = bad
		} else {
			delete(store.nodes, string([]byte{3}))
		}
		err := trie.Open(root, store).ApplyBatch(ops)
		if corruption == "hash" && !errors.Is(err, trie.ErrNodeHash) {
			t.Fatal(err)
		}
		if corruption == "missing" && !errors.Is(err, errMissing) {
			t.Fatal(err)
		}
		store.nodes[string([]byte{3})] = saved
	}
}
