package rlp

import (
	"errors"
)

const DepthMax = 32

const deepestTempoStructure = 6

const _ = uint(DepthMax - deepestTempoStructure)

const shortMax = 55

const (
	offsetString     = 0x80
	offsetLongString = 0xb7
	offsetList       = 0xc0
	offsetLongList   = 0xf7
)

const _ = uint(offsetLongString - offsetString - shortMax)

type Kind uint8

const (
	KindString Kind = 1
	KindList   Kind = 2
)

var (
	ErrUnexpectedEOF    = errors.New("rlp: input ends inside an item")
	ErrNonCanonicalSize = errors.New("rlp: length is not in its shortest form")
	ErrSingleByte       = errors.New("rlp: a byte below 0x80 must encode as itself")
	ErrLeadingZero      = errors.New("rlp: integer has a leading zero byte")
	ErrUintOverflow     = errors.New("rlp: integer is too large")
	ErrExpectedString   = errors.New("rlp: expected a string, found a list")
	ErrExpectedList     = errors.New("rlp: expected a list, found a string")
	ErrTrailingBytes    = errors.New("rlp: input has bytes after the last item")
	ErrTooDeep          = errors.New("rlp: lists nest deeper than DepthMax")
	ErrLength           = errors.New("rlp: string has the wrong length")
	ErrEmpty            = errors.New("rlp: no item left")
)

func BytesSize(b []byte) int {
	if len(b) == 1 {
		if b[0] < offsetString {
			return 1
		}
	}
	return headerSize(len(b)) + len(b)
}

func ListSize(payloadLen int) int {
	return headerSize(payloadLen) + payloadLen
}

func Uint64Size(v uint64) int {
	if v < offsetString {
		return 1
	}
	return 1 + uint64Len(uint64(v))
}

func headerSize(payloadLen int) int {
	if payloadLen <= shortMax {
		return 1
	}
	return 1 + uint64Len(uint64(payloadLen))
}

func uint64Len(v uint64) int {
	n := 0
	for k := 0; k < 8; k++ {
		if v>>(8*k) != 0 {
			n = k + 1
		}
	}
	return n
}
