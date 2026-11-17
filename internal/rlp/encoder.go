package rlp

import (
	"github.com/kapetan-io/tiger/assert"
)

type Encoder struct {
	buf    []byte
	starts [DepthMax]int
	depth  int
}

func NewEncoder(capacity int) *Encoder {
	return &Encoder{buf: make([]byte, 0, capacity)}
}

func (e *Encoder) Reset() {
	e.buf = e.buf[:0]
	e.depth = 0
}

func (e *Encoder) Encoded() []byte {
	assert.Equal(e.depth, 0, "rlp encoder has an open list")
	return e.buf
}

func (e *Encoder) Len() int {
	return len(e.buf)
}

// Cap is the capacity of the encoder's buffer, for pools that bound what
// they retain.
func (e *Encoder) Cap() int {
	return cap(e.buf)
}

// Bytes returns the encoder's whole buffer, including any open list's
// content; for callers that slice several items out of one encoder.
func (e *Encoder) Bytes() []byte {
	return e.buf
}

func (e *Encoder) WriteBytes(b []byte) {
	if len(b) == 1 {
		if b[0] < offsetString {
			e.buf = append(e.buf, b[0])
			return
		}
	}
	e.buf = appendHeader(e.buf, header{offset: offsetString, size: len(b)})
	e.buf = append(e.buf, b...)
}

func (e *Encoder) WriteString(s string) {
	e.WriteBytes([]byte(s))
}

func (e *Encoder) WriteUint64(v uint64) {
	if v == 0 {
		e.buf = append(e.buf, offsetString)
		return
	}
	if v < offsetString {
		e.buf = append(e.buf, byte(v))
		return
	}
	n := uint64Len(v)
	e.buf = append(e.buf, byte(offsetString+n))
	for k := n - 1; k >= 0; k-- {
		e.buf = append(e.buf, byte(v>>(8*k)))
	}
}

func (e *Encoder) WriteUintBytes(bigEndian []byte) {
	i := 0
	for ; i < len(bigEndian); i++ {
		if bigEndian[i] != 0 {
			break
		}
	}
	e.WriteBytes(bigEndian[i:])
}

func (e *Encoder) WriteRaw(encoded []byte) {
	e.buf = append(e.buf, encoded...)
}

func (e *Encoder) ListStart() {
	assert.Ok(e.depth < DepthMax, "rlp encoder lists nest deeper than DepthMax")
	e.starts[e.depth] = len(e.buf)
	e.depth++
}

func (e *Encoder) ListEnd() {
	assert.Ok(e.depth > 0, "rlp ListEnd without ListStart")
	e.depth--
	start := e.starts[e.depth]
	size := len(e.buf) - start
	hs := headerSize(size)
	e.buf = append(e.buf, make([]byte, hs)...)
	copy(e.buf[start+hs:], e.buf[start:start+size])
	head := appendHeader(e.buf[start:start], header{offset: offsetList, size: size})
	assert.Equal(len(head), hs, "rlp list header size")
}

type header struct {
	offset int
	size   int
}

func appendHeader(dst []byte, h header) []byte {
	if h.size <= shortMax {
		return append(dst, byte(h.offset+h.size))
	}
	n := uint64Len(uint64(h.size))
	dst = append(dst, byte(h.offset+shortMax+n))
	for k := n - 1; k >= 0; k-- {
		dst = append(dst, byte(uint64(h.size)>>(8*k)))
	}
	return dst
}

func EncodeBytes(b []byte) []byte {
	e := Encoder{buf: make([]byte, 0, BytesSize(b))}
	e.WriteBytes(b)
	return e.Encoded()
}

func EncodeUint64(v uint64) []byte {
	e := Encoder{buf: make([]byte, 0, 9)}
	e.WriteUint64(v)
	return e.Encoded()
}

func EncodeList(items ...[]byte) []byte {
	size := 0
	for i := 0; i < len(items); i++ {
		size += len(items[i])
	}
	e := Encoder{buf: make([]byte, 0, ListSize(size))}
	e.ListStart()
	for i := 0; i < len(items); i++ {
		e.WriteRaw(items[i])
	}
	e.ListEnd()
	return e.Encoded()
}
