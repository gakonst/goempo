package precompiles

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
	"unicode/utf8"
)

type slot Hash

func slotOf(n uint64) slot { return slot(primitives.Uint64ToHash(n)) }

func (s slot) plus(n uint64) slot { return slot(toHash(toWord(Hash(s)).Add(u256.FromUint64(n)))) }

func addressWord(a Address) Hash {
	var h Hash
	copy(h[12:], a[:])
	return h
}

func wordAddress(h Hash) Address {
	var a Address
	copy(a[:], h[12:])
	return a
}

func toHash(w Word) Hash { return Hash(w.Bytes32()) }

func toWord(h Hash) Word { return u256.FromBytes32(h) }

func (c *Context) mapKey(key Hash, base slot) slot {
	buf := &c.mapping
	copy(buf[:32], key[:])
	copy(buf[32:], base[:])
	if s, ok := mapKeys.get(buf); ok {
		return s
	}
	s := slot(c.keccak(buf[:]))
	mapKeys.put(buf, s)
	return s
}

func (c *Context) mapAddr(key Address, base slot) slot { return c.mapKey(addressWord(key), base) }

func (c *Context) mapU64(key uint64, base slot) slot {
	return c.mapKey(primitives.Uint64ToHash(key), base)
}

type flag bool

func (f flag) word() Word {
	if f {
		return u256.One()
	}
	return Word{}
}

func (f flag) hash() Hash { return toHash(f.word()) }

type field struct {
	offset uint
	size   uint
}

func fieldMask(size uint) Word {
	if size >= 32 {
		return u256.Max()
	}
	return u256.One().Lsh(size * 8).Sub(u256.One())
}

func (f field) of(w Word) Word { return w.Rsh(f.offset * 8).And(fieldMask(f.size)) }

func (f field) u64(w Word) uint64 { return f.of(w).Uint64() }

type packed struct{ w Word }

func pack(w Word) packed { return packed{w: w} }

func (p packed) with(f field, v Word) packed {
	mask := fieldMask(f.size).Lsh(f.offset * 8)
	return packed{w: p.w.And(mask.Not()).Or(v.And(fieldMask(f.size)).Lsh(f.offset * 8))}
}

func (p packed) withU64(f field, v uint64) packed { return p.with(f, u256.FromUint64(v)) }

func (c *Context) loadField(addr Address, s slot, f field) (Word, error) {
	w, err := c.sload(addr, s)
	if err != nil {
		return Word{}, err
	}
	return f.of(w), nil
}

func (c *Context) storeField(addr Address, s slot, f field, v Word) error {
	w, err := c.sload(addr, s)
	if err != nil {
		return err
	}
	return c.sstore(addr, s, pack(w).with(f, v).w)
}

func (c *Context) loadAddress(addr Address, s slot) (Address, error) {
	w, err := c.sload(addr, s)
	return wordAddress(toHash(w)), err
}

func (c *Context) storeAddress(addr Address, s slot, v Address) error {
	return c.sstore(addr, s, toWord(addressWord(v)))
}

func (c *Context) loadBool(addr Address, s slot) (bool, error) {
	w, err := c.sload(addr, s)
	return !w.IsZero(), err
}

const (
	maxStringBytes = 1 << 16
	maxStringWords = maxStringBytes / 32
)

func longStringWords(head Word) (int, error) {
	if !head.IsUint64() || head.Uint64() > 2*maxStringBytes+1 {
		return 0, errFatal
	}
	n := int((head.Uint64() - 1) / 2)
	return min((n+31)/32, maxStringWords), nil
}

func (c *Context) loadString(addr Address, s slot) ([]byte, error) {
	b, err := c.loadStringBytes(addr, s)
	if err == nil && !utf8.Valid(b) {
		return nil, errFatal
	}
	return b, err
}

