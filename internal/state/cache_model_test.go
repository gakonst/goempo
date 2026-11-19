package state_test

import (
	"bytes"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
)

func TestStorageCacheColdWarmEvictReopenModel(t *testing.T) {
	modes := []struct {
		name string
		opts state.Options
	}{
		{name: "default"},
		{name: "per-trie", opts: state.Options{StorageNodes: 32}},
		{name: "aggregate", opts: state.Options{StorageNodes: 32, StorageNodesTotal: 64}},
		{name: "cold-index", opts: state.Options{CacheAccounts: 1, CacheSlots: 1, StorageTries: 1, StorageNodes: 32, StorageNodesTotal: 64}},
	}
	var roots [8]primitives.Hash
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			f := newFuzzer(t, 991, mode.opts)
			for block := 1; block <= len(roots); block++ {
				cacheModelBlock(f, block)
				root, err := f.db.State().Commit(uint64(block))
				must(t, err)
				if want := modelRoot(f.model); root != want {
					t.Fatalf("block %d root %s want model %s", block, root, want)
				}
				if mode.name == "default" {
					roots[block-1] = root
				} else if root != roots[block-1] {
					t.Fatalf("block %d cache mode changed root", block)
				}
				checkCacheModelProofs(f)
				checkCacheModelProofs(f)
				f.checkRebuild()
				if block%2 == 0 {
					f.reopen(cleanClose)
					checkCacheModelProofs(f)
				}
			}
			must(t, f.db.Close())
		})
	}
}

func cacheModelBlock(f *fuzzer, block int) {
	states := [2]evm.State{f.model, f.db.State()}
	for _, st := range states {
		for a := 0; a < 4; a++ {
			addr := benchAddr(a)
			recreate := false
			if block == 5 {
				recreate = a == 1
			}
			if block == 1 || recreate {
				st.CreateAccount(addr)
				st.AddBalance(addr, u256.FromUint64(1))
				st.SetCode(addr, []byte{0x60, byte(a)})
				for k := 0; k < 1024; k++ {
					st.SetStorage(addr, benchSlot(k), u256.FromUint64(uint64(k+1)))
				}
			}
			for j := 0; j < 96; j++ {
				key := benchSlot((j*37 + block*11 + a*19) % 1152)
				v := u256.FromUint64(uint64(block*1000 + j + 1))
				if j%3 == 0 {
					v = u256.Zero()
				}
				st.SetStorage(addr, key, v)
			}
			snap := st.Snapshot()
			st.CreateAccount(addr)
			st.SetStorage(addr, benchSlot(2000), u256.FromUint64(99))
			st.RevertToSnapshot(snap)
			if block == 3 && a == 1 {
				st.SelfDestruct(addr)
			}
		}
	}
	f.model.FinalizeTx()
	f.db.State().FinalizeTx()
}

func checkCacheModelProofs(f *fuzzer) {
	f.t.Helper()
	for a := 0; a < 5; a++ {
		addr := benchAddr(a)
		keys := []primitives.Hash{benchSlot(0), benchSlot(22), benchSlot(512), benchSlot(1023), benchSlot(2000)}
		p, err := f.db.Proof(addr, keys)
		must(f.t, err)
		acc, exists := f.model.Get(addr)
		if p.Exists != exists {
			f.t.Fatalf("proof existence for %s differs", addr)
		}
		h := keccak.Sum256(addr[:])
		leaf, err := trie.VerifyProof(f.db.Head().Root, h[:], p.AccountProof)
		must(f.t, err)
		if !exists {
			if len(leaf) != 0 {
				f.t.Fatal("absent account has a proof leaf")
			}
			continue
		}
		root := storageRoot(acc.Storage)
		codeHash := keccak.Sum256(acc.Code)
		enc := rlp.NewEncoder(128)
		bal := acc.Balance.Bytes32()
		enc.ListStart()
		enc.WriteUint64(acc.Nonce)
		enc.WriteUintBytes(bal[:])
		enc.WriteBytes(root[:])
		enc.WriteBytes(codeHash[:])
		enc.ListEnd()
		if !bytes.Equal(leaf, enc.Encoded()) || p.StorageRoot != root {
			f.t.Fatal("account proof differs from independent state model")
		}
		for i, key := range keys {
			hk := keccak.Sum256(key[:])
			value, err := trie.VerifyProof(root, hk[:], p.Storage[i].Proof)
			must(f.t, err)
			want := acc.Storage[key]
			var encoded []byte
			if !want.IsZero() {
				enc.Reset()
				word := want.Bytes32()
				enc.WriteBytes(primitives.TrimLeftZeros(word[:]))
				encoded = enc.Encoded()
			}
			if !bytes.Equal(value, encoded) || p.Storage[i].Value != want {
				f.t.Fatalf("storage proof differs for %s/%s", addr, key)
			}
		}
	}
}
