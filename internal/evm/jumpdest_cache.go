package evm

import "sync"

const (
	jumpdestCacheEntries = 1 << 13
	jumpdestCacheBytes   = 1 << 30
	codeSizeMax          = 1 << 16
)

const _ = uint64(jumpdestCacheBytes - jumpdestCacheEntries*(codeSizeMax/8))

type jumpdestCache struct {
	mu   sync.RWMutex
	bits map[Hash][]uint64
}

var sharedJumpdests = jumpdestCache{bits: make(map[Hash][]uint64, jumpdestCacheEntries)}

func (c *jumpdestCache) get(hash Hash) ([]uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bits, ok := c.bits[hash]
	return bits, ok
}

func (c *jumpdestCache) put(hash Hash, bits []uint64) {
	if len(bits) > codeSizeMax/64+1 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bits) >= jumpdestCacheEntries {
		clear(c.bits)
	}
	c.bits[hash] = bits
}
