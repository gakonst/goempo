package storage

import (
	"bytes"
	"slices"
	"sync"

	"github.com/kapetan-io/tiger/assert"
)

const entryOverheadBytes = 64

const arenaChunkBytes = 1 << 20

const arenaLargeBytes = arenaChunkBytes / 8

const memShardBits = 3

const memShards = 1 << memShardBits

const memSlotsMin = 1 << 8

const memSlotsStartMax = 1 << 18

const memEntriesMax = 1 << 30

const deltaFoldMin = 1 << 12

const _ = uint(arenaChunkBytes - arenaLargeBytes)

const _ = uint(memSlotsStartMax - memSlotsMin)

const _ = uint(memEntriesMax - memSlotsStartMax)

const _ = uint(memEntriesMax - deltaFoldMin)

type memEntry struct {
	kChunk  uint32
	kOff    uint32
	vChunk  uint32
	vOff    uint32
	vLen    uint32
	vCap    uint32
	kLen    uint16
	deleted bool
}

type arenaSpan struct {
	chunk uint32
	off   uint32
}

type probe struct {
	pos int
	idx int
}

type memShard struct {
	chunks  [][]byte
	cur     int
	entries []memEntry
	slots   []uint64
	bytes   int

	sortMu sync.Mutex
	base   []uint32
	delta  []uint32
	baseN  int
	deltaN int
}

func newMemShard(limit int) *memShard {
	slots := memSlotsMin
	for steps := 0; steps < 32 && slots < memSlotsStartMax && slots*entryOverheadBytes < limit/2; steps++ {
		slots *= 2
	}
	s := &memShard{slots: make([]uint64, slots), entries: make([]memEntry, 0, slots/4)}
	s.chunks = append(s.chunks, make([]byte, 0, arenaChunkBytes))
	return s
}

func (s *memShard) alloc(n int) arenaSpan {
	if n > arenaLargeBytes {
		s.chunks = append(s.chunks, make([]byte, n))
		return arenaSpan{chunk: uint32(len(s.chunks) - 1)}
	}
	if cap(s.chunks[s.cur])-len(s.chunks[s.cur]) < n {
		s.chunks = append(s.chunks, make([]byte, 0, arenaChunkBytes))
		s.cur = len(s.chunks) - 1
	}
	c := s.chunks[s.cur]
	off := len(c)
	s.chunks[s.cur] = c[:off+n]
	return arenaSpan{chunk: uint32(s.cur), off: uint32(off)}
}

func (s *memShard) store(p []byte) arenaSpan {
	if len(p) == 0 {
		return arenaSpan{chunk: uint32(s.cur), off: uint32(len(s.chunks[s.cur]))}
	}
	sp := s.alloc(len(p))
	copy(s.chunks[sp.chunk][sp.off:], p)
	s.bytes += len(p)
	return sp
}

func (s *memShard) key(e *memEntry) []byte {
	end := e.kOff + uint32(e.kLen)
	return s.chunks[e.kChunk][e.kOff:end:end]
}

func (s *memShard) value(e *memEntry) []byte {
	end := e.vOff + e.vLen
	return s.chunks[e.vChunk][e.vOff:end:end]
}

func (s *memShard) find(key []byte, h uint64) probe {
	mask := len(s.slots) - 1
	tag := h >> 32
	pos := int(h) & mask
	for probes := 0; probes < len(s.slots); probes++ {
		slot := s.slots[pos]
		if slot == 0 {
			return probe{pos: pos, idx: -1}
		}
		idx := int(uint32(slot)) - 1
		if slot>>32 == tag && bytes.Equal(s.key(&s.entries[idx]), key) {
			return probe{pos: pos, idx: idx}
		}
		pos = (pos + 1) & mask
	}
	assert.Fail("storage memtable hash table is full")
	return probe{}
}

func (s *memShard) grow() {
	old := s.slots
	s.slots = make([]uint64, 2*len(old))
	mask := len(s.slots) - 1
	for _, slot := range old {
		if slot == 0 {
			continue
		}
		e := &s.entries[uint32(slot)-1]
		pos := int(hashKey(s.key(e))) & mask
		for probes := 0; probes < len(s.slots); probes++ {
			if s.slots[pos] == 0 {
				break
			}
			pos = (pos + 1) & mask
		}
		assert.Ok(s.slots[pos] == 0, "storage memtable rehash found a free slot")
		s.slots[pos] = slot
	}
}

func (s *memShard) apply(o op, h uint64) {
	pr := s.find(o.key, h)
	if pr.idx >= 0 {
		e := &s.entries[pr.idx]
		if uint32(len(o.value)) <= e.vCap {
			copy(s.chunks[e.vChunk][e.vOff:], o.value)
		} else {
			sp := s.store(o.value)
			e.vChunk, e.vOff, e.vCap = sp.chunk, sp.off, uint32(len(o.value))
		}
		e.vLen, e.deleted = uint32(len(o.value)), o.deleted
		return
	}
	assert.Ok(len(s.entries) < memEntriesMax, "storage memtable has too many entries")
	vs := s.store(o.value)
	ks := s.store(o.key)
	s.entries = append(s.entries, memEntry{
		kChunk: ks.chunk, kOff: ks.off, vChunk: vs.chunk, vOff: vs.off,
		vLen: uint32(len(o.value)), vCap: uint32(len(o.value)), kLen: uint16(len(o.key)), deleted: o.deleted,
	})
	s.slots[pr.pos] = h>>32<<32 | uint64(len(s.entries))
	s.bytes += entryOverheadBytes
	if 2*len(s.entries) > len(s.slots) {
		s.grow()
	}
}