func (c *Context) loadStringBytes(addr Address, s slot) ([]byte, error) {
	head, err := c.sload(addr, s)
	if err != nil {
		return nil, err
	}
	b := head.Bytes32()
	if head[0]&1 == 0 {
		n := min(int(b[31]/2), 31)
		out := make([]byte, n)
		copy(out, b[:n])
		return out, nil
	}
	if _, err := longStringWords(head); err != nil {
		return nil, err
	}
	n := min(int((head.Uint64()-1)/2), maxStringBytes)
	words := min((n+31)/32, maxStringWords)
	out := make([]byte, 0, n)
	base := slot(c.keccak(s[:]))
	for i := 0; i < words; i++ {
		w, err := c.sload(addr, base.plus(uint64(i)))
		if err != nil {
			return nil, err
		}
		chunk := w.Bytes32()
		take := min(n-i*32, 32)
		out = append(out, chunk[:take]...)
	}
	return out, nil
}

type wordSpan struct {
	from int
	to   int
}

func (c *Context) storeString(addr Address, s slot, value []byte) error {
	if len(value) > maxStringBytes {
		return errFatal
	}
	newWords := 0
	if len(value) > 31 {
		newWords = min((len(value)+31)/32, maxStringWords)
	}
	if c.at(hardfork.T5) {
		prev, err := c.sload(addr, s)
		if err != nil {
			return err
		}
		if prev[0]&1 == 1 {
			prevWords, err := longStringWords(prev)
			if err != nil {
				return err
			}
			if err := c.clearStringTail(addr, s, wordSpan{from: newWords, to: prevWords}); err != nil {
				return err
			}
		}
	}
	if len(value) < 32 {
		var b [32]byte
		copy(b[:], value)
		b[31] = byte(len(value) * 2)
		return c.sstore(addr, s, u256.FromBytes32(b))
	}
	if err := c.sstore(addr, s, u256.FromUint64(uint64(len(value))*2+1)); err != nil {
		return err
	}
	base := slot(c.keccak(s[:]))
	for i := 0; i < newWords; i++ {
		var b [32]byte
		copy(b[:], value[i*32:])
		if err := c.sstore(addr, base.plus(uint64(i)), u256.FromBytes32(b)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) clearStringTail(addr Address, s slot, span wordSpan) error {
	end := min(span.to, maxStringWords)
	if span.from >= end {
		return nil
	}
	base := slot(c.keccak(s[:]))
	for i := span.from; i < end; i++ {
		if err := c.sstore(addr, base.plus(uint64(i)), Word{}); err != nil {
			return err
		}
	}
	return nil
}

type ratio struct {
	num Word
	den Word
}

func (r ratio) of(a Word) (Word, error) {
	if a.IsZero() || r.num.IsZero() {
		return Word{}, nil
	}
	p := a.Mul(r.num)
	if p.Div(a) != r.num {
		return Word{}, underOverflow()
	}
	if r.den.IsZero() {
		return Word{}, underOverflow()
	}
	return p.Div(r.den), nil
}

type num Word

func (a num) add(b Word) (Word, error) {
	v, overflow := Word(a).AddOverflow(b)
	if overflow {
		return Word{}, underOverflow()
	}
	return v, nil
}

func (a num) sub(b Word) (Word, error) {
	v, overflow := Word(a).SubOverflow(b)
	if overflow {
		return Word{}, underOverflow()
	}
	return v, nil
}

func (a num) satAdd(b Word) Word {
	v, overflow := Word(a).AddOverflow(b)
	if overflow {
		return u256.Max()
	}
	return v
}

type sat uint64

func (a sat) add(b uint64) sat {
	if uint64(a)+b < uint64(a) {
		return sat(^uint64(0))
	}
	return a + sat(b)
}

func (a sat) sub(b uint64) sat {
	if uint64(a) < b {
		return 0
	}
	return a - sat(b)
}

func (a sat) mul(b uint64) sat {
	if a != 0 && (uint64(a)*b)/uint64(a) != b {
		return sat(^uint64(0))
	}
	return a * sat(b)
}
