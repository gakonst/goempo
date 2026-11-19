package trie_test

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"runtime"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type commitLane struct {
	tree    *trie.Trie
	store   *recycleStore
	workers int
	root    primitives.Hash
}

func TestParallelCommitIndependentModel(t *testing.T) {
	for seed := uint64(1); seed <= 3; seed++ {
		r := rand.New(rand.NewPCG(seed, 71))
		keys := recycleKeys(1024)
		for i := range keys {
			if i%7 == 0 {
				keys[i] = append([]byte{0, 0, 0}, keys[i]...)
			}
		}
		lanes := make([]commitLane, 4)
		for i, workers := range []int{0, 1, 4, 8} {
			store := &recycleStore{memStore: newMemStore()}
			lanes[i] = commitLane{tree: trie.Open(trie.EmptyRoot, store), store: store, workers: workers}
		}
		model := map[string][]byte{}
		for round := 0; round < 9; round++ {
			operations := 192
			if round == 0 || round == 5 {
				operations = len(keys)
			}
			if round == 2 {
				operations = 3
			}
			for op := 0; op < operations; op++ {
				index := r.IntN(len(keys))
				if round == 0 || round == 4 || round == 5 {
					index = op % len(keys)
				}
				k := keys[index]
				remove := (round+op)%4 == 0 || round == 4
				v := recycleValue(round + op)
				for i := range lanes {
					var err error
					if remove {
						err = lanes[i].tree.Unset(k)
					} else {
						err = lanes[i].tree.Set(k, v)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if remove {
					delete(model, string(k))
				} else {
					model[string(k)] = bytes.Clone(v)
				}
			}
			want := recycleRoot(model)
			var expectedWrites []recycleWrite
			for i := range lanes {
				lane := &lanes[i]
				beforeReads := lane.store.reads
				lane.store.retained = nil
				if lane.workers == 0 {
					lane.tree.Hash()
					lane.root = lane.tree.Commit(lane.store)
				} else {
					prior := runtime.GOMAXPROCS(lane.workers)
					lane.root = lane.tree.Commit(lane.store)
					runtime.GOMAXPROCS(prior)
				}
				if lane.store.reads != beforeReads {
					t.Fatal("compute/commit unexpectedly called reader")
				}
				if lane.root != want {
					t.Fatalf("seed%d round%d workers%d root%s want%s", seed, round, lane.workers, lane.root, want)
				}
				if i == 0 {
					expectedWrites = lane.store.retained
				} else if !reflect.DeepEqual(lane.store.retained, expectedWrites) {
					t.Fatalf("workers%d changed serial writer order/bytes", lane.workers)
				}
				if !reflect.DeepEqual(lane.store.nodes, lanes[0].store.nodes) {
					t.Fatal("persisted node contents differ")
				}
				recycleAccounting(t, lane.tree)
				lane.store.check(t)
				if !commitScratchClean(lane.tree) {
					t.Fatal("worker scratch retains node")
				}
				if round == 0 && lane.workers >= 4 && commitWorkerCount(lane.tree) != 4 {
					t.Fatal("parallel branch not exercised")
				}
				if lane.tree.Hash() != want {
					t.Fatal("serial Hash differs")
				}
				key := keys[(round*29)%len(keys)]
				proof, err := lane.tree.Prove(key)
				if err != nil {
					t.Fatal(err)
				}
				value, err := trie.VerifyProof(lane.root, key, proof)
				if err != nil || !bytes.Equal(value, model[string(key)]) {
					t.Fatal("proof differs", err)
				}
				if round%2 == 0 {
					lane.tree.Evict(round % 3)
				} else {
					lane.tree = trie.Open(lane.root, lane.store)
				}
				for n := 0; n < 16; n++ {
					k := keys[(round*71+n*37)%len(keys)]
					value, err := lane.tree.Find(k)
					if err != nil || !bytes.Equal(value, model[string(k)]) {
						t.Fatal("evict/reopen Find differs", err)
					}
				}
			}
		}
		for _, lane := range lanes {
			for _, k := range keys {
				if err := lane.tree.Unset(k); err != nil {
					t.Fatal(err)
				}
			}
			root := lane.tree.Commit(lane.store)
			if root != trie.EmptyRoot {
				t.Fatal("wipe did not produce empty root")
			}
			if !commitScratchClean(lane.tree) {
				t.Fatal("wipe retains worker scratch")
			}
		}
	}
}

func TestParallelCommitSmallAndExtensionFallback(t *testing.T) {
	for _, prefix := range [][]byte{nil, {0, 0, 0}} {
		tr := trie.New()
		reference := trie.New()
		for i := 0; i < 32; i++ {
			key := append(bytes.Clone(prefix), byte(i))
			tr.Update(key, recycleValue(i))
			reference.Update(key, recycleValue(i))
		}
		serial := reference.Hash()
		store := newMemStore()
		if tr.Commit(store) != serial {
			t.Fatal("fallback root differs")
		}
		if commitWorkerCount(tr) != 0 {
			t.Fatal("small tree spawned workers")
		}
	}
}

func commitWorkerCount(t *trie.Trie) int {
	pool := reflect.ValueOf(t).Elem().FieldByName("commit")
	if pool.IsNil() {
		return 0
	}
	count := 1
	lanes := pool.Elem().FieldByName("lanes")
	for i := 0; i < lanes.Len(); i++ {
		if !lanes.Index(i).IsNil() {
			count++
		}
	}
	return count
}
func commitScratchClean(t *trie.Trie) bool {
	pool := reflect.ValueOf(t).Elem().FieldByName("commit")
	if pool.IsNil() {
		return true
	}
	lanes := pool.Elem().FieldByName("lanes")
	for i := 0; i < lanes.Len(); i++ {
		if lanes.Index(i).IsNil() {
			continue
		}
		scratch := lanes.Index(i).Elem().FieldByName("scratch")
		for j := 0; j < scratch.Cap(); j++ {
			if !scratch.Slice(0, scratch.Cap()).Index(j).IsZero() {
				return false
			}
		}
	}
	return true
}
