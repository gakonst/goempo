package txpool

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type Hash = primitives.Hash

type Address = primitives.Address

const (
	DefaultMaxTxBytes        = 128 * 1024
	DefaultMaxPerSender      = 64
	DefaultMaxTxs            = 50_000
	DefaultPriceBumpPercent  = 10
	DefaultValidAfterMaxSecs = 120
	DefaultMaxAuthorizations = 16
	validBeforeMinSecs       = 3
	subblockNoncePrefix      = 0x5b
)

type Config struct {
	Executor          *executor.Executor
	MaxTxBytes        int
	MaxPerSender      int
	MaxTxs            int
	PriceBumpPercent  uint64
	MinFeeCap         uint64
	ValidAfterMaxSecs uint64
	MaxAuthorizations int
	Now               func() uint64
}

func (c *Config) defaults() {
	assert.Ok(c.Executor != nil, "a pool needs an executor")
	assert.Ok(c.Now != nil, "a pool needs a clock")
	if c.MaxTxBytes == 0 {
		c.MaxTxBytes = DefaultMaxTxBytes
	}
	if c.MaxPerSender == 0 {
		c.MaxPerSender = DefaultMaxPerSender
	}
	if c.MaxTxs == 0 {
		c.MaxTxs = DefaultMaxTxs
	}
	if c.PriceBumpPercent == 0 {
		c.PriceBumpPercent = DefaultPriceBumpPercent
	}
	if c.ValidAfterMaxSecs == 0 {
		c.ValidAfterMaxSecs = DefaultValidAfterMaxSecs
	}
	if c.MaxAuthorizations == 0 {
		c.MaxAuthorizations = DefaultMaxAuthorizations
	}
}

type laneKey struct {
	sender Address
	key    u256.Int
}

type lane struct {
	key  laneKey
	id   uint64
	base uint64
	txs  map[uint64]*Tx
}

type Tx struct {
	Tx     types.Transaction
	Raw    []byte
	Hash   Hash
	Sender executor.Sender
	Info   executor.TxInfo
	seq    uint64
	lane   laneKey
}

func (t *Tx) IncludableAt(timestamp uint64) bool {
	if t.Info.ValidAfter > timestamp {
		return false
	}
	return t.Info.ValidBefore == 0 || timestamp < t.Info.ValidBefore
}

type Pool struct {
	mu      sync.Mutex
	cfg     Config
	byHash  map[Hash]*Tx
	lanes   map[laneKey]*lane
	bySeq   map[uint64]*Tx
	laneIDs map[uint64]*lane
	senders map[Address]int
	seq     uint64
}

func New(cfg Config) *Pool {
	cfg.defaults()
	return &Pool{
		cfg:     cfg,
		byHash:  map[Hash]*Tx{},
		lanes:   map[laneKey]*lane{},
		bySeq:   map[uint64]*Tx{},
		laneIDs: map[uint64]*lane{},
		senders: map[Address]int{},
	}
}

func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byHash)
}

func (p *Pool) Get(h Hash) (*Tx, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.byHash[h]
	return t, ok
}

func (p *Pool) Add(raw []byte, st executor.State, head *types.Header) (Hash, error) {
	if len(raw) > p.cfg.MaxTxBytes {
		return Hash{}, fmt.Errorf("%w: %d > %d", ErrOversized, len(raw), p.cfg.MaxTxBytes)
	}
	tx, err := types.DecodeTransaction(raw)
	if err != nil {
		return Hash{}, errors.Join(ErrDecode, err)
	}
	t := &Tx{Tx: tx, Raw: raw, Hash: tx.Hash()}
	p.mu.Lock()
	_, known := p.byHash[t.Hash]
	p.mu.Unlock()
	if known {
		return t.Hash, ErrAlreadyKnown
	}
	if err := p.stateless(&t.Tx, head); err != nil {
		return t.Hash, err
	}
	t.Sender = executor.RecoverSender(&t.Tx)
	if t.Sender.Err != nil {
		return t.Hash, errors.Join(ErrSender, t.Sender.Err)
	}
	req := executor.CheckRequest{Header: p.checkHeader(head, &t.Tx), Tx: &t.Tx, Sender: t.Sender, FutureNonce: true}
	info, err := p.cfg.Executor.Check(st, &req)
	if err != nil {
		return t.Hash, errors.Join(ErrRejected, err)
	}
	t.Info = info
	t.lane = laneKey{sender: info.Sender, key: info.NonceKey}
	p.mu.Lock()
	defer p.mu.Unlock()
	return t.Hash, p.insert(t)
}

