package storage

import (
	"encoding/binary"
	"math/bits"
)

const (
	hashMul0 = 0xa0761d6478bd642f
	hashMul1 = 0xe7037ed1a0b428db
	hashMul2 = 0x8ebc6af09c88c6e3
)

func hashMix(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

func hashKey(key []byte) uint64 {
	h := uint64(len(key)) ^ hashMul2
	i := 0
	for ; i+8 <= len(key); i += 8 {
		h = hashMix(h^binary.LittleEndian.Uint64(key[i:]), hashMul0)
	}
	var tail uint64
	for j := len(key) - 1; j >= i; j-- {
		tail = tail<<8 | uint64(key[j])
	}
	return hashMix(hashMix(h^tail, hashMul1), hashMul0^uint64(len(key)))
}
