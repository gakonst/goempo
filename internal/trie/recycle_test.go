package trie_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/kapetan-io/tiger/assert"
)

// This model rebuilds the Patricia shape and RLP from a plain map. It does not
// call trie insertion, deletion, path encoding, RLP encoding or hashing walks.
type recycleEntry struct{ key, value []byte }

type recycleRLPKind bool

const (
	recycleBytes    recycleRLPKind = false
	recycleSequence recycleRLPKind = true
)

func recycleRLP(raw []byte, list recycleRLPKind) []byte {
	if !list && len(raw) == 1 && raw[0] < 128 {
		return bytes.Clone(raw)
	}
	base := byte(128)
	if list == recycleSequence {
		base = 192
	}
	if len(raw) < 56 {
		return append([]byte{base + byte(len(raw))}, raw...)
	}
	n := len(raw)
	var size []byte
	for n > 0 {
		size = append([]byte{byte(n)}, size...)
		n >>= 8
	}
	out := append([]byte{base + 55 + byte(len(size))}, size...)
	return append(out, raw...)
}
func recycleList(items ...[]byte) []byte {
	var raw []byte
	for _, item := range items {
		raw = append(raw, item...)
	}
	return recycleRLP(raw, recycleSequence)
}

type recyclePathKind bool

const (
	recycleExtension recyclePathKind = false
	recycleLeaf      recyclePathKind = true
)

func recycleCompact(path []byte, leaf recyclePathKind) []byte {
	flag := byte(0)
	if leaf == recycleLeaf {
		flag = 2
	}
	odd := len(path) % 2
	flag += byte(odd)
	out := []byte{flag << 4}
	if odd == 1 {
		out[0] |= path[0]
		path = path[1:]
	}
	for i := 0; i < len(path); i += 2 {
		out = append(out, path[i]<<4|path[i+1])
	}
	return out
}
func recycleChild(raw []byte) []byte {
	if len(raw) < 32 {
		return raw
	}
	h := keccak.Sum256(raw)
	return recycleRLP(h[:], recycleBytes)
}

type recycleFrame struct {
	entries              []recycleEntry
	depth                int
	exit                 bool
	kind                 byte
	path, value, encoded []byte
	children             [16]*recycleFrame
}

