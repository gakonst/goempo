package executor

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type (
	Address = primitives.Address
	Hash    = primitives.Hash
)

const (
	blockTxsMax = 1 << 16
	txLogsMax   = 1 << 20
)

const _ = uint(txLogsMax - blockTxsMax)

var (
	ErrSystemTx      = errors.New("executor: system transaction failed")
	ErrSystemTxPlace = errors.New("executor: system transaction is not last")
	ErrSystemTxFork  = errors.New("executor: system transaction after T4")
	ErrTooManyTxs    = errors.New("executor: too many transactions")
)

type State interface {
	evm.State
	Logs() []evm.Log
	FinalizeTx()
}

type Executor struct {
	ChainID     hardfork.ChainID
	EpochLength uint64
	Configs     *chain.Cache
}

type Input struct {
	Block   *types.Block
	GetHash func(number uint64) Hash
	Senders []Sender
	// Receipts, when not nil, is a buffer Execute appends the block's
	// receipts to, so a caller that is done with the last block's receipts
	// can reuse their storage.
	Receipts []types.Receipt
}

type Result struct {
	GasUsed      uint64
	Receipts     []types.Receipt
	ReceiptsRoot Hash
	Bloom        types.Bloom
}

type TxOutcome struct {
	GasUsed uint64
	Success bool
	Logs    []evm.Log
}

type TxEnv struct {
	State  State
	Rules  chain.Rules
	Config *evm.Config
	Block  *evm.BlockContext
	Tx     *types.Transaction
	Index  int
	Sim    *Simulation
	Sender *Sender
}

func (x *Executor) SystemSteps(st State, in Input) error {
	assert.Ok(in.Block != nil, "system steps need a block")
	header := &in.Block.Header
	rules := chain.RulesAt(x.ChainID, header.Inner.Timestamp)
	set := precompiles.NewSet(rules.Fork, x.Configs.EthereumPrecompiles(rules))
	sys := &systemEnv{config: x.Configs.Config(rules, set), block: blockContext(header, in.GetHash), header: header, rules: rules}
	preBlock(st, sys)
	return x.committeeCall(st, sys)
}

// Execute runs in.Block on st. Result.Receipts reuses in.Receipts' backing
// array when it is large enough.
func (x *Executor) Execute(st State, in Input) (Result, error) {
	assert.Ok(in.Block != nil, "executor needs a block")
	header := &in.Block.Header
	scratch := x.borrowScratch(header, in.GetHash)
	defer returnScratch(scratch)
	rules, set, cfg, blk, sys := scratch.sys.rules, &scratch.set, &scratch.cfg, &scratch.blk, &scratch.sys
	preBlock(st, sys)
	txs := in.Block.Transactions
	if len(txs) > blockTxsMax {
		return Result{}, ErrTooManyTxs
	}
	receipts := in.Receipts[:0]
	if cap(receipts) < len(txs) {
		receipts = make([]types.Receipt, 0, len(txs))
	}
	var cumulative uint64
	env := &scratch.env
	for i := 0; i < len(txs); i++ {
		*env = TxEnv{State: st, Rules: rules, Config: cfg, Block: blk, Tx: &txs[i], Index: i}
		if len(in.Senders) == len(txs) {
			env.Sender = &in.Senders[i]
		}
		out, err := apply(env, set, len(txs))
		if err != nil {
			return Result{}, fmt.Errorf("tx %d: %w", i, err)
		}
		cumulative += out.GasUsed
		receipts = append(receipts, receiptOf(txs[i].Type(), out, cumulative))
	}
	if err := x.committeeCall(st, sys); err != nil {
		return Result{}, err
	}
	return finish(receipts, cumulative), nil
}

func apply(env *TxEnv, set *precompiles.Set, count int) (TxOutcome, error) {
	if IsSystemTx(env.Tx) {
		if !env.Rules.HasSystemTransaction() {
			return TxOutcome{}, ErrSystemTxFork
		}
		if env.Index != count-1 {
			return TxOutcome{}, ErrSystemTxPlace
		}
		return applySystemTx(env)
	}
	return applyTx(env, set)
}

func blockContext(header *types.Header, getHash func(uint64) Hash) *evm.BlockContext {
	blk := new(evm.BlockContext)
	fillBlockContext(blk, header, getHash)
	return blk
}

