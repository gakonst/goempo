package trie

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

type Secure struct {
	trie   *Trie
	hasher *keccak.Hasher
}

func NewSecure() *Secure {
	return &Secure{trie: New(), hasher: keccak.New()}
}

func (s *Secure) hashKey(key []byte) primitives.Hash {
	return s.hasher.Hash(key)
}

func (s *Secure) Get(key []byte) ([]byte, bool) {
	h := s.hashKey(key)
	return s.trie.Get(h[:])
}

func (s *Secure) Update(key []byte, value Value) {
	h := s.hashKey(key)
	s.trie.Update(h[:], value)
}

func (s *Secure) Delete(key []byte) {
	h := s.hashKey(key)
	s.trie.Delete(h[:])
}

func (s *Secure) Hash() primitives.Hash {
	return s.trie.Hash()
}

func OrderedRoot(items [][]byte) primitives.Hash {
	t := New()
	n := len(items)
	if n == 0 {
		return EmptyRoot
	}
	for i := 1; i < min(n, 0x80); i++ {
		t.UpdateSorted(rlp.EncodeUint64(uint64(i)), items[i])
	}
	t.UpdateSorted(rlp.EncodeUint64(0), items[0])
	for i := 0x80; i < n; i++ {
		t.UpdateSorted(rlp.EncodeUint64(uint64(i)), items[i])
	}
	return t.Hash()
}
