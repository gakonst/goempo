package storage

import (
	"maps"
	"slices"
)

const entryOverheadBytes = 64

type memEntry struct {
	value   []byte
	deleted bool
}

type memtable struct {
	entries map[string]memEntry
	bytes   int
}

func newMemtable() *memtable {
	return &memtable{entries: map[string]memEntry{}}
}

func (m *memtable) apply(o op) {
	old, ok := m.entries[string(o.key)]
	if ok {
		m.bytes -= len(o.key) + len(old.value) + entryOverheadBytes
	}
	m.entries[string(o.key)] = memEntry{value: o.value, deleted: o.deleted}
	m.bytes += len(o.key) + len(o.value) + entryOverheadBytes
}

func (m *memtable) get(key []byte) (memEntry, bool) {
	e, ok := m.entries[string(key)]
	return e, ok
}

func (m *memtable) sortedKeys() []string {
	return slices.Sorted(maps.Keys(m.entries))
}

type memIterator struct {
	m    *memtable
	keys []string
	pos  int
}

func (m *memtable) iterator(prefix []byte) *memIterator {
	keys := m.sortedKeys()
	start, _ := slices.BinarySearch(keys, string(prefix))
	return &memIterator{m: m, keys: keys, pos: start}
}

func (it *memIterator) valid() bool { return it.pos < len(it.keys) }

func (it *memIterator) record() record {
	k := it.keys[it.pos]
	e := it.m.entries[k]
	return record{key: []byte(k), value: e.value, deleted: e.deleted}
}

func (it *memIterator) next() error {
	it.pos++
	return nil
}
