package chain

import (
	"sync"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const (
	cacheChainsMax = 2
	cacheRulesMax  = 64
)

const _ = uint(cacheRulesMax - cacheChainsMax*(int(hardfork.T15)+1))

type Cache struct {
	mu      sync.Mutex
	entries map[Rules]*machine
}

type machine struct {
	gas   *evm.GasTable
	table *evm.JumpTable
	eth   *evm.PrecompileSet
}

func NewCache() *Cache {
	return &Cache{entries: make(map[Rules]*machine)}
}

func (c *Cache) machine(r Rules) *machine {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.entries[r]; ok {
		return m
	}
	if len(c.entries) >= cacheRulesMax {
		clear(c.entries)
	}
	g := r.GasTable()
	m := &machine{gas: g, table: r.JumpTable(g), eth: r.EthereumPrecompiles()}
	c.entries[r] = m
	return m
}

func (c *Cache) EthereumPrecompiles(r Rules) *evm.PrecompileSet {
	if c == nil {
		return r.EthereumPrecompiles()
	}
	return c.machine(r).eth
}

// ConfigInto writes Config(r, precompiles) into dst without allocating when
// the cache is not nil.
func (c *Cache) ConfigInto(dst *evm.Config, r Rules, precompiles evm.PrecompileLookup) {
	if c == nil {
		*dst = *r.Config(precompiles)
		return
	}
	m := c.machine(r)
	if precompiles == nil {
		precompiles = m.eth
	}
	r.fill(dst, m.gas, m.table, precompiles)
}

func (c *Cache) Config(r Rules, precompiles evm.PrecompileLookup) *evm.Config {
	if c == nil {
		return r.Config(precompiles)
	}
	m := c.machine(r)
	if precompiles == nil {
		precompiles = m.eth
	}
	return r.config(m.gas, m.table, precompiles)
}
