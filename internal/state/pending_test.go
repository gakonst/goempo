package state_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/state"
	"github.com/kapetan-io/tiger/assert"
)

func TestPendingRootMatchesCommit(t *testing.T) {
	const blocks = 120
	f := newFuzzer(t, 7, state.Options{})
	for n := uint64(1); n <= blocks; n++ {
		txs := 1 + f.rng.IntN(5)
		for i := 0; i < txs; i++ {
			f.tx()
		}
		pending, err := f.db.State().PendingRoot()
		assert.NoError(err, "pending root")
		assert.Equal(pending, modelRoot(f.model), "pending root matches the model")
		again, err := f.db.State().PendingRoot()
		assert.NoError(err, "pending root again")
		assert.Equal(again, pending, "pending root is repeatable")
		root, err := f.db.State().Commit(n)
		assert.NoError(err, "commit")
		assert.Equal(root, pending, "commit root matches the pending root")
	}
}

func TestPendingRootLeavesHeadAlone(t *testing.T) {
	f := newFuzzer(t, 11, state.Options{})
	f.block(1)
	head := f.db.Head()
	f.tx()
	_, err := f.db.State().PendingRoot()
	assert.NoError(err, "pending root")
	assert.Equal(f.db.Head(), head, "head unchanged")
}
