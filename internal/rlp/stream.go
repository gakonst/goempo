package rlp

import (
	"github.com/gakonst/goempo/internal/primitives"
)

type Stream struct {
	data  []byte
	pos   int
	depth int
}

type item struct {
	kind  Kind
	start int
	end   int
}

func NewStream(b []byte) Stream {
	return Stream{data: b}
}

func (s *Stream) Empty() bool {
	return s.pos >= len(s.data)
}

func (s *Stream) Rest() []byte {
	return s.data[s.pos:]
}

func (s *Stream) Depth() int {
	return s.depth
}

func (s *Stream) Finish() error {
	if s.pos != len(s.data) {
		return ErrTrailingBytes
	}
	return nil
}

func (s *Stream) Kind() (Kind, error) {
	it, err := s.peek()
	return it.kind, err
}

func (s *Stream) Bytes() ([]byte, error) {
	it, err := s.peek()
	if err != nil {
		return nil, err
	}
	if it.kind != KindString {
		return nil, ErrExpectedString
	}
	s.pos = it.end
	return s.data[it.start:it.end], nil
}

func (s *Stream) BytesFixed(dst []byte) error {
	it, err := s.peek()
	if err != nil {
		return err
	}
	if it.kind != KindString {
		return ErrExpectedString
	}
	if it.end-it.start != len(dst) {
		return ErrLength
	}
	copy(dst, s.data[it.start:it.end])
	s.pos = it.end
	return nil
}

func (s *Stream) Address() (primitives.Address, error) {
	var a primitives.Address
	err := s.BytesFixed(a[:])
	return a, err
}

func (s *Stream) Hash() (primitives.Hash, error) {
	var h primitives.Hash
	err := s.BytesFixed(h[:])
	return h, err
}

func (s *Stream) UintBytes(sizeMax int) ([]byte, error) {
	content, err := s.Bytes()
	if err != nil {
		return nil, err
	}
	if len(content) > sizeMax {
		return nil, ErrUintOverflow
	}
	if len(content) > 0 {
		if content[0] == 0 {
			return nil, ErrLeadingZero
		}
	}
	return content, nil
}

func (s *Stream) Uint64() (uint64, error) {
	content, err := s.UintBytes(8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for i := 0; i < len(content); i++ {
		v = v<<8 | uint64(content[i])
	}
	return v, nil
}

func (s *Stream) List() (Stream, error) {
	it, err := s.peek()
	if err != nil {
		return Stream{}, err
	}
	if it.kind != KindList {
		return Stream{}, ErrExpectedList
	}
	if s.depth+1 > DepthMax {
		return Stream{}, ErrTooDeep
	}
	s.pos = it.end
	return Stream{data: s.data[it.start:it.end], depth: s.depth + 1}, nil
}

func (s *Stream) Raw() ([]byte, error) {
	begin := s.pos
	it, err := s.peek()
	if err != nil {
		return nil, err
	}
	s.pos = it.end
	return s.data[begin:it.end], nil
}

func (s *Stream) Skip() error {
	it, err := s.peek()
	if err != nil {
		return err
	}
	s.pos = it.end
	return nil
}

func (s *Stream) Count() (int, error) {
	cursor := Stream{data: s.data[s.pos:], depth: s.depth}
	itemsMax := len(cursor.data)
	n := 0
	for ; n < itemsMax; n++ {
		if cursor.Empty() {
			break
		}
		if err := cursor.Skip(); err != nil {
			return 0, err
		}
	}
	if n == itemsMax && !cursor.Empty() {
		return 0, ErrTrailingBytes
	}
	return n, nil
}

func (s *Stream) peek() (item, error) {
	if s.pos >= len(s.data) {
		return item{}, ErrEmpty
	}
	return parseHeader(s.data, s.pos)
}

func parseHeader(data []byte, pos int) (item, error) {
	b := int(data[pos])
	rest := len(data) - pos - 1
	if b < offsetString {
		return item{kind: KindString, start: pos, end: pos + 1}, nil
	}
	if b <= offsetLongString {
		size := b - offsetString
		if size > rest {
			return item{}, ErrUnexpectedEOF
		}
		if size == 1 {
			if data[pos+1] < offsetString {
				return item{}, ErrSingleByte
			}
		}
		return item{kind: KindString, start: pos + 1, end: pos + 1 + size}, nil
	}
	if b < offsetList {
		return parseLong(data, pos, KindString)
	}
	if b <= offsetLongList {
		size := b - offsetList
		if size > rest {
			return item{}, ErrUnexpectedEOF
		}
		return item{kind: KindList, start: pos + 1, end: pos + 1 + size}, nil
	}
	return parseLong(data, pos, KindList)
}

func parseLong(data []byte, pos int, kind Kind) (item, error) {
	base := offsetLongString
	if kind == KindList {
		base = offsetLongList
	}
	lenOfLen := int(data[pos]) - base
	rest := len(data) - pos - 1
	if lenOfLen > rest {
		return item{}, ErrUnexpectedEOF
	}
	sizeBytes := data[pos+1 : pos+1+lenOfLen]
	if sizeBytes[0] == 0 {
		return item{}, ErrNonCanonicalSize
	}
	var size uint64
	for i := 0; i < len(sizeBytes); i++ {
		size = size<<8 | uint64(sizeBytes[i])
	}
	if size <= shortMax {
		return item{}, ErrNonCanonicalSize
	}
	if size > uint64(rest-lenOfLen) {
		return item{}, ErrUnexpectedEOF
	}
	start := pos + 1 + lenOfLen
	return item{kind: kind, start: start, end: start + int(size)}, nil
}

type Item struct {
	Kind    Kind
	Content []byte
	Rest    []byte
}

func Split(b []byte) (Item, error) {
	if len(b) == 0 {
		return Item{}, ErrEmpty
	}
	it, err := parseHeader(b, 0)
	if err != nil {
		return Item{}, err
	}
	return Item{Kind: it.kind, Content: b[it.start:it.end], Rest: b[it.end:]}, nil
}

func DecodeBytes(b []byte) ([]byte, error) {
	s := NewStream(b)
	content, err := s.Bytes()
	if err != nil {
		return nil, err
	}
	return content, s.Finish()
}

func DecodeUint64(b []byte) (uint64, error) {
	s := NewStream(b)
	v, err := s.Uint64()
	if err != nil {
		return 0, err
	}
	return v, s.Finish()
}

func Validate(b []byte) error {
	whole := NewStream(b)
	if err := whole.Skip(); err != nil {
		return err
	}
	if err := whole.Finish(); err != nil {
		return err
	}
	var stack [DepthMax + 1]Stream
	stack[0] = NewStream(b)
	top := 0
	steps := 0
	for ; steps < 2*len(b)+2; steps++ {
		if stack[top].Empty() {
			if top == 0 {
				return nil
			}
			top--
			continue
		}
		kind, err := stack[top].Kind()
		if err != nil {
			return err
		}
		if kind == KindString {
			if err := stack[top].Skip(); err != nil {
				return err
			}
			continue
		}
		inner, err := stack[top].List()
		if err != nil {
			return err
		}
		top++
		stack[top] = inner
	}
	return ErrUnexpectedEOF
}
