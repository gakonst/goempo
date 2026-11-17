package consensus

import (
	"crypto/sha256"
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

type ElectorVersion uint8

const (
	ElectorV0 ElectorVersion = iota
	ElectorV1
)

const byteBits = 8

func Modulo(b []byte, n uint64) uint64 {
	assert.Ok(n > 0, "consensus: modulo by a positive count")
	var r uint64
	for _, c := range b {
		r = bits.Rem64(r>>(64-byteBits), r<<byteBits|uint64(c), n)
	}
	return r
}

type Election struct {
	Version ElectorVersion
	Round   Round
	N       int
	Seed    *[SignatureBytes]byte
}

func (e Election) Leader() int {
	assert.Ok(e.N > 0, "consensus: election has participants")
	if e.Seed == nil {
		return int((e.Round.Epoch + e.Round.View) % uint64(e.N))
	}
	if e.Version == ElectorV0 {
		return int(Modulo(e.Seed[:], uint64(e.N)))
	}
	h := sha256.Sum256(e.Seed[:])
	return int(Modulo(h[:], uint64(e.N)))
}
