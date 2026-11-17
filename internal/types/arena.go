package types

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/kapetan-io/tiger/assert"
)

// slabMin is the smallest slab a BlockDecoder allocates; a slab doubles when
// a run of blocks outgrows it, so a reused decoder settles at its workload's
// size and then decodes without allocating.
const slabMin = 1 << 10

const _ = uint(listItemsMax - slabMin)

// BlockDecoder decodes blocks in place and draws legacy transactions, their
// To addresses and the blocks' transaction slices from slabs it reuses.
//
// A block it decodes aliases the input buffer and the decoder's slabs: both
// must outlive the block, and Reset invalidates every block decoded before
// it. The zero value is ready; a nil decoder allocates from the heap.
type BlockDecoder struct {
	legacy []LegacyTx
	addrs  []primitives.Address
	txs    []Transaction
}

// Reset reclaims the slabs for the next run of blocks.
func (d *BlockDecoder) Reset() {
	clear(d.legacy)
	clear(d.txs)
	d.legacy = d.legacy[:0]
	d.addrs = d.addrs[:0]
	d.txs = d.txs[:0]
}

// DecodeBlock decodes b in place; b and the decoder's slabs back the block
// until the next Reset.
func (d *BlockDecoder) DecodeBlock(b []byte) (Block, error) {
	assert.Ok(d != nil, "DecodeBlock needs a decoder")
	return d.decodeBlock(b)
}

func (d *BlockDecoder) newLegacy() *LegacyTx {
	if d == nil {
		return &LegacyTx{}
	}
	if len(d.legacy) == cap(d.legacy) {
		d.legacy = make([]LegacyTx, 0, max(slabMin, 2*cap(d.legacy)))
	}
	d.legacy = d.legacy[:len(d.legacy)+1]
	tx := &d.legacy[len(d.legacy)-1]
	assert.Ok(tx.Data == nil && tx.To == nil, "a slab transaction starts zero")
	return tx
}

func (d *BlockDecoder) decodeTo(s *rlp.Stream) (*primitives.Address, error) {
	if d == nil {
		return decodeTo(s)
	}
	b, err := s.Bytes()
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	if len(b) != primitives.AddressLength {
		return nil, rlp.ErrLength
	}
	if len(d.addrs) == cap(d.addrs) {
		d.addrs = make([]primitives.Address, 0, max(slabMin, 2*cap(d.addrs)))
	}
	d.addrs = append(d.addrs, primitives.BytesToAddress(b))
	return &d.addrs[len(d.addrs)-1], nil
}

func (d *BlockDecoder) transactions(n int) []Transaction {
	if d == nil {
		return make([]Transaction, 0, n)
	}
	if cap(d.txs)-len(d.txs) < n {
		d.txs = make([]Transaction, 0, max(slabMin, 2*cap(d.txs), n))
	}
	start := len(d.txs)
	d.txs = d.txs[:start+n]
	return d.txs[start : start : start+n]
}

func (d *BlockDecoder) decodeTransactions(s *rlp.Stream) ([]Transaction, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	n, err := list.Count()
	if err != nil {
		return nil, err
	}
	if n > listItemsMax {
		return nil, ErrTooManyItems
	}
	out := d.transactions(n)
	for i := 0; i < n; i++ {
		tx, err := d.decodeBodyTransaction(&list)
		if err != nil {
			return nil, err
		}
		out = append(out, tx)
	}
	assert.Equal(len(out), n, "a block decodes the transactions it counted")
	return out, list.Finish()
}
