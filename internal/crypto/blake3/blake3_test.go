package blake3_test

import (
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/blake3"
	"github.com/kapetan-io/tiger/assert"
)

func pattern(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i % 251)
	}
	return out
}

func TestSumMatchesReferenceVectors(_ *testing.T) {
	empty := blake3.Sum(nil)
	assert.Equal(hex.EncodeToString(empty[:]), "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262", "empty input")
	two := blake3.Sum(pattern(1025))
	assert.Equal(hex.EncodeToString(two[:]), "d00278ae47eb27b34faecf67b4fe263f82d5412916c1ffd97c8cb7fb814b8444", "two chunks")
	h := blake3.New()
	h.Write(pattern(5000))
	long := make([]byte, 20)
	h.XOF(long)
	assert.Equal(hex.EncodeToString(long), "ee78d92070de3df1c57c37002abf0a6b1a6589ac", "five chunks")
}

func TestIncrementalWritesAndCloneMatchOneShot(_ *testing.T) {
	data := pattern(3000)
	h := blake3.New()
	h.Write(data[:7])
	c := h.Clone()
	h.Write(data[7:1500])
	h.Write(data[1500:])
	assert.Equal(h.Sum(), blake3.Sum(data), "split writes")
	c.Write(data[7:])
	assert.Equal(c.Sum(), blake3.Sum(data), "clone keeps its own state")
}
