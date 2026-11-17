package executor_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

func TestApplyCallMatchesMainnetGas(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-user-3211-3857.blk")
	db := openGenesis(t)
	target := -1
	for i := range blocks {
		if blocks[i].Header.Inner.Number == 3663 {
			target = i
			break
		}
		checkBlock(t, db, &blocks[i])
	}
	assert.Ok(target >= 0, "block 3663 in fixture")
	tx := &blocks[target].Transactions[0]
	sender, err := tx.Sender()
	assert.NoError(err, "sender")
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	st := db.State()
	out, err := x.ApplyCall(st, &blocks[target], tx, executor.Simulation{Sender: sender})
	assert.NoError(err, "apply call")
	assert.Equal(out.GasUsed, uint64(45_124), "same gas as the mined setUserToken tx")
	assert.Ok(out.Success, "succeeds")
	st.Discard()
	out, err = x.ApplyCall(db.State(), &blocks[target], tx, executor.Simulation{Sender: sender, SkipNonceCheck: true})
	assert.NoError(err, "the discarded call left the nonce untouched")
	assert.Equal(out.GasUsed, uint64(45_124), "repeatable")
}
