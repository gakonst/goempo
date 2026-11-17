package keccak

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

// BatchLanes is how many messages one permutation of a Batch hashes.
const BatchLanes = 8

const blockLanes = BlockSize / 8

// batchMessagesMax bounds one Hash call; callers split longer lists.
const batchMessagesMax = 1 << 20

// BatchBytesMax bounds one message of a Batch.
const BatchBytesMax = 1 << 24

// Batch hashes independent messages eight at a time with one Keccak-f[1600]
// over eight states (keccakf_amd64.s) on CPUs with AVX-512, so a group of
// eight costs about what one hash does. Messages of equal block count share
// permutations best. Without AVX-512 it hashes them one by one.
type Batch struct {
	a      [25][BatchLanes]uint64
	pad    [BlockSize]byte
	blocks [BatchLanes]int
	h      *Hasher
}

func NewBatch() *Batch {
	return &Batch{h: New()}
}

// Hash sets out[i] to the Keccak-256 of msgs[i].
func (b *Batch) Hash(msgs [][]byte, out []primitives.Hash) {
	assert.Equal(len(msgs), len(out), "keccak batch output count")
	assert.Ok(len(msgs) <= batchMessagesMax, "keccak batch longer than batchMessagesMax")
	if !useAVX512 {
		for i := 0; i < len(msgs); i++ {
			out[i] = b.h.Hash(msgs[i])
		}
		return
	}
	groups := (len(msgs) + BatchLanes - 1) / BatchLanes
	for g := 0; g < groups; g++ {
		end := min((g+1)*BatchLanes, len(msgs))
		b.group(msgs[g*BatchLanes:end], out[g*BatchLanes:end])
	}
}

func (b *Batch) group(msgs [][]byte, out []primitives.Hash) {
	assert.Ok(len(msgs) > 0 && len(msgs) <= BatchLanes, "keccak batch group size")
	if len(msgs) == 1 {
		out[0] = b.h.Hash(msgs[0])
		return
	}
	b.a = [25][BatchLanes]uint64{}
	rounds := 0
	for k := 0; k < len(msgs); k++ {
		assert.Ok(len(msgs[k]) <= BatchBytesMax, "keccak batch message longer than BatchBytesMax")
		b.blocks[k] = len(msgs[k])/BlockSize + 1
		rounds = max(rounds, b.blocks[k])
	}
	for j := 0; j < rounds; j++ {
		for k := 0; k < len(msgs); k++ {
			switch {
			case j < b.blocks[k]-1:
				b.absorb(k, msgs[k][j*BlockSize:(j+1)*BlockSize])
			case j == b.blocks[k]-1:
				tail := copy(b.pad[:], msgs[k][j*BlockSize:])
				clear(b.pad[tail:])
				b.pad[tail] = 0x01
				b.pad[BlockSize-1] |= 0x80
				b.absorb(k, b.pad[:])
			}
		}
		keccakF1600x8(&b.a)
		for k := 0; k < len(msgs); k++ {
			if j == b.blocks[k]-1 {
				for i := 0; i < Size/8; i++ {
					binary.LittleEndian.PutUint64(out[k][8*i:], b.a[i][k])
				}
			}
		}
	}
}

func (b *Batch) absorb(k int, block []byte) {
	assert.Equal(len(block), BlockSize, "keccak batch block size")
	for i := 0; i < blockLanes; i++ {
		b.a[i][k] ^= binary.LittleEndian.Uint64(block[8*i:])
	}
}
