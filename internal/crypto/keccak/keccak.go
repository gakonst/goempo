// Package keccak computes Keccak-256, the pre-standard SHA-3 variant Ethereum
// and Tempo hash with (padding byte 0x01, not FIPS 202's 0x06).
//
// Sum256 allocates one hashing state per call. A Hasher owns its state and
// computes any number of hashes with no allocation: Reset, Write, then Sum or
// SumInto; Hash does all three for one input.
package keccak

import (
	"hash"

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
	var out primitives.Hash
	h.SumInto(&out)
	return out
}

func (h *Hasher) Hash(b []byte) primitives.Hash {
	h.state.Reset()
	h.Write(b)
	return h.Sum()
}

func Sum256(b []byte) primitives.Hash {
	return New().Hash(b)
}

func Sum256Concat(parts ...[]byte) primitives.Hash {
	h := New()
	for i := 0; i < len(parts); i++ {
		h.Write(parts[i])
	}
	return h.Sum()
}
