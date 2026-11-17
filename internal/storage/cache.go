package storage

import (
	"bytes"
	"hash/crc32"
	"sync"

	"github.com/kapetan-io/tiger/assert"
)

const cacheSlotBytes = 8 << 10

const cacheShards = 64

const BlockCacheBytesDefault = 32 << 20

const cacheSlotsPerShardMin = 4

const _ = uint(cacheSlotBytes - blockBytesTarget)

type cacheSlot struct {
	key     uint64
	buf     []byte
	offs    [blockRecordsMax]uint16
	n       int
	live    bool
	ref     bool
	loading bool
}

type cacheShard struct {
	mu    sync.Mutex
	slots []cacheSlot
	index map[uint64]int32
	hand  int
}

type blockCache struct {
	shards [cacheShards]cacheShard
}

func newBlockCache(bytesTotal int) *blockCache {
	perShard := max(bytesTotal/cacheSlotBytes/cacheShards, cacheSlotsPerShardMin)
	slab := make([]byte, perShard*cacheShards*cacheSlotBytes)
	c := &blockCache{}
	for i := range c.shards {
		sh := &c.shards[i]
		sh.slots = make([]cacheSlot, perShard)
		sh.index = make(map[uint64]int32, perShard)
		for j := range sh.slots {
			off := (i*perShard + j) * cacheSlotBytes
			sh.slots[j].buf = slab[off : off : off+cacheSlotBytes]
		}
	}
	return c
}

func cacheKey(s *segment, block int) uint64 {
	assert.Ok(block >= 0 && block < 1<<32, "segment block index fits a cache key")
	return s.id<<32 | uint64(block)
}

func (c *blockCache) shard(key uint64) *cacheShard {
	return &c.shards[hashMix(key, hashMul0)%cacheShards]
}

func parseOffsets(block []byte, records int, offs []uint16) error {
	r := reader{b: block}
	for i := 0; i < records; i++ {
		offs[i] = uint16(len(block) - len(r.b))
		nextRecord(&r)
		if r.err != nil {
			return r.err
		}
	}
	if len(r.b) != 0 {
		return ErrCorrupt
	}
	return nil
}

func scanBlock(block []byte, records int, key []byte) (lookup, error) {
	r := reader{b: block}
	for range records {
		rec := nextRecord(&r)
		if r.err != nil {
			return lookup{}, r.err
		}
		c := bytes.Compare(rec.key, key)
		if c == 0 {
			return lookup{value: append([]byte(nil), rec.value...), found: true, deleted: rec.deleted}, nil
		}
		if c > 0 {
			return lookup{}, nil
		}
	}
	return lookup{}, nil
}

func searchSlot(sl *cacheSlot, key []byte) lookup {
	lo, hi := 0, sl.n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		r := reader{b: sl.buf[sl.offs[mid]:]}
		rec := nextRecord(&r)
		assert.Ok(r.err == nil, "cached block decodes")
		switch c := bytes.Compare(rec.key, key); {
		case c == 0:
			return lookup{value: append([]byte(nil), rec.value...), found: true, deleted: rec.deleted}
		case c < 0:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return lookup{}
}

func (sh *cacheShard) victim() int {
	for steps := 0; steps < 2*len(sh.slots)+1; steps++ {
		i := sh.hand
		sh.hand = (sh.hand + 1) % len(sh.slots)
		sl := &sh.slots[i]
		if sl.loading {
			continue
		}
		if sl.ref {
			sl.ref = false
			continue
		}
		if sl.live {
			if cur, ok := sh.index[sl.key]; ok && int(cur) == i {
				delete(sh.index, sl.key)
			}
			sl.live = false
		}
		return i
	}
	return -1
}

func (c *blockCache) get(s *segment, block int, key []byte) (lookup, error) {
	ck := cacheKey(s, block)
	sh := c.shard(ck)
	e := s.index[block]
	sh.mu.Lock()
	if i, ok := sh.index[ck]; ok {
		sl := &sh.slots[i]
		sl.ref = true
		l := searchSlot(sl, key)
		sh.mu.Unlock()
		return l, nil
	}
	slot := -1
	if e.length <= cacheSlotBytes {
		slot = sh.victim()
	}
	if slot < 0 {
		sh.mu.Unlock()
		buf, err := s.readBlock(block, nil)
		if err != nil {
			return lookup{}, err
		}
		return scanBlock(buf, e.records, key)
	}
	sl := &sh.slots[slot]
	sl.loading = true
	buf := sl.buf[:e.length]
	sh.mu.Unlock()
	err := c.load(s, e, buf, sl)
	sh.mu.Lock()
	sl.loading = false
	if err != nil {
		sh.mu.Unlock()
		return lookup{}, err
	}
	sl.key, sl.live, sl.ref, sl.n = ck, true, true, e.records
	sl.buf = buf
	sh.index[ck] = int32(slot)
	l := searchSlot(sl, key)
	sh.mu.Unlock()
	return l, nil
}

func (c *blockCache) load(s *segment, e indexEntry, buf []byte, sl *cacheSlot) error {
	if _, err := s.file.ReadAt(buf, e.offset); err != nil {
		return err
	}
	if crc32.Checksum(buf, castagnoli) != e.crc {
		return ErrCorrupt
	}
	return parseOffsets(buf, e.records, sl.offs[:])
}
