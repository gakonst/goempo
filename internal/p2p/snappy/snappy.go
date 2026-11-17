// Package snappy reads and writes the Snappy block format that devp2p
// compresses every message after Hello with. Decode takes any valid block;
// Encode writes literals only, which every decoder accepts.
package snappy

import (
	"encoding/binary"
	"errors"
)

const DecodedMax = 16 << 20

const literalChunkMax = 1 << 16

const copyLengthMax = 64

const _ = uint(DecodedMax - literalChunkMax)

const _ = uint(literalChunkMax - copyLengthMax)

var (
	ErrCorrupt  = errors.New("snappy: corrupt input")
	ErrTooLarge = errors.New("snappy: decoded length exceeds the limit")
)

func DecodedLen(src []byte) (int, error) {
	n, size := binary.Uvarint(src)
	if size <= 0 {
		return 0, ErrCorrupt
	}
	if n > DecodedMax {
		return 0, ErrTooLarge
	}
	return int(n), nil
}

type decoder struct {
	src  []byte
	pos  int
	dst  []byte
	want int
}

func Decode(src []byte) ([]byte, error) {
	want, err := DecodedLen(src)
	if err != nil {
		return nil, err
	}
	_, size := binary.Uvarint(src)
	d := decoder{src: src, pos: size, dst: make([]byte, 0, want), want: want}
	steps := 0
	for ; d.pos < len(src) && steps < len(src); steps++ {
		if err := d.element(); err != nil {
			return nil, err
		}
	}
	if d.pos != len(src) || len(d.dst) != want {
		return nil, ErrCorrupt
	}
	return d.dst, nil
}

func (d *decoder) element() error {
	tag := d.src[d.pos]
	d.pos++
	switch tag & 3 {
	case 0:
		return d.literal(tag)
	case 1:
		if d.pos >= len(d.src) {
			return ErrCorrupt
		}
		offset := int(tag>>5)<<8 | int(d.src[d.pos])
		d.pos++
		return d.copyBack(offset, copyLength(4+tag>>2&7))
	case 2:
		if d.pos+2 > len(d.src) {
			return ErrCorrupt
		}
		offset := int(binary.LittleEndian.Uint16(d.src[d.pos:]))
		d.pos += 2
		return d.copyBack(offset, copyLength(tag>>2)+1)
	default:
		if d.pos+4 > len(d.src) {
			return ErrCorrupt
		}
		offset := binary.LittleEndian.Uint32(d.src[d.pos:])
		d.pos += 4
		if offset > DecodedMax {
			return ErrCorrupt
		}
		return d.copyBack(int(offset), copyLength(tag>>2)+1)
	}
}

func (d *decoder) literal(tag byte) error {
	length := int(tag >> 2)
	if length >= 60 {
		extra := length - 59
		if d.pos+extra > len(d.src) {
			return ErrCorrupt
		}
		var le [4]byte
		copy(le[:], d.src[d.pos:d.pos+extra])
		wide := binary.LittleEndian.Uint32(le[:])
		if wide >= DecodedMax {
			return ErrCorrupt
		}
		length = int(wide)
		d.pos += extra
	}
	length++
	if length > len(d.src)-d.pos || length > d.want-len(d.dst) {
		return ErrCorrupt
	}
	d.dst = append(d.dst, d.src[d.pos:d.pos+length]...)
	d.pos += length
	return nil
}

func (d *decoder) copyBack(offset int, length copyLength) error {
	if offset == 0 || offset > len(d.dst) || int(length) > d.want-len(d.dst) || length > copyLengthMax {
		return ErrCorrupt
	}
	start := len(d.dst) - offset
	for i := range min(int(length), copyLengthMax) {
		d.dst = append(d.dst, d.dst[start+i])
	}
	return nil
}

type encoder struct {
	out []byte
}

func Encode(src []byte) []byte {
	e := encoder{out: make([]byte, 0, binary.MaxVarintLen64+len(src)+(len(src)/literalChunkMax+1)*3)}
	e.out = binary.AppendUvarint(e.out, uint64(len(src)))
	for start := 0; start < len(src); start += literalChunkMax {
		e.literal(src[start:min(start+literalChunkMax, len(src))])
	}
	return e.out
}

func (e *encoder) literal(chunk []byte) {
	n := len(chunk) - 1
	switch {
	case n < 60:
		e.out = append(e.out, byte(n)<<2)
	case n < 1<<8:
		e.out = append(e.out, 60<<2, byte(n))
	default:
		e.out = append(e.out, 61<<2, byte(n), byte(n>>8))
	}
	e.out = append(e.out, chunk...)
}

type copyLength uint8
