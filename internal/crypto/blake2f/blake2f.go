package blake2f

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	InputLength  = 213
	OutputLength = 64
	stateWords   = 8
	blockWords   = 16
	roundsLength = 4
	stateOffset  = roundsLength
	blockOffset  = stateOffset + stateWords*8
	offsetOffset = blockOffset + blockWords*8
	flagOffset   = offsetOffset + 16
	GasPerRound  = 1
	MaxRounds    = 1 << 26
	txGasMax     = 30_000_000
)

const _ = uint(MaxRounds - txGasMax*GasPerRound)

const _ = uint(InputLength - flagOffset - 1)

const _ = uint(flagOffset + 1 - InputLength)

const _ = uint(OutputLength - stateWords*8)

const _ = uint(GasPerRound - 1)

var (
	ErrInputLength   = errors.New("blake2f: input must be 213 bytes")
	ErrFinalFlag     = errors.New("blake2f: final block flag must be 0 or 1")
	ErrTooManyRounds = errors.New("blake2f: more rounds than any transaction's gas pays for")
)

var iv = [stateWords]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

var sigma = [10][blockWords]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
}

func Gas(input []byte) (uint64, error) {
	if len(input) != InputLength {
		return 0, ErrInputLength
	}
	return uint64(binary.BigEndian.Uint32(input[:roundsLength])) * GasPerRound, nil
}

type quarter struct {
	a int
	b int
	c int
	d int
}

var quarters = [8]quarter{
	{a: 0, b: 4, c: 8, d: 12}, {a: 1, b: 5, c: 9, d: 13}, {a: 2, b: 6, c: 10, d: 14}, {a: 3, b: 7, c: 11, d: 15},
	{a: 0, b: 5, c: 10, d: 15}, {a: 1, b: 6, c: 11, d: 12}, {a: 2, b: 7, c: 8, d: 13}, {a: 3, b: 4, c: 9, d: 14},
}

type words struct {
	x uint64
	y uint64
}

type workVector [blockWords]uint64

func (v *workVector) mix(q quarter, w words) {
	v[q.a] = v[q.a] + v[q.b] + w.x
	v[q.d] = bits.RotateLeft64(v[q.d]^v[q.a], -32)
	v[q.c] = v[q.c] + v[q.d]
	v[q.b] = bits.RotateLeft64(v[q.b]^v[q.c], -24)
	v[q.a] = v[q.a] + v[q.b] + w.y
	v[q.d] = bits.RotateLeft64(v[q.d]^v[q.a], -16)
	v[q.c] = v[q.c] + v[q.d]
	v[q.b] = bits.RotateLeft64(v[q.b]^v[q.c], -63)
}

func Compress(input []byte) ([OutputLength]byte, error) {
	var out [OutputLength]byte
	if len(input) != InputLength {
		return out, ErrInputLength
	}
	flag := input[flagOffset]
	if flag > 1 {
		return out, ErrFinalFlag
	}
	rounds := binary.BigEndian.Uint32(input[:roundsLength])
	if rounds > MaxRounds {
		return out, ErrTooManyRounds
	}
	var h [stateWords]uint64
	var m [blockWords]uint64
	for i := range h {
		h[i] = binary.LittleEndian.Uint64(input[stateOffset+8*i:])
	}
	for i := range m {
		m[i] = binary.LittleEndian.Uint64(input[blockOffset+8*i:])
	}
	var v workVector
	copy(v[:stateWords], h[:])
	copy(v[stateWords:], iv[:])
	v[12] ^= binary.LittleEndian.Uint64(input[offsetOffset:])
	v[13] ^= binary.LittleEndian.Uint64(input[offsetOffset+8:])
	if flag == 1 {
		v[14] = ^v[14]
	}
	for r := uint32(0); r < rounds; r++ {
		s := &sigma[r%10]
		for i, q := range quarters {
			v.mix(q, words{x: m[s[2*i]], y: m[s[2*i+1]]})
		}
	}
	for i := range h {
		h[i] ^= v[i] ^ v[i+stateWords]
		binary.LittleEndian.PutUint64(out[8*i:], h[i])
	}
	return out, nil
}