func recycleEncode(entries []recycleEntry, depth int) []byte {
	root := &recycleFrame{entries: entries, depth: depth}
	work := []*recycleFrame{root}
	maxDepth := depth
	for _, e := range entries {
		maxDepth = max(maxDepth, len(e.key))
	}
	limit := 2 * (len(entries)*(maxDepth+1) + 1)
	step := 0
	for ; step < limit; step++ {
		if len(work) == 0 {
			break
		}
		last := len(work) - 1
		f := work[last]
		work = work[:last]
		if f.exit {
			if f.kind == 1 {
				f.encoded = recycleList(recycleRLP(recycleCompact(f.path, recycleExtension), recycleBytes), recycleChild(f.children[0].encoded))
				continue
			}
			items := make([][]byte, 17)
			for i, c := range f.children {
				items[i] = recycleRLP(nil, recycleBytes)
				if c != nil {
					items[i] = recycleChild(c.encoded)
				}
			}
			items[16] = recycleRLP(f.value, recycleBytes)
			f.encoded = recycleList(items...)
			continue
		}
		es, d := f.entries, f.depth
		if len(es) == 1 {
			f.encoded = recycleList(recycleRLP(recycleCompact(es[0].key[d:], recycleLeaf), recycleBytes), recycleRLP(es[0].value, recycleBytes))
			continue
		}
		common := d
		for i := d; i < len(es[0].key); i++ {
			same := true
			for _, e := range es[1:] {
				if i >= len(e.key) || e.key[i] != es[0].key[i] {
					same = false
					break
				}
			}
			if !same {
				break
			}
			common = i + 1
		}
		f.exit = true
		work = append(work, f)
		if common > d {
			f.kind = 1
			f.path = es[0].key[d:common]
			f.children[0] = &recycleFrame{entries: es, depth: common}
			work = append(work, f.children[0])
			continue
		}
		for nib := 0; nib < 16; nib++ {
			var group []recycleEntry
			for _, e := range es {
				if len(e.key) > d && int(e.key[d]) == nib {
					group = append(group, e)
				}
			}
			if len(group) > 0 {
				f.children[nib] = &recycleFrame{entries: group, depth: d + 1}
				work = append(work, f.children[nib])
			}
		}
		for _, e := range es {
			if len(e.key) == d {
				f.value = e.value
			}
		}
	}
	assert.Ok(len(work) == 0, "independent model exceeded node bound")
	return root.encoded
}
func recycleRoot(model map[string][]byte) primitives.Hash {
	if len(model) == 0 {
		return keccak.Sum256([]byte{128})
	}
	var entries []recycleEntry
	for _, key := range sortedKeys(model) {
		var nibbles []byte
		for _, b := range []byte(key) {
			nibbles = append(nibbles, b>>4, b&15)
		}
		entries = append(entries, recycleEntry{nibbles, model[key]})
	}
	return keccak.Sum256(recycleEncode(entries, 0))
}
func recycleKeys(n int) [][]byte {
	keys := [][]byte{nil, {0}, {0, 1}, {0, 1, 2}, {255}}
	for i := 0; i < n; i++ {
		v := uint32(i) * 2654435761
		keys = append(keys, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	return keys
}
func recycleValue(i int) []byte {
	sizes := []int{1, 2, 31, 32, 33, 55, 56, 128}
	return bytes.Repeat([]byte{byte(i%251 + 1)}, sizes[i%len(sizes)])
}

// Read-only reflection inspects retention invariants without adding test-only
// exported production hooks or using unsafe to reach private implementation.
func recycleAccounting(t *testing.T, tr *trie.Trie) int {
	t.Helper()
	state := reflect.ValueOf(tr).Elem()
	live := map[uintptr]bool{}
	stack := []reflect.Value{state.FieldByName("root")}
	steps := 0
	for ; steps <= tr.Nodes(); steps++ {
		if len(stack) == 0 {
			break
		}
		last := len(stack) - 1
		p := stack[last]
		stack = stack[:last]
		if p.IsNil() {
			continue
		}
		if live[p.Pointer()] {
			t.Fatal("live node shared or cyclic")
		}
		live[p.Pointer()] = true
		children := p.Elem().FieldByName("children")
		for i := 0; i < children.Len(); i++ {
			if !children.Index(i).IsNil() {
				stack = append(stack, children.Index(i))
			}
		}
	}
	assert.Ok(len(stack) == 0, "live traversal exceeded node bound")
	if len(live) != tr.Nodes() {
		t.Fatalf("live=%d Nodes=%d", len(live), tr.Nodes())
	}
	free := state.FieldByName("free")
	if free.Len() > 256 || free.Cap() > 256 {
		t.Fatalf("unbounded recycler %d/%d", free.Len(), free.Cap())
	}
	seen := map[uintptr]bool{}
	for i := 0; i < free.Len(); i++ {
		p := free.Index(i)
		if p.IsNil() || live[p.Pointer()] || seen[p.Pointer()] {
			t.Fatal("recycler aliases live or duplicate node")
		}
		seen[p.Pointer()] = true
		if !p.Elem().IsZero() {
			t.Fatal("recycled node retains pointers or state")
		}
	}
	all := free.Slice(0, free.Cap())
	for i := free.Len(); i < all.Len(); i++ {
		if !all.Index(i).IsNil() {
			t.Fatal("inactive recycler slot retains node")
		}
	}
	scratch := state.FieldByName("scratch")
	allScratch := scratch.Slice(0, scratch.Cap())
	for i := 0; i < allScratch.Len(); i++ {
		if !allScratch.Index(i).IsZero() {
			t.Fatal("inactive scratch retains node/path")
		}
	}
	return free.Len()
}

type recycleWrite struct {
	node          trie.StoredNode
	path, encoded []byte
}
type recycleStore struct {
	*memStore
	retained []recycleWrite
}

func (s *recycleStore) WriteNode(n trie.StoredNode) {
	s.memStore.WriteNode(n)
	s.retained = append(s.retained, recycleWrite{n, bytes.Clone(n.Path), bytes.Clone(n.Encoded)})
}
func (s *recycleStore) check(t *testing.T) {
	t.Helper()
	for _, w := range s.retained {
		if !bytes.Equal(w.node.Path, w.path) || !bytes.Equal(w.node.Encoded, w.encoded) {
			t.Fatal("writer-retained bytes mutated")
		}
	}
}

func TestRecyclerIndependentModel(t *testing.T) {
	keys := recycleKeys(512)
	for lane := 0; lane < 2; lane++ {
		store := &recycleStore{memStore: newMemStore()}
		tr := trie.Open(trie.EmptyRoot, store)
		model := map[string][]byte{}
		type retainedProof struct {
			root                 primitives.Hash
			key, value, returned []byte
			proof                [][]byte
		}
		var history []retainedProof
		for i, k := range keys {
			v := recycleValue(i + lane)
			if err := tr.Set(k, v); err != nil {
				t.Fatal(err)
			}
			model[string(k)] = bytes.Clone(v)
			clear(v)
		}
		for round := 0; round < 16; round++ {
			for op := 0; op < 96; op++ {
				i := (round*61 + op*37 + lane) % len(keys)
				key := keys[i]
				if (round+op)%4 == 0 {
					if err := tr.Unset(key); err != nil {
						t.Fatal(err)
					}
					delete(model, string(key))
				} else {
					v := recycleValue(round + op + lane)
					if err := tr.Set(key, v); err != nil {
						t.Fatal(err)
					}
					model[string(key)] = bytes.Clone(v)
					clear(v)
				}
			}
			root := tr.Commit(store)
			want := recycleRoot(model)
			if root != want {
				t.Fatalf("lane%d round%d root=%s want=%s", lane, round, root, want)
			}
			recycleAccounting(t, tr)
			store.check(t)
			key := keys[(round*29)%len(keys)]
			proof, err := tr.Prove(key)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := tr.Find(key)
			if err != nil {
				t.Fatal(err)
			}
			savedValue := bytes.Clone(retained)
			history = append(history, retainedProof{root, bytes.Clone(key), savedValue, retained, proof})
			tr.Evict(round % 3)
			recycleAccounting(t, tr)
			for _, k := range keys {
				got, err := tr.Find(k)
				if err != nil || !bytes.Equal(got, model[string(k)]) {
					t.Fatalf("round%d key%x got%x err%v", round, k, got, err)
				}
			}
			if !bytes.Equal(retained, savedValue) {
				t.Fatal("Find value changed after recycling")
			}
			got, err := trie.VerifyProof(root, key, proof)
			if err != nil || !bytes.Equal(got, model[string(key)]) {
				t.Fatalf("retained proof: %x %v", got, err)
			}
			if tr.Hash() != want {
				t.Fatal("read/evict changed root")
			}
			recycleAccounting(t, tr)
			store.check(t)
			fresh := trie.Open(root, store)
			for _, k := range keys {
				got, err := fresh.Find(k)
				if err != nil || !bytes.Equal(got, model[string(k)]) {
					t.Fatal("reopen disagrees", err)
				}
			}
			for _, old := range history {
				if !bytes.Equal(old.returned, old.value) {
					t.Fatal("historical Find bytes mutated")
				}
				v, err := trie.VerifyProof(old.root, old.key, old.proof)
				if err != nil || !bytes.Equal(v, old.value) {
					t.Fatal("historical proof bytes mutated", err)
				}
			}
			if fresh.Hash() != want {
				t.Fatal("reopened root disagrees")
			}
			recycleAccounting(t, fresh)
		}
	}
}

func TestRecyclerErrorsPreserveSealedNode(t *testing.T) {
	store := newMemStore()
	tr := trie.Open(trie.EmptyRoot, store)
	keys := recycleKeys(64)
	for i, k := range keys {
		if err := tr.Set(k, recycleValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	root := tr.Commit(store)
	tr = trie.Open(root, store)
	// Warm the pool before injecting a descendant failure.
	if _, err := tr.Find(keys[10]); err != nil {
		t.Fatal(err)
	}
	tr.Evict(0)
	good := store.nodes[""]
	delete(store.nodes, "")
	missing := trie.Open(root, store)
	for range 3 {
		if _, err := missing.Find(keys[10]); !errors.Is(err, errMissing) {
			t.Fatalf("missing: %v", err)
		}
		if missing.Nodes() != 1 || missing.Hash() != root {
			t.Fatal("missing mutated sealed root")
		}
		recycleAccounting(t, missing)
	}
	store.nodes[""] = good
	// A corrupt descendant is detected even after the root and pool are warm.
	for _, path := range sortedKeys(store.nodes) {
		if len(path) > 0 {
			original := store.nodes[path]
			delete(store.nodes, path)
			missingFailures := 0
			for _, key := range keys {
				if _, err := tr.Find(key); errors.Is(err, errMissing) {
					missingFailures++
				}
			}
			if missingFailures == 0 {
				t.Fatal("no descendant missing-node failure detected")
			}
			recycleAccounting(t, tr)
			store.nodes[path] = []byte{192}
			failures := 0
			for _, key := range keys {
				if _, err := tr.Find(key); errors.Is(err, trie.ErrNodeHash) {
					failures++
				}
			}
			if failures == 0 {
				t.Fatal("no descendant corruption detected")
			}
			recycleAccounting(t, tr)
			store.nodes[path] = original
			break
		}
	}
	for _, key := range keys {
		if _, err := tr.Find(key); err != nil {
			t.Fatal("restore", err)
		}
	}
	if tr.Hash() != root {
		t.Fatal("corruption changed root")
	}
	// Hash-valid malformed encodings exercise errors after partial decode and
	// allocation. Repeated attempts must not publish partial trees or counts.
	hash := bytes.Repeat([]byte{7}, 32)
	items := make([][]byte, 17)
	for i := range items {
		items[i] = recycleRLP(nil, recycleBytes)
	}
	items[0] = recycleRLP(hash, recycleBytes)
	items[1] = recycleRLP([]byte{1, 2}, recycleBytes)
	malformed := [][]byte{recycleList(items...), recycleList(recycleRLP([]byte{0}, recycleBytes), []byte{192}), {192}, {255}}
	for _, raw := range malformed {
		h := keccak.Sum256(raw)
		s := newMemStore()
		s.nodes[""] = raw
		bad := trie.Open(h, s)
		for range 5 {
			if _, err := bad.Find([]byte{0}); !errors.Is(err, trie.ErrNodeRLP) {
				t.Fatalf("malformed %x: %v", raw, err)
			}
			if bad.Nodes() != 1 || bad.Hash() != h {
				t.Fatal("decode error published partial tree")
			}
			recycleAccounting(t, bad)
		}
	}
}

func TestRecyclerCapacityAndAllocations(t *testing.T) {
	store := newMemStore()
	tr := trie.Open(trie.EmptyRoot, store)
	keys := recycleKeys(2048)
	for i, k := range keys {
		if err := tr.Set(k, recycleValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	root := tr.Commit(store)
	before := tr.Nodes()
	tr.Evict(0)
	if n := recycleAccounting(t, tr); n < 240 {
		t.Fatalf("pool did not fill near cap: %d", n)
	}
	if tr.Nodes() >= before {
		t.Fatal("eviction failed")
	}
	// A small working set fits in the fixed retention cap. Each measured cycle
	// traverses and evicts identical saved nodes, with and without a warm pool.
	small := newMemStore()
	warm := trie.Open(trie.EmptyRoot, small)
	smallKeys := recycleKeys(32)
	for i, k := range smallKeys {
		if err := warm.Set(k, recycleValue(i)); err != nil {
			t.Fatal(err)
		}
	}
	smallRoot := warm.Commit(small)
	var failure error
	walk := func(tree *trie.Trie) {
		for _, key := range smallKeys {
			if _, err := tree.Find(key); err != nil {
				failure = err
			}
		}
		tree.Evict(0)
	}
	walk(warm)
	reused := testing.AllocsPerRun(20, func() { walk(warm) })
	cold := testing.AllocsPerRun(20, func() { walk(trie.Open(smallRoot, small)) })
	if failure != nil {
		t.Fatal(failure)
	}
	t.Logf("warm=%0.2f cold=%0.2f allocations per complete find/evict cycle", reused, cold)
	if cold-reused < 30 {
		t.Fatalf("node recycling savings too small: warm=%f cold=%f", reused, cold)
	}
	recycleAccounting(t, warm)
	if tr.Hash() != root {
		t.Fatal("cap eviction changed root")
	}
}

func TestRecyclerSortedSealingModel(t *testing.T) {
	keys := recycleKeys(512)
	model := map[string][]byte{}
	for i, k := range keys {
		model[string(k)] = recycleValue(i)
	}
	tr := trie.New()
	for _, k := range sortedKeys(model) {
		tr.UpdateSorted([]byte(k), model[k])
		recycleAccounting(t, tr)
	}
	if got, want := tr.Hash(), recycleRoot(model); got != want {
		t.Fatalf("sorted root %s want %s", got, want)
	}
	recycleAccounting(t, tr)
}

func TestRecyclerIndependentTriesInterleaved(t *testing.T) {
	keys := recycleKeys(64)
	stores := []*memStore{newMemStore(), newMemStore()}
	tries := []*trie.Trie{trie.Open(trie.EmptyRoot, stores[0]), trie.Open(trie.EmptyRoot, stores[1])}
	models := []map[string][]byte{{}, {}}
	for round := 0; round < 12; round++ {
		for i, k := range keys {
			for lane, tr := range tries {
				if (i+round+lane)%5 == 0 {
					if err := tr.Unset(k); err != nil {
						t.Fatal(err)
					}
					delete(models[lane], string(k))
				} else {
					v := recycleValue(i + round + lane)
					if err := tr.Set(k, v); err != nil {
						t.Fatal(err)
					}
					models[lane][string(k)] = v
				}
			}
		}
		for lane, tr := range tries {
			root := tr.Commit(stores[lane])
			if root != recycleRoot(models[lane]) {
				t.Fatal("interleaved independent root mismatch")
			}
			tr.Evict(0)
			recycleAccounting(t, tr)
			for _, k := range keys {
				v, err := tr.Find(k)
				if err != nil || !bytes.Equal(v, models[lane][string(k)]) {
					t.Fatal("cross-trie reuse", err)
				}
			}
		}
	}
}
