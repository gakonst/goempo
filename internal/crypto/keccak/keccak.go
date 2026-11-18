// Package keccak computes Keccak-256, the pre-standard SHA-3 variant Ethereum
// and Tempo hash with (padding byte 0x01, not FIPS 202's 0x06).
//
// Sum256 and Sum256Concat borrow a hashing state from a pool. A Hasher owns its state and
// computes any number of hashes with no allocation: Reset, Write, then Sum or
// SumInto; Hash does all three for one input.
package keccak

import (
	"hash"
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
	"golang.org/x/crypto/sha3"
)

const Size = 32

const BlockSize = 136

type readHash interface {
	hash.Hash
	Read(out []byte) (int, error)
}

type Hasher struct {
	state readHash
	out   primitives.Hash
}

func New() *Hasher {
	state, ok := sha3.NewLegacyKeccak256().(readHash)
	assert.Ok(ok, "legacy keccak state has a Read method")
	return &Hasher{state: state}
}

func (h *Hasher) Reset() {
	h.state.Reset()
}

func (h *Hasher) Write(b []byte) {
	n, err := h.state.Write(b)
	assert.NoError(err, "keccak write")
	assert.Equal(n, len(b), "keccak write length")
}

func (h *Hasher) SumInto(out *primitives.Hash) {
	n, err := h.state.Read(out[:])
	assert.NoError(err, "keccak read")
	assert.Equal(n, Size, "keccak read length")
}

func (h *Hasher) Sum() primitives.Hash {
	h.SumInto(&h.out)
	return h.out
}

func (h *Hasher) Hash(b []byte) primitives.Hash {
	if memoizable(b) {
		return memoHash(h, b)
	}
	return h.hashDirect(b)
}

func (h *Hasher) hashDirect(b []byte) primitives.Hash {
	h.state.Reset()
	h.Write(b)
	return h.Sum()
}

func Sum256(b []byte) primitives.Hash {
	if memoizable(b) {
		return memoHash(nil, b)
	}
	h := pooled()
	out := h.hashDirect(b)
	hashers.Put(h)
	return out
}

func Sum256Concat(parts ...[]byte) primitives.Hash {
	total := 0
	for i := 0; i < len(parts); i++ {
		total += len(parts[i])
	}
	if total > 0 && total <= memoInputMax {
		var joined [memoInputMax]byte
		n := 0
		for i := 0; i < len(parts); i++ {
			n += copy(joined[n:], parts[i])
		}
		assert.Equal(n, total, "joined parts fill the short input")
		return memoHash(nil, joined[:n])
	}
	h := pooled()
	h.Reset()
	for i := 0; i < len(parts); i++ {
		h.Write(parts[i])
	}
	out := h.Sum()
	hashers.Put(h)
	return out
}

var hashers = sync.Pool{New: func() any { return New() }}

func pooled() *Hasher {
	h, ok := hashers.Get().(*Hasher)
	assert.Ok(ok, "keccak pool holds hashers")
	return h
}
