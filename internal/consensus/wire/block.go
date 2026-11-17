package wire

import (
	"errors"
	"math"
)

const (
	rlpListShort    = 0xc0
	rlpListLong     = 0xf7
	rlpLengthMax    = 8
	rlpShortLimit   = 56
	byteBits        = 8
	blockEncodedMax = 1 << 26
)

const _ = uint(uint64Bytes - rlpLengthMax)

const _ = uint(math.MaxInt32 - blockEncodedMax)

var ErrBlock = errors.New("wire: block is not an RLP list")

func BlockLen(b []byte) (int, error) {
	r := reader{data: b}
	first, err := r.byte1()
	if err != nil {
		return 0, err
	}
	if first < rlpListShort {
		return 0, ErrBlock
	}
	header := 1
	size := uint64(first - rlpListShort)
	if first > rlpListLong {
		n := int(first - rlpListLong)
		raw, err := r.fixed(n)
		if err != nil {
			return 0, err
		}
		if raw[0] == 0 || n > rlpLengthMax {
			return 0, ErrBlock
		}
		size = 0
		for _, c := range raw {
			size = size<<byteBits | uint64(c)
		}
		if size < rlpShortLimit {
			return 0, ErrBlock
		}
		header += n
	}
	if size > blockEncodedMax || size > uint64(len(r.data)) {
		return 0, ErrShort
	}
	return header + int(size), nil
}

func DecodeBlock(b []byte) ([]byte, error) {
	n, err := BlockLen(b)
	if err != nil {
		return nil, err
	}
	if n != len(b) {
		return nil, ErrLeftover
	}
	return b, nil
}

func EncodeBlock(block []byte) []byte {
	return append([]byte(nil), block...)
}