func fillBlockContext(dst *evm.BlockContext, header *types.Header, getHash func(uint64) Hash) {
	h := &header.Inner
	ts := chain.Timestamp{Seconds: h.Timestamp, MillisPart: header.TimestampMillisPart}
	*dst = evm.BlockContext{
		Coinbase:   h.Beneficiary,
		Number:     h.Number,
		Time:       h.Timestamp,
		TimeMillis: ts.Millis(),
		GasLimit:   h.GasLimit,
		BaseFee:    u256.FromUint64(h.BaseFeePerGas),
		PrevRandao: h.MixHash,
		GetHash:    getHash,
	}
}

func receiptOf(kind types.TxType, out TxOutcome, cumulative uint64) types.Receipt {
	assert.Ok(len(out.Logs) <= txLogsMax, "logs of one transaction are bounded")
	logs := make([]types.Log, len(out.Logs))
	for i := 0; i < len(out.Logs); i++ {
		l := &out.Logs[i]
		logs[i] = types.Log{Address: l.Address, Topics: l.Topics, Data: l.Data}
	}
	return types.Receipt{
		Type:              kind,
		Success:           out.Success,
		CumulativeGasUsed: cumulative,
		Bloom:             types.LogsBloom(logs),
		Logs:              logs,
	}
}

// receiptScratch holds a block's receipt encodings while finish computes
// their root; finish borrows one from receiptScratches and returns it.
type receiptScratch struct {
	enc     *rlp.Encoder
	ends    []int
	encoded [][]byte
}

// receiptScratchRetainMax caps the encoding buffer a scratch keeps.
const receiptScratchRetainMax = 1 << 24

const receiptScratchHint = 1 << 16

const _ = uint(receiptScratchRetainMax - receiptScratchHint)

var receiptScratches = sync.Pool{New: func() any {
	return &receiptScratch{enc: rlp.NewEncoder(receiptScratchHint)}
}}

func finish(receipts []types.Receipt, gasUsed uint64) Result {
	sc, ok := receiptScratches.Get().(*receiptScratch)
	assert.Ok(ok, "the receipt scratch pool holds scratches")
	sc.enc.Reset()
	sc.ends = sc.ends[:0]
	var bloom types.Bloom
	for i := 0; i < len(receipts); i++ {
		receipts[i].EncodeTo(sc.enc)
		sc.ends = append(sc.ends, sc.enc.Len())
		bloom.Or(&receipts[i].Bloom)
	}
	buf := sc.enc.Encoded()
	sc.encoded = sc.encoded[:0]
	start := 0
	for i := 0; i < len(sc.ends); i++ {
		sc.encoded = append(sc.encoded, buf[start:sc.ends[i]:sc.ends[i]])
		start = sc.ends[i]
	}
	assert.Equal(start, len(buf), "receipt encodings tile the scratch buffer")
	root := receiptsRoot(sc.enc, sc.encoded)
	clear(sc.encoded)
	if sc.enc.Cap() <= receiptScratchRetainMax {
		receiptScratches.Put(sc)
	}
	return Result{GasUsed: gasUsed, Receipts: receipts, ReceiptsRoot: root, Bloom: bloom}
}

// singleLeafKey is the hex-prefix encoding of the only key in a one-item
// ordered trie: rlp(0) = 0x80, the nibbles 8 and 0 of a leaf path.
var singleLeafKey = [2]byte{0x20, 0x80}

// receiptsRoot is trie.OrderedRoot(items), with the one-receipt block (most
// blocks: one system transaction) hashed straight from its leaf node, built
// in e after the items. items must alias e's buffer or be independent of it.
func receiptsRoot(e *rlp.Encoder, items [][]byte) Hash {
	if len(items) != 1 {
		return trie.OrderedRoot(items)
	}
	start := e.Len()
	e.ListStart()
	e.WriteBytes(singleLeafKey[:])
	e.WriteBytes(items[0])
	e.ListEnd()
	leaf := e.Encoded()[start:]
	// A leaf shorter than a hash would be embedded rather than hashed; a
	// receipt carries a 256-byte bloom, so its leaf never is.
	assert.Ok(len(leaf) >= primitives.HashLength, "a receipt leaf is hashed")
	return keccak.Sum256(leaf)
}
