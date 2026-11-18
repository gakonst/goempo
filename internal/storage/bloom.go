package storage

import "math/bits"

const bloomBitsPerKey = 10

const bloomProbes = 7

const bloomBlockWords = 8

const bloomLegacy = 0

const bloomBlocked = 1

const _ = uint(bloomBitsPerKey - bloomProbes)

type bloom struct {
	words   []uint64
	version uint64
}

func newBloom(keys int) bloom {
	bitCount := max(keys*bloomBitsPerKey, 64*bloomBlockWords)
	blocks := (bitCount + 64*bloomBlockWords - 1) / (64 * bloomBlockWords)
	return bloom{words: make([]uint64, blocks*bloomBlockWords), version: bloomBlocked}
}

func legacyKeyHash(key []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range key {
		h ^= uint64(c)
		h *= 1099511628211
	}
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

type bloomSpot struct {
	base  int
	probe uint64
}

func (b bloom) blockBits(h uint64) bloomSpot {
	blocks := uint64(len(b.words) / bloomBlockWords)
	block, _ := bits.Mul64(h, blocks)
	return bloomSpot{base: int(block) * bloomBlockWords, probe: hashMix(h, hashMul2)}
}

func (b bloom) addHash(h uint64) {
	spot := b.blockBits(h)
	base, probe := spot.base, spot.probe
	for i := 0; i < bloomProbes; i++ {
		bit := probe >> (9 * i) & 511
		b.words[base+int(bit/64)] |= 1 << (bit % 64)
	}
}

func (b bloom) mayContainHash(h uint64) bool {
	spot := b.blockBits(h)
	base, probe := spot.base, spot.probe
	for i := 0; i < bloomProbes; i++ {
		bit := probe >> (9 * i) & 511
		if b.words[base+int(bit/64)]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

func (b bloom) mayContainLegacy(h uint64) bool {
	step := h>>32 | 1
	bitCount := uint64(len(b.words)) * 64
	for i := 0; i < bloomProbes; i++ {
		bit := (h + uint64(i)*step) % bitCount
		if b.words[bit/64]&(1<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

type probeKey struct {
	key    []byte
	hash   uint64
	legacy uint64
	hasOld bool
}

func newProbeKey(key []byte) probeKey {
	return probeKey{key: key, hash: hashKey(key)}
}

func (b bloom) mayContain(p *probeKey) bool {
	if b.version == bloomBlocked {
		return b.mayContainHash(p.hash)
	}
	if !p.hasOld {
		p.legacy = legacyKeyHash(p.key)
		p.hasOld = true
	}
	return b.mayContainLegacy(p.legacy)
}