func (s *memShard) get(p *probeKey) (lookup, bool) {
	pr := s.find(p.key, p.hash)
	if pr.idx < 0 {
		return lookup{}, false
	}
	e := &s.entries[pr.idx]
	if e.deleted {
		return lookup{found: true, deleted: true}, true
	}
	return lookup{value: append([]byte(nil), s.value(e)...), found: true}, true
}

func (s *memShard) record(idx uint32) record {
	e := &s.entries[idx]
	return record{key: s.key(e), value: s.value(e), deleted: e.deleted}
}

func (s *memShard) less(a, b uint32) int {
	return bytes.Compare(s.key(&s.entries[a]), s.key(&s.entries[b]))
}

func (s *memShard) sortFrom(from int) []uint32 {
	idxs := make([]uint32, 0, len(s.entries)-from)
	for i := from; i < len(s.entries); i++ {
		idxs = append(idxs, uint32(i))
	}
	slices.SortFunc(idxs, s.less)
	return idxs
}

func (s *memShard) fold() {
	out := make([]uint32, 0, len(s.base)+len(s.delta))
	i, j := 0, 0
	for steps := 0; steps < cap(out); steps++ {
		takeBase := j == len(s.delta)
		if !takeBase && i < len(s.base) {
			takeBase = s.less(s.base[i], s.delta[j]) < 0
		}
		if takeBase {
			out = append(out, s.base[i])
			i++
			continue
		}
		out = append(out, s.delta[j])
		j++
	}
	s.base = out
	s.baseN = len(s.entries)
	s.delta = nil
}

type sortMode bool

const (
	sortLazy sortMode = false
	sortFull sortMode = true
)

func (s *memShard) sorted(mode sortMode) ([]uint32, []uint32) {
	s.sortMu.Lock()
	defer s.sortMu.Unlock()
	if s.deltaN < len(s.entries) {
		s.delta = s.sortFrom(s.baseN)
		s.deltaN = len(s.entries)
	}
	big := len(s.delta) > max(deltaFoldMin, s.baseN/4)
	if len(s.delta) > 0 {
		if mode == sortFull || big {
			s.fold()
		}
	}
	return s.base, s.delta
}

type shardIterator struct {
	s     *memShard
	base  []uint32
	delta []uint32
	cur   uint32
	done  bool
}

func (s *memShard) iterator(prefix []byte, mode sortMode) *shardIterator {
	base, delta := s.sorted(mode)
	seek := func(list []uint32) []uint32 {
		i, _ := slices.BinarySearchFunc(list, prefix, func(idx uint32, k []byte) int {
			return bytes.Compare(s.key(&s.entries[idx]), k)
		})
		return list[i:]
	}
	it := &shardIterator{s: s, base: seek(base), delta: seek(delta)}
	it.pick()
	return it
}

func (it *shardIterator) pick() {
	switch {
	case len(it.base) == 0 && len(it.delta) == 0:
		it.done = true
	case len(it.delta) == 0:
		it.cur = it.base[0]
	case len(it.base) == 0:
		it.cur = it.delta[0]
	case it.s.less(it.base[0], it.delta[0]) < 0:
		it.cur = it.base[0]
	default:
		it.cur = it.delta[0]
	}
}

func (it *shardIterator) valid() bool { return !it.done }

func (it *shardIterator) record() record { return it.s.record(it.cur) }

func (it *shardIterator) next() error {
	if len(it.base) > 0 && it.base[0] == it.cur {
		it.base = it.base[1:]
	} else {
		it.delta = it.delta[1:]
	}
	it.pick()
	return nil
}

type memtable struct {
	shards [memShards]*memShard
}

func newMemtable(limit int) *memtable {
	m := &memtable{}
	for i := range m.shards {
		m.shards[i] = newMemShard(limit / memShards)
	}
	return m
}

func shardOf(h uint64) int { return int(h >> (64 - memShardBits)) }

func (m *memtable) len() int {
	n := 0
	for _, s := range m.shards {
		n += len(s.entries)
	}
	return n
}

func (m *memtable) bytes() int {
	n := 0
	for _, s := range m.shards {
		n += s.bytes
	}
	return n
}

func (m *memtable) apply(o op) {
	h := hashKey(o.key)
	m.shards[shardOf(h)].apply(o, h)
}

func (m *memtable) get(p *probeKey) (lookup, bool) {
	return m.shards[shardOf(p.hash)].get(p)
}

func (m *memtable) sources(prefix []byte, mode sortMode, dst *merger) {
	for _, s := range m.shards {
		dst.sources = append(dst.sources, s.iterator(prefix, mode))
		dst.total += len(s.entries)
	}
}
