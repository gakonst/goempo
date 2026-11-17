package builder

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/txpool"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

type Hash = primitives.Hash

type Address = primitives.Address

const (
	millisPerSecond         = 1000
	rlpBlockSizeMax         = 8_388_608
	nonTransactionSizeGuess = 2048
	gasLimitBoundDivisor    = 1024
	gasLimitMin             = 5000
	rlpShortHeader          = 1
	rlpLongHeaderMax        = 9
	rlpShortPayloadMax      = 55
	headerOptionalFieldsT4  = 6
	_                       = uint(rlpBlockSizeMax - nonTransactionSizeGuess)
	_                       = uint(gasLimitBoundDivisor - rlpShortPayloadMax)
	_                       = uint(rlpLongHeaderMax - rlpShortHeader)
	_                       = uint(gasLimitMin - gasLimitBoundDivisor)
	_                       = uint(rlpShortPayloadMax - headerOptionalFieldsT4)
)

var (
	ErrForkUnsupported = errors.New("builder: building blocks with a system transaction (before T4) is not supported")
	ErrTimestamp       = errors.New("builder: timestamp is not after the parent's")
)

type State interface {
	executor.State
	PendingRoot() (Hash, error)
}

type Attributes struct {
	TimestampMillis uint64
	ExtraData       []byte
	Beneficiary     Address
	PrevRandao      Hash
	Consensus       *types.ConsensusContext
	GasLimit        uint64
	GetHash         func(number uint64) Hash
}

type Stats struct {
	Offered       int
	Included      int
	Skipped       int
	PaymentTxs    int
	NonPaymentGas uint64
}

type Built struct {
	Block    types.Block
	Receipts []types.Receipt
	Stats    Stats
}

type Builder struct {
	pool *txpool.Pool
}

func New(pool *txpool.Pool) *Builder {
	assert.Ok(pool != nil, "a builder needs a pool")
	return &Builder{pool: pool}
}

func (a *Attributes) gasLimitAfter(parent uint64) uint64 {
	target := a.GasLimit
	if target == 0 {
		return parent
	}
	delta := parent/gasLimitBoundDivisor - 1
	if target > parent {
		return min(parent+delta, target)
	}
	return max(parent-delta, target, gasLimitMin)
}

func Header(x *executor.Executor, parent *types.Block, attrs *Attributes) (types.Header, error) {
	ph := &parent.Header.Inner
	ts := attrs.TimestampMillis / millisPerSecond
	if ts < ph.Timestamp {
		return types.Header{}, fmt.Errorf("%w: %d < %d", ErrTimestamp, ts, ph.Timestamp)
	}
	rules := chain.RulesAt(x.ChainID, ts)
	if rules.HasSystemTransaction() {
		return types.Header{}, ErrForkUnsupported
	}
	gasLimit := attrs.gasLimitAfter(ph.GasLimit)
	h := types.Header{
		GeneralGasLimit:     rules.GeneralGasLimit(gasLimit),
		SharedGasLimit:      rules.SharedGasLimit(gasLimit),
		TimestampMillisPart: attrs.TimestampMillis % millisPerSecond,
		Consensus:           attrs.Consensus,
	}
	h.Inner = types.EthHeader{
		ParentHash:     parent.Hash(),
		OmmersHash:     keccak.Sum256(rlp.EncodeList()),
		Beneficiary:    attrs.Beneficiary,
		Number:         ph.Number + 1,
		GasLimit:       gasLimit,
		Timestamp:      ts,
		ExtraData:      attrs.ExtraData,
		MixHash:        attrs.PrevRandao,
		OptionalFields: max(ph.OptionalFields, headerOptionalFieldsT4),
		BaseFeePerGas:  rules.ExpectedBaseFee(chain.ParentGas{BaseFee: ph.BaseFeePerGas, GasUsed: ph.GasUsed}),
		RequestsHash:   sha256.Sum256(nil),
	}
	h.Inner.WithdrawalsRoot = trie.OrderedRoot(nil)
	return h, nil
}

type session struct {
	run        *executor.BlockRun
	rules      chain.Rules
	gasLimit   uint64
	general    uint64
	cumulative uint64
	nonPayment uint64
	size       int
	txs        []types.Transaction
	stats      Stats
}

func (b *Builder) Build(st State, x *executor.Executor, parent *types.Block, attrs Attributes) (Built, error) {
	assert.Ok(attrs.GetHash != nil, "a build needs block hashes")
	h, err := Header(x, parent, &attrs)
	if err != nil {
		return Built{}, err
	}
	s := &session{
		rules:    chain.RulesAt(x.ChainID, h.Inner.Timestamp),
		gasLimit: h.Inner.GasLimit - h.SharedGasLimit,
		general:  h.GeneralGasLimit,
		size:     nonTransactionSizeGuess + len(attrs.ExtraData),
	}
	s.run = x.Begin(st, &h, attrs.GetHash)
	best := b.pool.Best(txpool.BlockEnv{BaseFee: h.Inner.BaseFeePerGas, Timestamp: h.Inner.Timestamp})
	limit := b.pool.Len()
	for range limit {
		t, ok := best.Next()
		if !ok {
			break
		}
		s.offer(best, t)
	}
	res, err := s.run.Finish()
	if err != nil {
		return Built{}, err
	}
	h.Inner.GasUsed = res.GasUsed
	h.Inner.ReceiptsRoot = res.ReceiptsRoot
	h.Inner.LogsBloom = res.Bloom
	blk := types.Block{Header: h, Transactions: s.txs, HasWithdrawals: true}
	blk.Header.Inner.TransactionsRoot = trie.OrderedRoot(blk.TransactionEnvelopes())
	if blk.Header.Inner.StateRoot, err = st.PendingRoot(); err != nil {
		return Built{}, err
	}
	return Built{Block: blk, Receipts: res.Receipts, Stats: s.stats}, nil
}

func (s *session) offer(best *txpool.Best, t *txpool.Tx) {
	s.stats.Offered++
	maxGas := min(t.Info.GasLimit, s.rules.TxGasLimitCap())
	if s.cumulative+maxGas > s.gasLimit {
		s.skip(best, t)
		return
	}
	payment := IsPayment(&t.Tx, s.rules)
	if !payment && s.nonPayment+maxGas > s.general {
		s.skip(best, t)
		return
	}
	size := s.size + bodyLength(&t.Tx, len(t.Raw))
	if size > rlpBlockSizeMax {
		s.skip(best, t)
		return
	}
	out, err := s.run.Apply(&t.Tx, t.Sender)
	if err != nil {
		s.skip(best, t)
		return
	}
	s.size = size
	s.cumulative += out.GasUsed
	if payment {
		s.stats.PaymentTxs++
	} else {
		s.nonPayment += out.GasUsed
	}
	s.stats.NonPaymentGas = s.nonPayment
	s.stats.Included++
	s.txs = append(s.txs, t.Tx)
}

func (s *session) skip(best *txpool.Best, t *txpool.Tx) {
	s.stats.Skipped++
	best.MarkInvalid(t)
}

func bodyLength(tx *types.Transaction, n int) int {
	if tx.Type() == types.TxTypeLegacy {
		return n
	}
	if n <= rlpShortPayloadMax {
		return n + rlpShortHeader
	}
	return n + rlpLongHeaderMax
}
