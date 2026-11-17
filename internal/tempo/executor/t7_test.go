package executor_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

type fixture struct {
	block    string
	prestate string
	index    int
}

func applyFixture(t *testing.T, f fixture) executor.TxOutcome {
	blocks := readBlocks(t, f.block)
	assert.Equal(len(blocks), 1, "one block")
	tx := &blocks[0].Transactions[f.index]
	sender, err := tx.Sender()
	assert.NoError(err, "sender")
	st := loadPrestate(t, f.prestate)
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	out, err := x.ApplyCall(st, &blocks[0], tx, executor.Simulation{Sender: sender})
	assert.NoError(err, "apply")
	return out
}

func TestEVMStoreCreatingASlotPaysStorageCreditFromT7(t *testing.T) {
	out := applyFixture(t, fixture{block: "testdata/mainnet-29152422.blk", prestate: "testdata/mainnet-29152422-tx0-prestate.json", index: 0})
	assert.Ok(out.Success, "succeeds")
	assert.Equal(out.GasUsed, uint64(302_225), "a contract SSTORE from zero pays the 245,000 credit")
}

func TestExpiringNonceTransferWithMemoAtT7(t *testing.T) {
	out := applyFixture(t, fixture{block: "testdata/mainnet-29152424.blk", prestate: "testdata/mainnet-29152424-tx1-prestate.json", index: 1})
	assert.Ok(out.Success, "succeeds")
	assert.Equal(out.GasUsed, uint64(53_387), "matches the mainnet receipt")
}
