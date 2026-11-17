package executor_test

import (
	"errors"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

func TestBlockRunMatchesMainnetBlocks(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-user-3211-3857.blk")
	db := openGenesis(t)
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	zero := func(uint64) primitives.Hash { return primitives.Hash{} }
	for i := range blocks {
		blk := &blocks[i]
		h := &blk.Header.Inner
		run := x.Begin(db.State(), &blk.Header, zero)
		for j := range blk.Transactions {
			tx := &blk.Transactions[j]
			_, err := run.Apply(tx, executor.RecoverSender(tx))
			assert.NoError(err, "apply mainnet tx")
		}
		res, err := run.Finish()
		assert.NoError(err, "finish")
		assert.Equal(res.GasUsed, h.GasUsed, "gas used")
		assert.Equal(res.ReceiptsRoot, h.ReceiptsRoot, "receipts root")
		pending, err := db.State().PendingRoot()
		assert.NoError(err, "pending root")
		assert.Equal(pending, h.StateRoot, "pending state root")
		root, err := db.State().Commit(h.Number)
		assert.NoError(err, "commit")
		assert.Equal(root, h.StateRoot, "state root")
	}
}

func TestBlockRunRollsBackARejectedTx(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-user-3211-3857.blk")
	db := openGenesis(t)
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	zero := func(uint64) primitives.Hash { return primitives.Hash{} }
	blk := &blocks[0]
	h := &blk.Header.Inner
	run := x.Begin(db.State(), &blk.Header, zero)
	first := &blk.Transactions[0]
	sender := executor.RecoverSender(first)
	for j := range blk.Transactions {
		tx := &blk.Transactions[j]
		_, err := run.Apply(tx, executor.RecoverSender(tx))
		assert.NoError(err, "apply mainnet tx")
		if j == 0 {
			_, err = run.Apply(first, sender)
			assert.Ok(errors.Is(err, executor.ErrNonce), "replayed tx is rejected for its nonce")
		}
	}
	_, err := run.Apply(first, sender)
	assert.Ok(err != nil, "no tx after the system tx")
	res, err := run.Finish()
	assert.NoError(err, "finish")
	assert.Equal(res.GasUsed, h.GasUsed, "gas used")
	assert.Equal(res.ReceiptsRoot, h.ReceiptsRoot, "receipts root")
	root, err := db.State().Commit(h.Number)
	assert.NoError(err, "commit")
	assert.Equal(root, h.StateRoot, "state root after a rolled back tx")
}
