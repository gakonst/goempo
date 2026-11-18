package executor

import (
	"errors"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

var ErrRunSealed = errors.New("executor: block run already holds its system transaction")

type BlockRun struct {
	x          *Executor
	st         State
	rules      chain.Rules
	set        *precompiles.Set
	cfg        *evm.Config
	blk        *evm.BlockContext
	sys        *systemEnv
	receipts   []types.Receipt
	cumulative uint64
	sealed     bool
	finished   bool
}

func (x *Executor) Begin(st State, header *types.Header, getHash func(number uint64) Hash) *BlockRun {
	assert.Ok(header != nil, "a block run needs a header")
	rules := chain.RulesAt(x.ChainID, header.Inner.Timestamp)
	set := precompiles.NewSet(rules.Fork, x.Configs.EthereumPrecompiles(rules))
	cfg := x.Configs.Config(rules, set)
	blk := blockContext(header, getHash)
	sys := &systemEnv{config: cfg, block: blk, header: header, rules: rules}
	finishPreBlock(st, sys)
	return &BlockRun{x: x, st: st, rules: rules, set: set, cfg: cfg, blk: blk, sys: sys}
}

func (b *BlockRun) Rules() chain.Rules { return b.rules }

func (b *BlockRun) GasUsed() uint64 { return b.cumulative }

func (b *BlockRun) Count() int { return len(b.receipts) }

func (b *BlockRun) Apply(tx *types.Transaction, sender Sender) (TxOutcome, error) {
	assert.Ok(!b.finished, "a finished block run takes no transactions")
	if b.sealed {
		return TxOutcome{}, ErrRunSealed
	}
	if len(b.receipts) >= blockTxsMax {
		return TxOutcome{}, ErrTooManyTxs
	}
	snap := b.st.Snapshot()
	env := &TxEnv{State: b.st, Rules: b.rules, Config: b.cfg, Block: b.blk, Tx: tx, Index: len(b.receipts), Sender: &sender}
	out, err := b.applyOne(env)
	if err != nil {
		b.st.RevertToSnapshot(snap)
		b.st.FinalizeTx()
		return TxOutcome{}, err
	}
	b.cumulative += out.GasUsed
	b.receipts = append(b.receipts, receiptOf(tx.Type(), out, b.cumulative))
	return out, nil
}

func (b *BlockRun) applyOne(env *TxEnv) (TxOutcome, error) {
	if !IsSystemTx(env.Tx) {
		return applyTx(env, b.set)
	}
	if !b.rules.HasSystemTransaction() {
		return TxOutcome{}, ErrSystemTxFork
	}
	out, err := applySystemTx(env)
	if err == nil {
		b.sealed = true
	}
	return out, err
}

func (b *BlockRun) Finish() (Result, error) {
	assert.Ok(!b.finished, "a block run finishes once")
	b.finished = true
	if err := b.x.committeeCall(b.st, b.sys); err != nil {
		return Result{}, err
	}
	return finish(b.receipts, b.cumulative), nil
}
