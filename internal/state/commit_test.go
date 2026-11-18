package state_test

import (
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// largeBlock changes enough accounts and slots that the commit updates
// storage tries in parallel and saves the large ones per subtree: one hot
// account with hundreds of slots, a handful with dozens, and some balances.
func largeBlock(rng *rand.Rand, model *memstate.State, st *state.State) {
	hot := benchAddr(0)
	model.AddBalance(hot, u256.FromUint64(1))
	st.AddBalance(hot, u256.FromUint64(1))
	for range 300 + rng.IntN(300) {
		k := benchSlot(rng.IntN(4000))
		v := u256.FromUint64(rng.Uint64N(5))
		model.SetStorage(hot, k, v)
		st.SetStorage(hot, k, v)
	}
	for range 6 {
		a := benchAddr(1 + rng.IntN(40))
		model.AddBalance(a, u256.FromUint64(1))
		st.AddBalance(a, u256.FromUint64(1))
		for range 20 + rng.IntN(40) {
			k := benchSlot(rng.IntN(500))
			v := u256.FromUint64(rng.Uint64N(3))
			model.SetStorage(a, k, v)
			st.SetStorage(a, k, v)
		}
	}
	for range 30 {
		a := benchAddr(100 + rng.IntN(200))
		model.AddBalance(a, u256.FromUint64(1))
		st.AddBalance(a, u256.FromUint64(1))
	}
	model.FinalizeTx()
	st.FinalizeTx()
}

// TestLargeBlocksMatchModel commits large blocks, synchronously and
// pipelined, and checks each root against the model, then that the stored
// nodes and flat index reproduce the head after a reopen.
func TestLargeBlocksMatchModel(t *testing.T) {
	for _, pipelined := range []bool{false, true} {
		rng := rand.New(rand.NewPCG(5, 17))
		fs := goio.NewMemFS()
		db := openDB(t, fs, state.Options{})
		model := memstate.New()
		var want primitives.Hash
		for n := uint64(1); n <= 12; n++ {
			largeBlock(rng, model, db.State())
			want = modelRoot(model)
			if !pipelined {
				root, err := db.State().Commit(n)
				assert.NoError(err, "commit")
				assert.Equal(root, want, "commit root matches the model")
				continue
			}
			assert.NoError(db.State().CommitAsync(n), "commit async")
			if n%3 == 0 {
				c, err := db.State().WaitCommit()
				assert.NoError(err, "wait commit")
				assert.Equal(c.Root, want, "pipelined root matches the model")
				assert.Equal(c.Number, n, "pipelined block number")
			}
		}
		assert.NoError(db.Close(), "close")
		db = openDB(t, fs, state.Options{})
		assert.Equal(db.Head().Root, want, "reopened head root")
		root, err := db.RebuildRoot()
		assert.NoError(err, "rebuild root")
		assert.Equal(root, want, "flat index rebuilds the head root")
		hot, ok := db.State().Account(benchAddr(0))
		assert.Ok(ok, "hot account exists")
		m, _ := model.Get(benchAddr(0))
		assert.Equal(hot.StorageRoot, storageRoot(m.Storage), "hot account storage root")
		assert.NoError(db.Close(), "close reopened")
	}
}
