package storage

import (
	"errors"

	"github.com/kapetan-io/tiger/assert"
)

const KeyBytesMax = 1024

const ValueBytesMax = 1 << 24

const _ = uint(ValueBytesMax - KeyBytesMax)

var (
	ErrKeyTooLong   = errors.New("storage: key longer than KeyBytesMax")
	ErrValueTooLong = errors.New("storage: value longer than ValueBytesMax")
	ErrEmptyKey     = errors.New("storage: empty key")
	ErrCorrupt      = errors.New("storage: segment is corrupt")
	ErrManifest     = errors.New("storage: manifest is corrupt")
	ErrNotFound     = errors.New("storage: key not found")
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
	ops   []op
	bytes int
}

func (b *Batch) Put(key []byte, value Value) {
	assert.Ok(len(key) > 0, "storage put of an empty key")
	assert.Ok(len(key) <= KeyBytesMax, "storage key longer than KeyBytesMax")
	assert.Ok(len(value) <= ValueBytesMax, "storage value longer than ValueBytesMax")
	b.ops = append(b.ops, op{key: append([]byte(nil), key...), value: append([]byte{}, value...)})
	b.bytes += len(key) + len(value)
}

func (b *Batch) Delete(key []byte) {
	assert.Ok(len(key) > 0, "storage delete of an empty key")
	assert.Ok(len(key) <= KeyBytesMax, "storage key longer than KeyBytesMax")
	b.ops = append(b.ops, op{key: append([]byte(nil), key...), deleted: true})
	b.bytes += len(key)
}

func (b *Batch) Len() int { return len(b.ops) }

func (b *Batch) Bytes() int { return b.bytes }

func (b *Batch) Reset() {
	b.ops = b.ops[:0]
	b.bytes = 0
}
