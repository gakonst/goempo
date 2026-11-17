package memstate_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

var (
	alice = primitives.Address{0xa1}
	bob   = primitives.Address{0xb0}
	key   = primitives.Hash{0x01}
)

func TestSnapshotRevertUndoesEveryChange(t *testing.T) {
	st := memstate.New()
	st.Put(alice, memstate.Account{Balance: u256.FromUint64(100), Nonce: 3})
	snap := st.Snapshot()
	st.SubBalance(alice, u256.FromUint64(40))
	st.AddBalance(bob, u256.FromUint64(40))
	st.SetNonce(alice, 4)
	st.SetStorage(alice, key, u256.FromUint64(7))
	st.SetTransientStorage(alice, key, u256.FromUint64(9))
	st.WarmAddress(bob)
	st.WarmSlot(alice, key)
	st.AddRefund(4800)
	st.AddLog(evm.Log{Address: alice})
	st.SetCode(bob, []byte{0x00})
	st.RevertToSnapshot(snap)
	assert.Equal(st.Balance(alice), u256.FromUint64(100), "balance restored")
	assert.Equal(st.Nonce(alice), uint64(3), "nonce restored")
	assert.Ok(!st.Exist(bob), "bob removed")
	assert.Ok(st.Storage(alice, key).IsZero(), "storage restored")
	assert.Ok(st.TransientStorage(alice, key).IsZero(), "transient restored")
	assert.Ok(!st.AddressWarm(bob), "warm address restored")
	assert.Ok(!st.SlotWarm(alice, key), "warm slot restored")
	assert.Equal(st.Refund(), int64(0), "refund restored")
	assert.Equal(len(st.Logs()), 0, "logs restored")
}

func TestOriginalStorageHoldsUntilFinalize(t *testing.T) {
	st := memstate.New()
	st.Put(alice, memstate.Account{Storage: map[primitives.Hash]u256.Int{key: u256.FromUint64(1)}})
	st.SetStorage(alice, key, u256.FromUint64(2))
	assert.Equal(st.OriginalStorage(alice, key), u256.FromUint64(1), "original before finalize")
	assert.Equal(st.Storage(alice, key), u256.FromUint64(2), "current")
	st.FinalizeTx()
	assert.Equal(st.OriginalStorage(alice, key), u256.FromUint64(2), "original after finalize")
	assert.Ok(st.TransientStorage(alice, key).IsZero(), "transient cleared per tx")
}

func TestFinalizeRemovesTouchedEmptyAndDestructed(t *testing.T) {
	st := memstate.New()
	st.Put(alice, memstate.Account{Balance: u256.FromUint64(5)})
	st.Put(bob, memstate.Account{Nonce: 1, Code: []byte{0x00}})
	st.AddBalance(primitives.Address{0xcc}, u256.Zero())
	st.SelfDestruct(bob)
	st.FinalizeTx()
	assert.Ok(st.Exist(alice), "non-empty account stays")
	assert.Ok(!st.Exist(bob), "destructed account removed")
	assert.Ok(!st.Exist(primitives.Address{0xcc}), "touched empty account removed")
}

func TestHasStorageSeesClearedSlots(t *testing.T) {
	st := memstate.New()
	st.Put(alice, memstate.Account{Storage: map[primitives.Hash]u256.Int{key: u256.FromUint64(1)}})
	assert.Ok(st.HasStorage(alice), "committed slot")
	st.SetStorage(alice, key, u256.Zero())
	assert.Ok(!st.HasStorage(alice), "cleared slot")
}
