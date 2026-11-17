package node

import (
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/types"
)

const (
	DefaultRecentBlocks = 1024
	recentBlocksMax     = 1 << 16
)

const _ = uint(recentBlocksMax - DefaultRecentBlocks)

type Recent struct {
	mu      sync.RWMutex
	archive *Archive
	ring    []*rpc.BlockData
	byHash  map[primitives.Hash]uint64
	txs     map[primitives.Hash]rpc.TxLocation
	head    uint64
	genesis *rpc.BlockData
}

func NewRecent(size int, archive *Archive) *Recent {
	size = max(1, min(size, recentBlocksMax))
	return &Recent{
		archive: archive,
		ring:    make([]*rpc.BlockData, size),
		byHash:  map[primitives.Hash]uint64{},
		txs:     map[primitives.Hash]rpc.TxLocation{},
	}
}

func (r *Recent) Add(b *rpc.BlockData) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := b.Number()
	slot := n % uint64(len(r.ring))
	if old := r.ring[slot]; old != nil {
		r.forget(old)
	}
	r.ring[slot] = b
	r.byHash[b.Hash] = n
	for i := range b.Block.Transactions {
		r.txs[b.TxHash(i)] = rpc.TxLocation{Block: n, Index: i}
	}
	if n == 0 {
		r.genesis = b
	}
	r.head = max(r.head, n)
}

func (r *Recent) forget(old *rpc.BlockData) {
	n := old.Number()
	if r.byHash[old.Hash] == n {
		delete(r.byHash, old.Hash)
	}
	for i := range old.Block.Transactions {
		h := old.TxHash(i)
		if loc, ok := r.txs[h]; ok && loc.Block == n {
			delete(r.txs, h)
		}
	}
}

func (r *Recent) SetHead(n uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = n
}

func (r *Recent) Head() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.head
}

func (r *Recent) Block(number uint64) (*rpc.BlockData, bool) {
	r.mu.RLock()
	head := r.head
	b := r.ring[number%uint64(len(r.ring))]
	genesis := r.genesis
	r.mu.RUnlock()
	if number > head {
		return nil, false
	}
	if b != nil && b.Number() == number {
		return b, true
	}
	if number == 0 && genesis != nil {
		return genesis, true
	}
	return r.fromArchive(number)
}

func (r *Recent) fromArchive(number uint64) (*rpc.BlockData, bool) {
	if r.archive == nil {
		return nil, false
	}
	raw, err := r.archive.Raw(number)
	if err != nil {
		return nil, false
	}
	blk, err := types.DecodeBlock(raw)
	if err != nil {
		return nil, false
	}
	b, err := rpc.NewBlockData(&blk, raw, nil)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (r *Recent) BlockNumber(hash primitives.Hash) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.byHash[hash]
	return n, ok
}

func (r *Recent) TxLocation(hash primitives.Hash) (rpc.TxLocation, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	loc, ok := r.txs[hash]
	return loc, ok
}
