package trie

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
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

// OrderedRoot is the root of the trie mapping rlp(i) to items[i], the
// transactions, receipts and withdrawals root. It builds the whole trie
// without copying the items, then hashes it level by level (and on workers
// for long lists).
func OrderedRoot(items [][]byte) primitives.Hash {
	n := len(items)
	if n == 0 {
		return EmptyRoot
	}
	t := &Trie{borrowValues: true}
	for i := 0; i < n; i++ {
		assert.Ok(len(items[i]) > 0, "trie OrderedRoot with an empty item")
		var key [9]byte
		assert.NoError(t.insert(keyNibbles(appendIndexKey(key[:0], uint64(i))), items[i], nil), "ordered trie insert")
	}
	return t.Hash()
}

// appendIndexKey appends rlp(v), the key of item v in an ordered trie.
func appendIndexKey(dst []byte, v uint64) []byte {
	if v == 0 {
		return append(dst, 0x80)
	}
	if v < 0x80 {
		return append(dst, byte(v))
	}
	size := 0
	for rest := v; rest > 0 && size < 8; rest >>= 8 {
		size++
	}
	dst = append(dst, 0x80+byte(size))
	for k := size - 1; k >= 0; k-- {
		dst = append(dst, byte(v>>(8*k)))
	}
	return dst
}
