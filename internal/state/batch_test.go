package state_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
)

func TestBatchStorageStateModel(t *testing.T) {
	for _, opts := range []state.Options{{}, {StorageNodes: 32, StorageNodesTotal: 64, CacheSlots: 1}, {MemtableBytes: 4096, StorageTries: 1}} {
		f := newFuzzer(t, 937, opts)
		for block := 1; block <= 8; block++ {
			for _, st := range [2]evm.State{f.model, f.db.State()} {
				for a := 0; a < 3; a++ {
					addr := benchAddr(a)
					if block == 1 || block == 6 {
						st.CreateAccount(addr)
						st.AddBalance(addr, u256.FromUint64(1))
					}
					for i := 0; i < 512; i++ {
						k := benchSlot((i*37 + block*13) % 768)
						v := u256.FromUint64(uint64(block*1000 + i + 1))
						if (i+block)%5 == 0 {
							v = u256.Zero()
						}
						st.SetStorage(addr, k, v)
					}
					snapshot := st.Snapshot()
					for i := 0; i < 150; i++ {
						st.SetStorage(addr, benchSlot(i), u256.Zero())
					}
					st.RevertToSnapshot(snapshot)
					if block == 4 && a == 1 {
						st.SelfDestruct(addr)
					}
				}
			}
			f.model.FinalizeTx()
			f.db.State().FinalizeTx()
			root, err := f.db.State().Commit(uint64(block))
			must(t, err)
			if root != modelRoot(f.model) {
				t.Fatalf("block%d independent model mismatch", block)
			}
			f.checkRebuild()
			checkCacheModelProofs(f)
			if block%2 == 0 {
				f.reopen(cleanClose)
			}
		}
		must(t, f.db.Close())
	}
}

func batchStoreSnapshot(t *testing.T, store *storage.DB) map[string][]byte {
	t.Helper()
	snapshot := map[string][]byte{}
	must(t, store.Iterate(nil, func(e storage.Entry) error { snapshot[string(e.Key)] = bytes.Clone(e.Value); return nil }))
	return snapshot
}

func TestBatchCommitErrorDoesNotPublish(t *testing.T) {
	f := newFuzzer(t, 111, state.Options{})
	addr := benchAddr(0)
	st := f.db.State()
	st.CreateAccount(addr)
	st.AddBalance(addr, u256.FromUint64(1))
	for i := 0; i < 512; i++ {
		st.SetStorage(addr, benchSlot(i), u256.FromUint64(uint64(i+1)))
	}
	_, err := st.Commit(1)
	must(t, err)
	f.reopen(cleanClose)
	// Corrupt a first-nibble storage child, not the account/flat slot data.
	var target []byte
	must(t, f.db.Store().Iterate([]byte{'n'}, func(e storage.Entry) error {
		if len(e.Key) == 1+20+2 && e.Key[len(e.Key)-1] == 1 && target == nil {
			target = bytes.Clone(e.Key)
		}
		return nil
	}))
	if target == nil {
		t.Fatal("no first-nibble child in fixture")
	}
	var bad storage.Batch
	bad.Put(target, []byte{0xc0})
	must(t, f.db.Store().Write(&bad))
	before := batchStoreSnapshot(t, f.db.Store())
	head := f.db.Head()
	st = f.db.State()
	for i := 0; i < 512; i++ {
		st.SetStorage(addr, benchSlot(i), u256.FromUint64(uint64(i+1000)))
	}
	_, err = st.Commit(2)
	if !errors.Is(err, trie.ErrNodeHash) {
		t.Fatal("expected authenticated node failure", err)
	}
	if f.db.Head() != head || !reflect.DeepEqual(before, batchStoreSnapshot(t, f.db.Store())) {
		t.Fatal("failed commit published state")
	}
	_, again := st.Commit(3)
	if again != err {
		t.Fatal("failed DB was not poisoned", again)
	}
	must(t, f.db.Close())
}
