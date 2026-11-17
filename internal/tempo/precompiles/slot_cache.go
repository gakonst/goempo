package precompiles

import "sync"

const (
	mapKeyCacheEntries = 1 << 16
	mapKeyCacheBytes   = 1 << 24
	mapKeyEntryBytes   = 64 + 32
)

const _ = uint(mapKeyCacheBytes - mapKeyCacheEntries*mapKeyEntryBytes*2)

type mapKeyCache struct {
	mu    sync.Mutex
	slots map[[64]byte]slot
}

var mapKeys = mapKeyCache{slots: make(map[[64]byte]slot, mapKeyCacheEntries)}

func (m *mapKeyCache) get(in *[64]byte) (slot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.slots[*in]
	return s, ok
}

func (m *mapKeyCache) put(in *[64]byte, s slot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.slots) >= mapKeyCacheEntries {
		clear(m.slots)
	}
	m.slots[*in] = s
}
