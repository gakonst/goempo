package dkg

import (
	"encoding/binary"
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

const (
	blake3BlockLen  = 64
	blake3ChunkLen  = 1024
	blake3KeyLen    = 32
	blake3OutLen    = 32
	blake3StackMax  = 54
	blake3Rounds    = 7
	blake3Words     = 16
	blake3CVWords   = 8
	flagChunkStart  = 1
	flagChunkEnd    = 2
	flagParent      = 4
	flagRoot        = 8
	flagKeyedHash   = 16
	blake3OutputMax = 1 << 20
	blake3UpdateMax = 1 << 26
	blake3ChunksMax = blake3UpdateMax/blake3ChunkLen + 1
	blake3BlocksMax = blake3OutputMax/blake3BlockLen + 2
)

const _ = uint(blake3ChunkLen/blake3BlockLen - blake3Words)

const _ = uint(64 - blake3StackMax)

const _ = uint(blake3UpdateMax - encodedMax)

const _ = uint(blake3OutputMax - blake3BlockLen)

const _ = uint(blake3ChunksMax - blake3UpdateMax/blake3ChunkLen)

const _ = uint(blake3BlocksMax - blake3OutputMax/blake3BlockLen)

var blake3IV = [blake3CVWords]uint32{
	0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A, 0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
}

var blake3Perm = [blake3Words]int{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}

type quarter struct{ a, b, c, d int }

func blake3G(s *[blake3Words]uint32, q quarter, m [2]uint32) {
	mx, my := m[0], m[1]
	s[q.a] = s[q.a] + s[q.b] + mx
	s[q.d] = bits.RotateLeft32(s[q.d]^s[q.a], -16)
	s[q.c] = s[q.c] + s[q.d]
	s[q.b] = bits.RotateLeft32(s[q.b]^s[q.c], -12)
	s[q.a] = s[q.a] + s[q.b] + my
	s[q.d] = bits.RotateLeft32(s[q.d]^s[q.a], -8)
	s[q.c] = s[q.c] + s[q.d]
	s[q.b] = bits.RotateLeft32(s[q.b]^s[q.c], -7)
}

var blake3Quarters = [8]quarter{
	{0, 4, 8, 12}, {1, 5, 9, 13}, {2, 6, 10, 14}, {3, 7, 11, 15},
	{0, 5, 10, 15}, {1, 6, 11, 12}, {2, 7, 8, 13}, {3, 4, 9, 14},
}

func blake3Round(s *[blake3Words]uint32, m [blake3Words]uint32) {
	for i, q := range blake3Quarters {
		blake3G(s, q, [2]uint32{m[2*i], m[2*i+1]})
	}
}

type compressInput struct {
	cv       [blake3CVWords]uint32
	block    [blake3Words]uint32
	counter  uint64
	blockLen uint32
	flags    uint32
}

func blake3Compress(in compressInput) [blake3Words]uint32 {
	s := [blake3Words]uint32{
		in.cv[0], in.cv[1], in.cv[2], in.cv[3], in.cv[4], in.cv[5], in.cv[6], in.cv[7],
		blake3IV[0], blake3IV[1], blake3IV[2], blake3IV[3],
		uint32(in.counter), uint32(in.counter >> 32), in.blockLen, in.flags,
	}
	m := in.block
	for r := range blake3Rounds {
		blake3Round(&s, m)
		if r == blake3Rounds-1 {
			break
		}
		var next [blake3Words]uint32
		for i := range blake3Words {
			next[i] = m[blake3Perm[i]]
		}
		m = next
	}
	for i := range blake3CVWords {
		s[i] ^= s[i+blake3CVWords]
		s[i+blake3CVWords] ^= in.cv[i]
	}
	return s
}

func blockWords(b *[blake3BlockLen]byte) [blake3Words]uint32 {
	var w [blake3Words]uint32
	for i := range blake3Words {
		w[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	return w
}

func firstEight(w [blake3Words]uint32) [blake3CVWords]uint32 {
	return [blake3CVWords]uint32(w[:blake3CVWords])
}

type blake3Output struct {
	in compressInput
}

func (o blake3Output) chainingValue() [blake3CVWords]uint32 {
	return firstEight(blake3Compress(o.in))
}

func (o blake3Output) root(seek uint64, out []byte) {
	assert.Ok(len(out) <= blake3OutputMax, "dkg: blake3 output within bound")
	counter := seek / blake3BlockLen
	skip := int(seek % blake3BlockLen)
	written := 0
	for range blake3BlocksMax {
		if written == len(out) {
			return
		}
		in := o.in
		in.counter = counter
		in.flags |= flagRoot
		words := blake3Compress(in)
		var block [blake3BlockLen]byte
		for i := range blake3Words {
			binary.LittleEndian.PutUint32(block[4*i:], words[i])
		}
		written += copy(out[written:], block[skip:])
		skip = 0
		counter++
	}
	assert.Ok(written == len(out), "dkg: blake3 output filled")
}

type blake3Chunk struct {
	cv        [blake3CVWords]uint32
	counter   uint64
	block     [blake3BlockLen]byte
	blockLen  int
	compacted int
	flags     uint32
}

func (c *blake3Chunk) len() int { return c.compacted*blake3BlockLen + c.blockLen }

func (c *blake3Chunk) startFlag() uint32 {
	if c.compacted == 0 {
		return flagChunkStart
	}
	return 0
}

func (c *blake3Chunk) update(data []byte) {
	assert.Ok(c.len()+len(data) <= blake3ChunkLen, "dkg: blake3 chunk within its length")
	for range blake3Words + 1 {
		if len(data) == 0 {
			return
		}
		if c.blockLen == blake3BlockLen {
			words := blake3Compress(compressInput{
				cv: c.cv, block: blockWords(&c.block), counter: c.counter,
				blockLen: blake3BlockLen, flags: c.flags | c.startFlag(),
			})
			c.cv = firstEight(words)
			c.compacted++
			c.block = [blake3BlockLen]byte{}
			c.blockLen = 0
		}
		n := copy(c.block[c.blockLen:], data)
		c.blockLen += n
		data = data[n:]
	}
	assert.Ok(len(data) == 0, "dkg: blake3 chunk consumed its input")
}

func (c *blake3Chunk) output() blake3Output {
	return blake3Output{in: compressInput{
		cv: c.cv, block: blockWords(&c.block), counter: c.counter,
		blockLen: uint32(c.blockLen), flags: c.flags | c.startFlag() | flagChunkEnd,
	}}
}

type parentNode struct {
	left  [blake3CVWords]uint32
	right [blake3CVWords]uint32
}

type blake3Hasher struct {
	key      [blake3CVWords]uint32
	flags    uint32
	chunk    blake3Chunk
	stack    [blake3StackMax][blake3CVWords]uint32
	stackLen int
}

func (h *blake3Hasher) parent(n parentNode) blake3Output {
	var block [blake3Words]uint32
	copy(block[:blake3CVWords], n.left[:])
	copy(block[blake3CVWords:], n.right[:])
	return blake3Output{in: compressInput{cv: h.key, block: block, blockLen: blake3BlockLen, flags: h.flags | flagParent}}
}

func newBlake3() blake3Hasher {
	return blake3Hasher{key: blake3IV, chunk: blake3Chunk{cv: blake3IV}}
}

func newBlake3Keyed(key [blake3KeyLen]byte) blake3Hasher {
	var k [blake3CVWords]uint32
	for i := range blake3CVWords {
		k[i] = binary.LittleEndian.Uint32(key[4*i:])
	}
	return blake3Hasher{key: k, flags: flagKeyedHash, chunk: blake3Chunk{cv: k, flags: flagKeyedHash}}
}

func (h *blake3Hasher) pushChunk(cv [blake3CVWords]uint32, total uint64) {
	for range blake3StackMax {
		if total&1 != 0 {
			break
		}
		assert.Ok(h.stackLen > 0, "dkg: blake3 stack holds a left child")
		h.stackLen--
		cv = h.parent(parentNode{left: h.stack[h.stackLen], right: cv}).chainingValue()
		total >>= 1
	}
	assert.Ok(h.stackLen < blake3StackMax, "dkg: blake3 stack within bound")
	h.stack[h.stackLen] = cv
	h.stackLen++
}

func (h *blake3Hasher) write(data []byte) {
	assert.Ok(len(data) <= blake3UpdateMax, "dkg: blake3 update within bound")
	for range blake3ChunksMax + 1 {
		if len(data) == 0 {
			return
		}
		if h.chunk.len() == blake3ChunkLen {
			total := h.chunk.counter + 1
			h.pushChunk(h.chunk.output().chainingValue(), total)
			h.chunk = blake3Chunk{cv: h.key, counter: total, flags: h.flags}
		}
		n := min(blake3ChunkLen-h.chunk.len(), len(data))
		h.chunk.update(data[:n])
		data = data[n:]
	}
	assert.Ok(len(data) == 0, "dkg: blake3 consumed its input")
}

func (h *blake3Hasher) rootOutput() blake3Output {
	out := h.chunk.output()
	for i := range h.stackLen {
		out = h.parent(parentNode{left: h.stack[h.stackLen-1-i], right: out.chainingValue()})
	}
	return out
}

func (h *blake3Hasher) sum() [blake3OutLen]byte {
	var out [blake3OutLen]byte
	h.rootOutput().root(0, out[:])
	return out
}

func Blake3(data []byte) [blake3OutLen]byte {
	h := newBlake3()
	h.write(data)
	return h.sum()
}

func Blake3XOF(data []byte, n int) []byte {
	h := newBlake3()
	h.write(data)
	out := make([]byte, n)
	h.rootOutput().root(0, out)
	return out
}

func Blake3KeyedXOF(key [blake3KeyLen]byte, data []byte, n int) []byte {
	h := newBlake3Keyed(key)
	h.write(data)
	out := make([]byte, n)
	h.rootOutput().root(0, out)
	return out
}
