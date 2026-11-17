package rpc

import (
	"errors"
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

var ErrReceiptsCount = errors.New("rpc: receipt count differs from the transaction count")

type TxLocation struct {
	Block uint64
	Index int
}

type BlockData struct {
	Hash     primitives.Hash
	Block    *types.Block
	Raw      []byte
	Receipts []types.Receipt

	once    sync.Once
	senders []primitives.Address
	txHash  []primitives.Hash
}

type Chain interface {
	Head() uint64
	Block(number uint64) (*BlockData, bool)
	BlockNumber(hash primitives.Hash) (uint64, bool)
	TxLocation(hash primitives.Hash) (TxLocation, bool)
}

type StateReader interface {
	Balance(addr primitives.Address) u256.Int
	Nonce(addr primitives.Address) uint64
	Code(addr primitives.Address) []byte
	Storage(addr primitives.Address, key primitives.Hash) u256.Int
}

type StateSource interface {
	StateAt(number uint64) (StateReader, error)
}

func NewBlockData(blk *types.Block, raw []byte, receipts []types.Receipt) (*BlockData, error) {
	if receipts != nil && len(receipts) != len(blk.Transactions) {
		return nil, ErrReceiptsCount
	}
	if raw == nil {
		raw = blk.Encode()
	}
	return &BlockData{Hash: blk.Hash(), Block: blk, Raw: raw, Receipts: receipts}, nil
}

func (b *BlockData) Number() uint64 { return b.Block.Header.Inner.Number }

func (b *BlockData) derive() {
	b.once.Do(func() {
		txs := b.Block.Transactions
		b.senders = make([]primitives.Address, len(txs))
		b.txHash = make([]primitives.Hash, len(txs))
		for i, tx := range txs {
			b.txHash[i] = tx.Hash()
			b.senders[i] = txSender(tx)
		}
	})
}

func txSender(tx types.Transaction) primitives.Address {
	if legacy, ok := tx.Data.(*types.LegacyTx); ok && legacy.IsSystem() {
		return primitives.Address{}
	}
	sender, err := tx.Sender()
	if err != nil {
		return primitives.Address{}
	}
	return sender
}

func (b *BlockData) Sender(i int) primitives.Address {
	b.derive()
	return b.senders[i]
}

func (b *BlockData) TxHash(i int) primitives.Hash {
	b.derive()
	return b.txHash[i]
}

type MemChain struct {
	mu      sync.RWMutex
	blocks  map[uint64]*BlockData
	byHash  map[primitives.Hash]uint64
	txs     map[primitives.Hash]TxLocation
	head    uint64
	hasHead bool
}

func NewMemChain() *MemChain {
	return &MemChain{
		blocks: map[uint64]*BlockData{},
		byHash: map[primitives.Hash]uint64{},
		txs:    map[primitives.Hash]TxLocation{},
	}
}

func (c *MemChain) Add(b *BlockData) {
	b.derive()
	c.mu.Lock()
	defer c.mu.Unlock()
	n := b.Number()
	c.blocks[n] = b
	c.byHash[b.Hash] = n
	for i, h := range b.txHash {
		c.txs[h] = TxLocation{Block: n, Index: i}
	}
	if !c.hasHead || n > c.head {
		c.head = n
		c.hasHead = true
	}
}

func (c *MemChain) SetHead(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.head = n
	c.hasHead = true
}

func (c *MemChain) Head() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.head
}

func (c *MemChain) Block(number uint64) (*BlockData, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.blocks[number]
	return b, ok
}

func (c *MemChain) BlockNumber(hash primitives.Hash) (uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.byHash[hash]
	return n, ok
}

func (c *MemChain) TxLocation(hash primitives.Hash) (TxLocation, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	loc, ok := c.txs[hash]
	return loc, ok
}
