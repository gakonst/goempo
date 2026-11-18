package storage

import (
	"encoding/binary"
	"math/bits"
)

type hashSeed uint64

const (
	hashMul0 hashSeed = 0xa0761d6478bd642f
	hashMul1 hashSeed = 0xe7037ed1a0b428db
	hashMul2 hashSeed = 0x8ebc6af09c88c6e3
)

func hashMix(a uint64, b hashSeed) uint64 {
	hi, lo := bits.Mul64(a, uint64(b))
	return hi ^ lo
}

func hashKey(key []byte) uint64 {
	h := uint64(len(key)) ^ uint64(hashMul2)
	words := len(key) / 8
	for i := range words {
		h = hashMix(h^binary.LittleEndian.Uint64(key[8*i:]), hashMul0)
	}
	rest := key[8*words:]
	var tail uint64
	for i := range rest {
		tail |= uint64(rest[i]) << (8 * i)
	}
	return hashMix(hashMix(h^tail, hashMul1), hashMul0^hashSeed(len(key)))
}
