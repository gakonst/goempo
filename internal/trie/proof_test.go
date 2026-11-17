package trie_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/kapetan-io/tiger/assert"
)

type memNodes map[string][]byte

func (m memNodes) WriteNode(n trie.StoredNode) { m[string(n.Path)] = n.Encoded }

func (m memNodes) ReadNode(path []byte) ([]byte, error) { return m[string(path)], nil }

type provedTrie struct {
	t    *trie.Trie
	keys [][]byte
}

func provingTrie(count int) provedTrie {
	t := trie.New()
	keys := make([][]byte, count)
	for i := range keys {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(i))
		h := keccak.Sum256(b[:])
		keys[i] = h[:]
		t.Update(keys[i], append([]byte("value-"), b[:]...))
	}
	return provedTrie{t: t, keys: keys}
}

func TestProveVerifiesForEveryKey(t *testing.T) {
	pt := provingTrie(300)
	tr, keys := pt.t, pt.keys
	root := tr.Hash()
	for _, k := range keys {
		proof, err := tr.Prove(k)
		assert.NoError(err, "prove")
		got, err := trie.VerifyProof(root, k, proof)
		assert.NoError(err, "verify")
		want, _ := tr.Get(k)
		assert.Equal(string(got), string(want), "proved value")
	}
}

func TestProveAbsentKey(t *testing.T) {
	tr := provingTrie(50).t
	root := tr.Hash()
	missing := keccak.Sum256([]byte("absent"))
	proof, err := tr.Prove(missing[:])
	assert.NoError(err, "prove absent")
	assert.Ok(len(proof) > 0, "absence proof has nodes")
	got, err := trie.VerifyProof(root, missing[:], proof)
	assert.NoError(err, "verify absent")
	assert.Equal(len(got), 0, "no value")
}

func TestProveFromStoredNodes(t *testing.T) {
	pt := provingTrie(200)
	tr, keys := pt.t, pt.keys
	store := memNodes{}
	root := tr.Commit(store)
	reopened := trie.Open(root, store)
	for _, k := range keys[:20] {
		proof, err := reopened.Prove(k)
		assert.NoError(err, "prove from store")
		got, err := trie.VerifyProof(root, k, proof)
		assert.NoError(err, "verify stored")
		want, _ := tr.Get(k)
		assert.Equal(string(got), string(want), "stored value")
	}
	proof, err := reopened.Prove(keys[0])
	assert.NoError(err, "prove")
	proof[len(proof)-1] = append([]byte(nil), proof[len(proof)-1]...)
	proof[len(proof)-1][len(proof[len(proof)-1])-1] ^= 1
	_, err = trie.VerifyProof(root, keys[0], proof)
	assert.Ok(errors.Is(err, trie.ErrProof), "tampered proof fails")
}

func TestProveTinyTrieEmbedsRoot(t *testing.T) {
	tr := trie.New()
	tr.Update([]byte{1}, []byte{2})
	proof, err := tr.Prove([]byte{1})
	assert.NoError(err, "prove")
	assert.Equal(len(proof), 1, "root always included")
	got, err := trie.VerifyProof(tr.Hash(), []byte{1}, proof)
	assert.NoError(err, "verify")
	assert.Equal(string(got), string([]byte{2}), "value")
}
