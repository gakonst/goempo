package primitives

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

var ErrUint64Overflow = errors.New("primitives: big-endian value does not fit in 64 bits")

func Uint64Len(v uint64) int {
	return (bits.Len64(v) + 7) / 8
}

func AppendUint64Minimal(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[8-Uint64Len(v):]...)
}

func Uint64FromBytes(b []byte) (uint64, error) {
	if len(b) > 8 {
		return 0, ErrUint64Overflow
	}
	var buf [8]byte
	copy(buf[8-len(b):], b)
	return binary.BigEndian.Uint64(buf[:]), nil
}

func PutUint64(dst []byte, v uint64) {
	binary.BigEndian.PutUint64(dst, v)
}

func Uint64(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

func TrimLeftZeros(b []byte) []byte {
	i := 0
	for ; i < len(b); i++ {
		if b[i] != 0 {
			break
		}
	}
	return b[i:]
}

func Uint64ToHash(v uint64) Hash {
	var h Hash
	binary.BigEndian.PutUint64(h[HashLength-8:], v)
	return h
}
