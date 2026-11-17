package rpc

import (
	"strconv"
	"unicode/utf8"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

const hexDigits = "0123456789abcdef"

type field string

type jbool bool

type writer struct {
	buf   []byte
	comma bool
}

func newWriter(capacity int) *writer {
	return &writer{buf: make([]byte, 0, capacity)}
}

func (w *writer) bytes() []byte { return w.buf }

func (w *writer) sep() {
	if w.comma {
		w.buf = append(w.buf, ',')
	}
}

func (w *writer) objOpen() {
	w.sep()
	w.buf = append(w.buf, '{')
	w.comma = false
}

func (w *writer) objClose() {
	w.buf = append(w.buf, '}')
	w.comma = true
}

func (w *writer) arrOpen() {
	w.sep()
	w.buf = append(w.buf, '[')
	w.comma = false
}

func (w *writer) arrClose() {
	w.buf = append(w.buf, ']')
	w.comma = true
}

func (w *writer) key(k field) {
	w.sep()
	w.buf = append(w.buf, '"')
	w.buf = append(w.buf, k...)
	w.buf = append(w.buf, '"', ':')
	w.comma = false
}

func (w *writer) raw(b []byte) {
	w.sep()
	w.buf = append(w.buf, b...)
	w.comma = true
}

func (w *writer) null() { w.raw(resultNull) }

func (w *writer) boolean(v jbool) {
	if v {
		w.raw(resultTrue)
		return
	}
	w.raw(resultFalse)
}

func (w *writer) integer(v int64) {
	w.sep()
	w.buf = strconv.AppendInt(w.buf, v, 10)
	w.comma = true
}

func (w *writer) str(s string) {
	w.sep()
	w.buf = appendQuoted(w.buf, s)
	w.comma = true
}

func appendQuoted(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i, r := range s {
		switch {
		case r < utf8.RuneSelf:
			dst = appendASCII(dst, byte(r))
		case r == utf8.RuneError:
			dst = appendInvalid(dst, s[i:])
		default:
			dst = utf8.AppendRune(dst, r)
		}
	}
	return append(dst, '"')
}

func appendInvalid(dst []byte, rest string) []byte {
	r, size := utf8.DecodeRuneInString(rest)
	if size == 1 {
		return append(dst, "\\ufffd"...)
	}
	return utf8.AppendRune(dst, r)
}

func appendASCII(dst []byte, c byte) []byte {
	switch c {
	case '"':
		return append(dst, '\\', '"')
	case '\\':
		return append(dst, '\\', '\\')
	case '\n':
		return append(dst, '\\', 'n')
	case '\r':
		return append(dst, '\\', 'r')
	case '\t':
		return append(dst, '\\', 't')
	}
	if c < 0x20 {
		return append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
	}
	return append(dst, c)
}

func (w *writer) quantity(v uint64) {
	w.sep()
	w.buf = append(w.buf, '"', '0', 'x')
	w.buf = strconv.AppendUint(w.buf, v, 16)
	w.buf = append(w.buf, '"')
	w.comma = true
}

func (w *writer) quantity256(v u256.Int) {
	w.sep()
	w.buf = append(w.buf, '"')
	w.buf = append(w.buf, v.Hex()...)
	w.buf = append(w.buf, '"')
	w.comma = true
}

func (w *writer) quantityBytes(b []byte) {
	w.quantity256(u256.FromBytes(b))
}

func (w *writer) data(b []byte) {
	w.sep()
	w.buf = append(w.buf, '"', '0', 'x')
	for _, c := range b {
		w.buf = append(w.buf, hexDigits[c>>4], hexDigits[c&0xf])
	}
	w.buf = append(w.buf, '"')
	w.comma = true
}

func (w *writer) hash(h primitives.Hash) { w.data(h[:]) }

func (w *writer) address(a primitives.Address) { w.data(a[:]) }

func (w *writer) addressPtr(a *primitives.Address) {
	if a == nil {
		w.null()
		return
	}
	w.address(*a)
}

func (w *writer) kQuantity(k field, v uint64) {
	w.key(k)
	w.quantity(v)
}

func (w *writer) kQuantity256(k field, v u256.Int) {
	w.key(k)
	w.quantity256(v)
}

func (w *writer) kData(k field, b []byte) {
	w.key(k)
	w.data(b)
}

func (w *writer) kHash(k field, h primitives.Hash) {
	w.key(k)
	w.hash(h)
}

func (w *writer) kAddress(k field, a primitives.Address) {
	w.key(k)
	w.address(a)
}

func (w *writer) kStr(k field, s string) {
	w.key(k)
	w.str(s)
}

func (w *writer) kBool(k field, v jbool) {
	w.key(k)
	w.boolean(v)
}

func (w *writer) kNull(k field) {
	w.key(k)
	w.null()
}
