package trie_test

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/trie"
)

type writer interface {
	Update(key []byte, value trie.Value)
	Hash() primitives.Hash
}

func decodeField(t *testing.T, s string) []byte {
	t.Helper()
	if !strings.HasPrefix(s, "0x") {
		return []byte(s)
	}
	b, err := primitives.DecodeHex(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

type orderedCase struct {
	In   [][]*string `json:"in"`
	Root string      `json:"root"`
}

type anyOrderCase struct {
	In   map[string]string `json:"in"`
	Root string            `json:"root"`
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func runOrdered(t *testing.T, path string, fresh func() writer) {
	cases := map[string]orderedCase{}
	readJSON(t, path, &cases)
	for _, name := range sortedKeys(cases) {
		test := cases[name]
		t.Run(name, func(t *testing.T) {
			tr := fresh()
			for _, pair := range test.In {
				var value []byte
				if pair[1] != nil {
					value = decodeField(t, *pair[1])
				}
				tr.Update(decodeField(t, *pair[0]), value)
			}
			if got := tr.Hash().Hex(); got != test.Root {
				t.Fatalf("root %s want %s", got, test.Root)
			}
		})
	}
}

func runAnyOrder(t *testing.T, path string, fresh func() writer) {
	cases := map[string]anyOrderCase{}
	readJSON(t, path, &cases)
	for _, name := range sortedKeys(cases) {
		test := cases[name]
		t.Run(name, func(t *testing.T) {
			keys := sortedKeys(test.In)
			for round := 0; round < 4; round++ {
				tr := fresh()
				order := rand.New(rand.NewPCG(uint64(round), 7)).Perm(len(keys))
				for _, i := range order {
					tr.Update(decodeField(t, keys[i]), decodeField(t, test.In[keys[i]]))
				}
				if got := tr.Hash().Hex(); got != test.Root {
					t.Fatalf("round %d root %s want %s", round, got, test.Root)
				}
			}
		})
	}
}

func plain() writer  { return trie.New() }
func secure() writer { return trie.NewSecure() }

func TestEthereumTrieTests(t *testing.T) {
	runOrdered(t, "testdata/trietest.json", plain)
	runAnyOrder(t, "testdata/trieanyorder.json", plain)
}

func TestEthereumSecureTrieTests(t *testing.T) {
	runOrdered(t, "testdata/trietest_secureTrie.json", secure)
	runAnyOrder(t, "testdata/trieanyorder_secureTrie.json", secure)
	runAnyOrder(t, "testdata/hex_encoded_securetrie_test.json", secure)
}

func TestEmptyAndGet(t *testing.T) {
	tr := trie.New()
	if tr.Hash() != trie.EmptyRoot {
		t.Fatal("empty root")
	}
	tr.Update([]byte("dog"), []byte("puppy"))
	tr.Update([]byte("do"), []byte("verb"))
	got, ok := tr.Get([]byte("do"))
	if !ok {
		t.Fatal("missing do")
	}
	if string(got) != "verb" {
		t.Fatal("do value")
	}
	if _, ok := tr.Get([]byte("d")); ok {
		t.Fatal("found d")
	}
	if _, ok := tr.Get([]byte("doge")); ok {
		t.Fatal("found doge")
	}
	tr.Delete([]byte("dog"))
	tr.Delete([]byte("do"))
	tr.Delete([]byte("absent"))
	if tr.Hash() != trie.EmptyRoot {
		t.Fatal("root after deleting everything")
	}
	if tr.Nodes() != 0 {
		t.Fatalf("nodes %d", tr.Nodes())
	}
	s := trie.NewSecure()
	s.Update([]byte("k"), []byte("v"))
	if v, ok := s.Get([]byte("k")); !ok || string(v) != "v" {
		t.Fatal("secure get")
	}
	s.Delete([]byte("k"))
	if s.Hash() != trie.EmptyRoot {
		t.Fatal("secure delete")
	}
}

func randomKey(r *rand.Rand) []byte {
	alphabet := []byte{0x00, 0x01, 0x10, 0x11, 0xab, 0xff}
	n := 1 + r.IntN(4)
	k := make([]byte, n)
	for i := range k {
		k[i] = alphabet[r.IntN(len(alphabet))]
	}
	return k
}

func randomValue(r *rand.Rand) []byte {
	n := 1 + r.IntN(40)
	v := make([]byte, n)
	for i := range v {
		v[i] = byte(r.Uint32())
	}
	return v
}

func rebuild(model map[string][]byte) *trie.Trie {
	fresh := trie.New()
	for _, k := range sortedKeys(model) {
		fresh.UpdateSorted([]byte(k), model[k])
	}
	return fresh
}

func TestRandomOpsMatchRebuild(t *testing.T) {
	for seed := uint64(0); seed < 200; seed++ {
		r := rand.New(rand.NewPCG(seed, 99))
		tr := trie.New()
		model := map[string][]byte{}
		for op := 0; op < 60; op++ {
			k := randomKey(r)
			if r.IntN(3) == 0 {
				tr.Delete(k)
				delete(model, string(k))
			} else {
				v := randomValue(r)
				tr.Update(k, v)
				model[string(k)] = v
			}
			if r.IntN(4) == 0 {
				tr.Hash()
			}
		}
		want := rebuild(model)
		if tr.Hash() != want.Hash() {
			t.Fatalf("seed %d: root %s want %s", seed, tr.Hash(), want.Hash())
		}
		for _, k := range sortedKeys(model) {
			v := model[k]
			got, ok := tr.Get([]byte(k))
			if !ok || !bytes.Equal(got, v) {
				t.Fatalf("seed %d: get %x", seed, k)
			}
		}
		if len(model) == 0 {
			if tr.Nodes() != 0 {
				t.Fatalf("seed %d: %d nodes left", seed, tr.Nodes())
			}
		}
	}
}

func TestOrderedRootMatchesPlainTrie(t *testing.T) {
	sizes := []int{0, 1, 2, 16, 127, 128, 129, 300, 70000}
	r := rand.New(rand.NewPCG(1, 2))
	for _, n := range sizes {
		items := make([][]byte, n)
		for i := range items {
			items[i] = randomValue(r)
		}
		plainTrie := trie.New()
		for i := n - 1; i >= 0; i-- {
			plainTrie.Update(rlp.EncodeUint64(uint64(i)), items[i])
		}
		if got := trie.OrderedRoot(items); got != plainTrie.Hash() {
			t.Fatalf("n=%d: ordered %s plain %s", n, got, plainTrie.Hash())
		}
	}
}

func TestSortedModeBoundsMemory(t *testing.T) {
	tr := trie.New()
	var key [8]byte
	for i := uint64(0); i < 50000; i++ {
		primitives.PutUint64(key[:], i)
		tr.UpdateSorted(key[:], []byte{1, 2, 3})
	}
	if tr.Nodes() > 200 {
		t.Fatalf("sorted trie kept %d nodes", tr.Nodes())
	}
	unsorted := trie.New()
	for i := uint64(0); i < 50000; i++ {
		primitives.PutUint64(key[:], i)
		unsorted.Update(key[:], []byte{1, 2, 3})
	}
	if tr.Hash() != unsorted.Hash() {
		t.Fatal("sorted and unsorted roots differ")
	}
}

func BenchmarkOrderedRoot1000(b *testing.B) {
	items := make([][]byte, 1000)
	for i := range items {
		items[i] = bytes.Repeat([]byte{byte(i)}, 120)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		trie.OrderedRoot(items)
	}
}