func (p *Pool) checkHeader(head *types.Header, tx *types.Transaction) *types.Header {
	h := *head
	h.Inner.Number = head.Inner.Number + 1
	h.Inner.Timestamp = head.Inner.Timestamp + validBeforeMinSecs
	if aa, ok := tx.Data.(*types.TempoTx); ok && aa.ValidAfter > h.Inner.Timestamp {
		h.Inner.Timestamp = aa.ValidAfter
	}
	if chain.RulesAt(p.cfg.Executor.ChainID, h.Inner.Timestamp).Fork != chain.RulesAt(p.cfg.Executor.ChainID, head.Inner.Timestamp).Fork {
		h.Inner.Timestamp = head.Inner.Timestamp + 1
	}
	h.Inner.BaseFeePerGas = maxFeeOf(tx)
	return &h
}

func maxFeeOf(tx *types.Transaction) uint64 {
	var fee u256.Int
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		fee = d.GasPrice
	case *types.AccessListTx:
		fee = d.GasPrice
	case *types.DynamicFeeTx:
		fee = d.MaxFeePerGas
	case *types.SetCodeTx:
		fee = d.MaxFeePerGas
	case *types.TempoTx:
		fee = d.MaxFeePerGas
	}
	if !fee.IsUint64() {
		return ^uint64(0)
	}
	return fee.Uint64()
}

func (p *Pool) insert(t *Tx) error {
	if t.Info.ExpiryNonce {
		if err := p.room(t); err != nil {
			return err
		}
		p.add(t)
		return nil
	}
	l := p.lanes[t.lane]
	if old := l.lookup(t.Info.Nonce); old != nil {
		if !t.Info.Outbids(&old.Info, p.cfg.PriceBumpPercent) {
			return ErrReplacementUnderpriced
		}
		p.drop(old)
		p.add(t)
		p.laneFor(t).txs[t.Info.Nonce] = t
		return nil
	}
	if err := p.room(t); err != nil {
		return err
	}
	p.add(t)
	p.laneFor(t).txs[t.Info.Nonce] = t
	return nil
}

func (l *lane) lookup(nonce uint64) *Tx {
	if l == nil {
		return nil
	}
	return l.txs[nonce]
}

func (p *Pool) room(t *Tx) error {
	if len(p.byHash) >= p.cfg.MaxTxs {
		return ErrPoolFull
	}
	if p.senders[t.Info.Sender] >= p.cfg.MaxPerSender {
		return ErrSenderLimit
	}
	return nil
}

func (p *Pool) laneFor(t *Tx) *lane {
	l := p.lanes[t.lane]
	if l == nil {
		p.seq++
		l = &lane{key: t.lane, id: p.seq, base: t.Info.StateNonce, txs: map[uint64]*Tx{}}
		p.lanes[t.lane] = l
		p.laneIDs[l.id] = l
	}
	l.base = max(l.base, t.Info.StateNonce)
	return l
}

func (p *Pool) add(t *Tx) {
	p.seq++
	t.seq = p.seq
	p.byHash[t.Hash] = t
	p.bySeq[t.seq] = t
	p.senders[t.Info.Sender]++
}

func (p *Pool) drop(t *Tx) {
	delete(p.byHash, t.Hash)
	delete(p.bySeq, t.seq)
	if l := p.lanes[t.lane]; !t.Info.ExpiryNonce && l.lookup(t.Info.Nonce) == t {
		delete(l.txs, t.Info.Nonce)
		if len(l.txs) == 0 {
			delete(p.lanes, t.lane)
			delete(p.laneIDs, l.id)
		}
	}
	p.senders[t.Info.Sender]--
	if p.senders[t.Info.Sender] <= 0 {
		delete(p.senders, t.Info.Sender)
	}
}

func (p *Pool) Remove(h Hash) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	t, ok := p.byHash[h]
	if ok {
		p.drop(t)
	}
	return ok
}

func (p *Pool) OnBlock(blk *types.Block, st executor.State) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range blk.Transactions {
		if t, ok := p.byHash[blk.Transactions[i].Hash()]; ok {
			p.drop(t)
		}
	}
	ts := blk.Header.Inner.Timestamp
	fork := chain.RulesAt(p.cfg.Executor.ChainID, ts).Fork
	for _, seq := range slices.Sorted(maps.Keys(p.bySeq)) {
		t := p.bySeq[seq]
		if t.Info.ValidBefore != 0 && t.Info.ValidBefore <= ts {
			p.drop(t)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(p.laneIDs)) {
		l := p.laneIDs[id]
		base, err := p.cfg.Executor.LaneNonce(st, fork, l.key.sender, l.key.key)
		if err != nil {
			return err
		}
		l.base = base
		for _, n := range slices.Sorted(maps.Keys(l.txs)) {
			if n < base {
				p.drop(l.txs[n])
			}
		}
	}
	return nil
}
