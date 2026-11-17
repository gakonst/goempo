package trie_test

import (
	"bytes"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/gakonst/goempo/internal/trie"
)

// logWriter records every write in order; as a SplitWriter it hands out
// sub-logs and appends them in order on Join.
type logWriter struct {
	writes []trie.StoredNode
	subs   []*logWriter
	splits int
}

func (l *logWriter) WriteNode(n trie.StoredNode) {
	l.writes = append(l.writes, trie.StoredNode{Path: bytes.Clone(n.Path), Encoded: n.Encoded})
}

func (l *logWriter) Split(n int) []trie.NodeWriter {
	l.splits++
	l.subs = l.subs[:0]
	out := make([]trie.NodeWriter, n)
	for i := range out {
		sub := &logWriter{}
		l.subs = append(l.subs, sub)
		out[i] = sub
	}
	return out
}

func (l *logWriter) Join() {
	for _, sub := range l.subs {
		l.writes = append(l.writes, sub.writes...)
	}
	l.subs = l.subs[:0]
}

type plainWriter struct{ l *logWriter }

func (p plainWriter) WriteNode(n trie.StoredNode) { p.l.WriteNode(n) }

// SplitWriter: a large Commit saves each dirty subtree through its own writer
// on a worker and joins them in child order.
//
// TestParallelSaveMatchesSequential commits the same blocks through a
// sequential and a split writer: the parallel save writes the same nodes,
// in the same order on every run, and a trie reopened from them reads back.
func TestParallelSaveMatchesSequential(t *testing.T) {
	for seed := uint64(0); seed < 6; seed++ {
		var logs [3][][]trie.StoredNode
		var roots [3][]byte
		splits := 0
		for run := 0; run < 3; run++ {
			r := rand.New(rand.NewPCG(seed, 23))
			store := newMemStore()
			tr := trie.Open(trie.EmptyRoot, store)
			model := map[string][]byte{}
			var all [][]trie.StoredNode
			var last []byte
			for block := 0; block < 4; block++ {
				ops := []int{6000, 300, 5000, 20}[block]
				for op := 0; op < ops; op++ {
					k := hashedKey(r)
					v := randomValue(r)
					if err := tr.Set(k, v); err != nil {
						t.Fatal(err)
					}
					model[string(k)] = v
				}
				l := &logWriter{}
				var root [32]byte
				if run == 0 {
					root = tr.Commit(plainWriter{l})
				} else {
					root = tr.Commit(l)
					splits += l.splits
				}
				for _, w := range l.writes {
					store.WriteNode(w)
				}
				all = append(all, l.writes)
				if want := rebuild(model).Hash(); root != want {
					t.Fatalf("seed %d run %d block %d: root %x want %x", seed, run, block, root, want)
				}
				last = root[:]
			}
			logs[run], roots[run] = all, last
			reopened := trie.Open([32]byte(last), store)
			for _, k := range slices.Sorted(maps.Keys(model)) {
				v := model[k]
				got, err := reopened.Find([]byte(k))
				if err != nil || !bytes.Equal(got, v) {
					t.Fatalf("seed %d run %d: reopened trie reads %x, %v; want %x", seed, run, got, err, v)
				}
			}
		}
		if splits == 0 {
			t.Fatalf("seed %d: no commit saved in parallel", seed)
		}
		for block := range logs[0] {
			if !sameWrites(writeLogs{a: logs[1][block], b: logs[2][block]}) {
				t.Fatalf("seed %d block %d: parallel save order differs between runs", seed, block)
			}
			if !sameSet(writeLogs{a: logs[0][block], b: logs[1][block]}) {
				t.Fatalf("seed %d block %d: parallel save wrote different nodes than sequential", seed, block)
			}
		}
	}
}

type writeLogs struct {
	a, b []trie.StoredNode
}

func sameWrites(l writeLogs) bool {
	a, b := l.a, l.b
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Path, b[i].Path) || !bytes.Equal(a[i].Encoded, b[i].Encoded) {
			return false
		}
	}
	return true
}

func sameSet(l writeLogs) bool {
	a, b := l.a, l.b
	if len(a) != len(b) {
		return false
	}
	m := make(map[string][]byte, len(a))
	for _, w := range a {
		m[string(w.Path)] = w.Encoded
	}
	for _, w := range b {
		if enc, ok := m[string(w.Path)]; !ok || !bytes.Equal(enc, w.Encoded) {
			return false
		}
	}
	return true
}
