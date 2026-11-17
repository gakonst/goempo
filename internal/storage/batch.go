package storage

import (
	"errors"

	"github.com/kapetan-io/tiger/assert"
)

const KeyBytesMax = 1024

const ValueBytesMax = 1 << 24

const _ = uint(ValueBytesMax - KeyBytesMax)

const batchChunkFirst = 512

const batchChunkBytes = 64 << 10

const batchFreeMax = 8

const batchOpsFirst = 32

const batchOpsChunk = 8192

const _ = uint(batchOpsChunk - batchOpsFirst)

const batchPartsMax = 1 << 20

const _ = uint(batchChunkBytes - batchChunkFirst)

var (
	ErrKeyTooLong   = errors.New("storage: key longer than KeyBytesMax")
	ErrValueTooLong = errors.New("storage: value longer than ValueBytesMax")
	ErrEmptyKey     = errors.New("storage: empty key")
	ErrCorrupt      = errors.New("storage: segment is corrupt")
	ErrManifest     = errors.New("storage: manifest is corrupt")
	ErrNotFound     = errors.New("storage: key not found")
	ErrClosed       = errors.New("storage: database is closed")
)

type Value []byte

type Entry struct {
	Key   []byte
	Value []byte
}

type op struct {
	key     []byte
	value   []byte
	deleted bool
}

type Batch struct {
	parts [][]op
	ops   []op
	count int
	bytes int
	buf   []byte
}

type batchMem struct {
	ops []op
	buf []byte
}

var batchFree = make(chan batchMem, batchFreeMax)

func (b *Batch) reserve() {
	if cap(b.ops) > 0 {
		return
	}
	select {
	case m := <-batchFree:
		b.ops, b.buf = m.ops[:0], m.buf[:0]
	default:
	}
}

func (b *Batch) copyIn(p []byte) []byte {
	if len(p) > cap(b.buf)-len(b.buf) {
		size := min(max(2*cap(b.buf), batchChunkFirst), batchChunkBytes)
		b.buf = make([]byte, 0, max(size, len(p)))
	}
	off := len(b.buf)
	b.buf = append(b.buf, p...)
	return b.buf[off:len(b.buf):len(b.buf)]
}

func (b *Batch) push(o op) {
	b.reserve()
	if len(b.ops) == cap(b.ops) {
		size := min(max(2*cap(b.ops), batchOpsFirst), batchOpsChunk)
		if len(b.ops) > 0 {
			assert.Ok(len(b.parts) < batchPartsMax, "storage batch has too many parts")
			b.parts = append(b.parts, b.ops)
		}
		b.ops = make([]op, 0, size)
	}
	b.ops = append(b.ops, o)
	b.count++
}

func (b *Batch) Put(key []byte, value Value) {
	assert.Ok(len(key) > 0, "storage put of an empty key")
	assert.Ok(len(key) <= KeyBytesMax, "storage key longer than KeyBytesMax")
	assert.Ok(len(value) <= ValueBytesMax, "storage value longer than ValueBytesMax")
	b.push(op{key: b.copyIn(key), value: b.copyIn(value)})
	b.bytes += len(key) + len(value)
}

func (b *Batch) PutOwned(key []byte, value Value) {
	assert.Ok(len(key) > 0, "storage put of an empty key")
	assert.Ok(len(key) <= KeyBytesMax, "storage key longer than KeyBytesMax")
	assert.Ok(len(value) <= ValueBytesMax, "storage value longer than ValueBytesMax")
	b.push(op{key: key, value: value})
	b.bytes += len(key) + len(value)
}

func (b *Batch) Delete(key []byte) {
	assert.Ok(len(key) > 0, "storage delete of an empty key")
	assert.Ok(len(key) <= KeyBytesMax, "storage key longer than KeyBytesMax")
	b.push(op{key: b.copyIn(key), deleted: true})
	b.bytes += len(key)
}

func (b *Batch) Len() int { return b.count }

func (b *Batch) Append(other *Batch) {
	assert.Ok(b != other, "storage batch appended to itself")
	if other.count == 0 {
		return
	}
	assert.Ok(len(b.parts)+len(other.parts)+2 <= batchPartsMax, "storage batch has too many parts")
	if len(b.ops) > 0 {
		b.parts = append(b.parts, b.ops)
		b.ops = b.ops[len(b.ops):]
	}
	b.parts = append(b.parts, other.parts...)
	if len(other.ops) > 0 {
		b.parts = append(b.parts, other.ops)
	}
	b.count += other.count
	b.bytes += other.bytes
	*other = Batch{}
}

func (b *Batch) Bytes() int { return b.bytes }

func (b *Batch) each(visit func(op)) {
	for _, part := range b.parts {
		for _, o := range part {
			visit(o)
		}
	}
	for _, o := range b.ops {
		visit(o)
	}
}

func (b *Batch) Reset() {
	clear(b.parts)
	b.parts = b.parts[:0]
	b.ops = b.ops[:0]
	b.count = 0
	b.bytes = 0
	b.buf = b.buf[:0]
}

func (b *Batch) release() {
	for _, part := range b.parts {
		clear(part)
	}
	ops := b.ops[:cap(b.ops)]
	clear(ops)
	m := batchMem{ops: ops[:0], buf: b.buf[:0]}
	*b = Batch{}
	if cap(m.ops) == 0 {
		return
	}
	select {
	case batchFree <- m:
	default:
	}
}
