package precompiles

import (
	"errors"
	"unicode/utf8"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

var errABI = errors.New("precompile: abi decode")

type scalarKind uint8

const (
	kAddress scalarKind = iota
	kBool
	kUint
	kInt
	kBytesN
)

type scalar struct {
	kind scalarKind
	size int
}

type argKind uint8

const (
	argScalar argKind = iota
	argBytes
	argString
	argTupleArray
)

type abiType struct {
	kind   argKind
	scalar scalar
	fields []scalar
}

func scalarArg(k scalarKind, size int) abiType {
	return abiType{kind: argScalar, scalar: scalar{kind: k, size: size}}
}

var (
	tAddress = scalarArg(kAddress, 160)
	tBool    = scalarArg(kBool, 8)
	tU8      = scalarArg(kUint, 8)
	tU32     = scalarArg(kUint, 32)
	tU64     = scalarArg(kUint, 64)
	tU128    = scalarArg(kUint, 128)
	tU256    = scalarArg(kUint, 256)
	tI16     = scalarArg(kInt, 16)
	tI32     = scalarArg(kInt, 32)
	tB32     = scalarArg(kBytesN, 32)
	tBytes   = abiType{kind: argBytes}
	tString  = abiType{kind: argString}
)

func tTupleArray(fields ...scalar) abiType { return abiType{kind: argTupleArray, fields: fields} }

func tScalarArray(s abiType) abiType { return tTupleArray(s.scalar) }

type abiValue struct {
	word  Hash
	data  []byte
	items [][]Hash
}

func (v abiValue) addr() Address { return wordAddress(v.word) }
func (v abiValue) u256() Word    { return toWord(v.word) }
func (v abiValue) u64() uint64   { return toWord(v.word).Uint64() }
func (v abiValue) boolean() bool { return !v.word.IsZero() }
func (v abiValue) bytes32() Hash { return v.word }
func (v abiValue) u8() uint8     { return uint8(toWord(v.word).Uint64()) }

type decodeMode struct {
	strict   flag
	trailing flag
}

const maxArrayItems = 1 << 16

func decodeABI(data []byte, types []abiType, mode decodeMode) ([]abiValue, error) {
	head := 32 * len(types)
	if head > len(data) {
		return nil, errABI
	}
	vals := make([]abiValue, len(types))
	end := head
	for i, t := range types {
		w := wordAt(data, 32*i)
		if t.kind == argScalar {
			v, err := decodeScalar(w, t.scalar, mode)
			if err != nil {
				return nil, err
			}
			vals[i] = abiValue{word: v}
			continue
		}
		off, err := smallInt(w, len(data))
		if err != nil {
			return nil, err
		}
		if mode.strict && off != end {
			return nil, errABI
		}
		d, err := decodeDynamicValue(data, dynAt{offset: off, typ: t}, mode)
		if err != nil {
			return nil, err
		}
		vals[i] = d.value
		end = max(end, off+d.used)
	}
	if mode.strict && !mode.trailing && end != len(data) {
		return nil, errABI
	}
	return vals, nil
}

type dynAt struct {
	offset int
	typ    abiType
}

func wordAt(data []byte, at int) Hash {
	var h Hash
	copy(h[:], data[at:at+32])
	return h
}

func smallInt(w Hash, limit int) (int, error) {
	v := toWord(w)
	if !v.IsUint64() || v.Uint64() > uint64(limit) {
		return 0, errABI
	}
	return int(v.Uint64()), nil
}

type decoded struct {
	value abiValue
	used  int
}

func decodeDynamicValue(data []byte, at dynAt, mode decodeMode) (decoded, error) {
	if at.offset+32 > len(data) {
		return decoded{}, errABI
	}
	n, err := smallInt(wordAt(data, at.offset), len(data))
	if err != nil {
		return decoded{}, err
	}
	body := at.offset + 32
	if at.typ.kind == argTupleArray {
		return decodeTupleArray(data, tupleArrayAt{body: body, n: n, fields: at.typ.fields}, mode)
	}
	if body+n > len(data) {
		return decoded{}, errABI
	}
	raw := data[body : body+n]
	used := 32 + n
	if mode.strict {
		padded := (n + 31) / 32 * 32
		if body+padded > len(data) {
			return decoded{}, errABI
		}
		for _, b := range data[body+n : body+padded] {
			if b != 0 {
				return decoded{}, errABI
			}
		}
		used = 32 + padded
		if at.typ.kind == argString && !utf8.Valid(raw) {
			return decoded{}, errABI
		}
	} else if at.typ.kind == argString {
		raw = lossyUTF8(raw)
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return decoded{value: abiValue{data: out}, used: used}, nil
}

type tupleArrayAt struct {
	body   int
	n      int
	fields []scalar
}

func decodeTupleArray(data []byte, at tupleArrayAt, mode decodeMode) (decoded, error) {
	width := 32 * len(at.fields)
	if at.n > maxArrayItems || at.body+at.n*width > len(data) {
		return decoded{}, errABI
	}
	items := make([][]Hash, at.n)
	for i := range items {
		row := make([]Hash, len(at.fields))
		for j, f := range at.fields {
			v, err := decodeScalar(wordAt(data, at.body+i*width+32*j), f, mode)
			if err != nil {
				return decoded{}, err
			}
			row[j] = v
		}
		items[i] = row
	}
	return decoded{value: abiValue{items: items}, used: 32 + at.n*width}, nil
}

func decodeScalar(h Hash, t scalar, mode decodeMode) (Hash, error) {
	canon := canonicalWord(h, t)
	if mode.strict && canon != h {
		return Hash{}, errABI
	}
	return canon, nil
}

func canonicalWord(h Hash, t scalar) Hash {
	w := toWord(h)
	if t.kind == kAddress {
		return toHash(w.And(fieldMask(20)))
	}
	if t.kind == kBool {
		return flag(!w.IsZero()).hash()
	}
	if t.kind == kUint {
		return toHash(w.And(fieldMask(uint(t.size / 8))))
	}
	if t.kind == kInt {
		return toHash(w.SignExtend(u256.FromUint64(uint64(t.size/8 - 1))))
	}
	assert.Ok(t.kind == kBytesN, "scalarKind: unhandled value")
	var out Hash
	copy(out[:t.size], h[:t.size])
	return out
}

func lossyUTF8(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	out := make([]byte, 0, len(b)+8)
	i := 0
	for steps := 0; steps < len(b) && i < len(b); steps++ {
		r, size := utf8.DecodeRune(b[i:])
		if r != utf8.RuneError || size > 1 {
			out = append(out, b[i:i+size]...)
			i += size
			continue
		}
		out = utf8.AppendRune(out, utf8.RuneError)
		i += maximalInvalidPrefix(b[i:])
	}
	return out
}

type byteRange struct {
	lo byte
	hi byte
}

type utf8Lead struct {
	need  int
	first byteRange
}

var contRange = byteRange{lo: 0x80, hi: 0xbf}

func leadInfo(c byte) utf8Lead {
	if c >= 0xc2 && c <= 0xdf {
		return utf8Lead{need: 1, first: contRange}
	}
	if c == 0xe0 {
		return utf8Lead{need: 2, first: byteRange{lo: 0xa0, hi: 0xbf}}
	}
	if c == 0xed {
		return utf8Lead{need: 2, first: byteRange{lo: 0x80, hi: 0x9f}}
	}
	if c >= 0xe1 && c <= 0xef {
		return utf8Lead{need: 2, first: contRange}
	}
	if c == 0xf0 {
		return utf8Lead{need: 3, first: byteRange{lo: 0x90, hi: 0xbf}}
	}
	if c == 0xf4 {
		return utf8Lead{need: 3, first: byteRange{lo: 0x80, hi: 0x8f}}
	}
	if c >= 0xf1 && c <= 0xf3 {
		return utf8Lead{need: 3, first: contRange}
	}
	return utf8Lead{}
}

func maximalInvalidPrefix(b []byte) int {
	lead := leadInfo(b[0])
	limit := min(lead.need+1, len(b))
	for i := 1; i < limit; i++ {
		r := contRange
		if i == 1 {
			r = lead.first
		}
		if b[i] < r.lo || b[i] > r.hi {
			return i
		}
	}
	return max(limit, 1)
}

func selector(sig string) [4]byte {
	h := keccak.Sum256([]byte(sig))
	return [4]byte{h[0], h[1], h[2], h[3]}
}

func selectorWord(sel [4]byte) uint32 {
	return uint32(sel[0])<<24 | uint32(sel[1])<<16 | uint32(sel[2])<<8 | uint32(sel[3])
}

type encoder struct {
	head []byte
	tail []byte
	n    int
}

func newEncoder(fields int) *encoder {
	return &encoder{head: make([]byte, 0, fields*32), n: fields}
}

func (e *encoder) word(h Hash) *encoder {
	e.head = append(e.head, h[:]...)
	return e
}

func (e *encoder) u256(w Word) *encoder  { return e.word(toHash(w)) }
func (e *encoder) u64(v uint64) *encoder { return e.u256(u256.FromUint64(v)) }
func (e *encoder) addr(a Address) *encoder {
	return e.word(addressWord(a))
}

func (e *encoder) bytes(b []byte) *encoder {
	e.u64(uint64(e.n*32 + len(e.tail)))
	l := toHash(u256.FromUint64(uint64(len(b))))
	e.tail = append(e.tail, l[:]...)
	e.tail = append(e.tail, b...)
	if pad := (32 - len(b)%32) % 32; pad > 0 {
		e.tail = append(e.tail, make([]byte, pad)...)
	}
	return e
}

func (e *encoder) out() []byte { return append(e.head, e.tail...) }

func abiError(sel [4]byte, args ...Hash) []byte {
	out := make([]byte, 4, 4+32*len(args))
	copy(out, sel[:])
	for _, a := range args {
		out = append(out, a[:]...)
	}
	return out
}

func encodeWords(args ...Hash) []byte {
	out := make([]byte, 0, 32*len(args))
	for _, a := range args {
		out = append(out, a[:]...)
	}
	return out
}

func wordOfU64(v uint64) Hash { return toHash(u256.FromUint64(v)) }
