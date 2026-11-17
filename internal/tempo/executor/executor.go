package executor

import (
	"errors"
	"fmt"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
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
	ChainID hardfork.ChainID
}

type Input struct {
	Block   *types.Block
	GetHash func(number uint64) Hash
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
}

func (x *Executor) Execute(st State, in Input) (Result, error) {
	assert.Ok(in.Block != nil, "executor needs a block")
	header := &in.Block.Header
	rules := chain.RulesAt(x.ChainID, header.Inner.Timestamp)
	set := precompiles.NewSet(rules.Fork, rules.EthereumPrecompiles())
	ts := chain.Timestamp{Seconds: header.Inner.Timestamp, MillisPart: header.TimestampMillisPart}
	cfg := rules.Config(ts.Millis(), set)
	blk := blockContext(header, in.GetHash)
	preBlock(st, &systemEnv{config: cfg, block: blk, header: header, rules: rules})
	txs := in.Block.Transactions
	if len(txs) > blockTxsMax {
		return Result{}, ErrTooManyTxs
	}
	receipts := make([]types.Receipt, 0, len(txs))
	var cumulative uint64
	for i := 0; i < len(txs); i++ {
		env := &TxEnv{State: st, Rules: rules, Config: cfg, Block: blk, Tx: &txs[i], Index: i}
		out, err := apply(env, set, len(txs))
		if err != nil {
			return Result{}, fmt.Errorf("tx %d: %w", i, err)
		}
		cumulative += out.GasUsed
		receipts = append(receipts, receiptOf(txs[i].Type(), out, cumulative))
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
	h := &header.Inner
	return &evm.BlockContext{
		Coinbase:   h.Beneficiary,
		Number:     h.Number,
		Time:       h.Timestamp,
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

func finish(receipts []types.Receipt, gasUsed uint64) Result {
	encoded := make([][]byte, len(receipts))
	var bloom types.Bloom
	for i := 0; i < len(receipts); i++ {
		encoded[i] = receipts[i].Encode()
		bloom.Or(&receipts[i].Bloom)
	}
	return Result{GasUsed: gasUsed, Receipts: receipts, ReceiptsRoot: trie.OrderedRoot(encoded), Bloom: bloom}
}
