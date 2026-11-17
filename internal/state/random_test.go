package state_test

import (
	"bytes"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	fuzzAccounts = 24
	fuzzSlots    = 12
)

type fuzzer struct {
	t     *testing.T
	rng   *rand.Rand
	model *memstate.State
	fs    *goio.MemFS
	opts  state.Options
	db    *state.DB
}

func newFuzzer(t *testing.T, seed uint64, opts state.Options) *fuzzer {
	fs := goio.NewMemFS()
	return &fuzzer{
		t:     t,
		rng:   rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		model: memstate.New(),
		fs:    fs,
		opts:  opts,
		db:    openDB(t, fs, opts),
	}
}

func (f *fuzzer) addr() primitives.Address {
	var a primitives.Address
	a[0] = byte(f.rng.IntN(fuzzAccounts))
	a[19] = 0x77
	return a
}

func (f *fuzzer) slot() primitives.Hash {
	var h primitives.Hash
	h[31] = byte(f.rng.IntN(fuzzSlots))
	h[0] = byte(f.rng.IntN(2))
	return h
}

func (f *fuzzer) value() u256.Int {
	if f.rng.IntN(3) == 0 {
		return u256.Zero()
	}
	return u256.FromUint64(f.rng.Uint64N(1000) + 1)
}

func (f *fuzzer) step(states [2]evm.State) {
	a := f.addr()
	op := f.rng.IntN(16)
	v := f.value()
	k := f.slot()
	code := []byte{byte(f.rng.IntN(4)), 0x60}
	if f.rng.IntN(3) == 0 {
		code = nil
	}
	nonce := f.rng.Uint64N(4)
	destruct := f.rng.IntN(4) == 0
	for _, s := range states {
		switch op {
		case 0, 1:
			s.AddBalance(a, v)
		case 2:
			if s.Balance(a).Cmp(v) >= 0 {
				s.SubBalance(a, v)
			}
		case 3:
			s.SetNonce(a, nonce)
		case 4:
			s.SetCode(a, code)
		case 5, 6, 7:
			s.SetStorage(a, k, v)
		case 8:
			s.CreateAccount(a)
		case 9:
			if destruct {
				s.SelfDestruct(a)
			}
		case 10:
			s.Touch(a)
		case 11:
			s.SetTransientStorage(a, k, v)
		case 12:
			s.WarmSlot(a, k)
		case 13:
			s.WarmAddress(a)
		case 14:
			s.AddRefund(int64(nonce) - 1)
		case 15:
			s.AddLog(evm.Log{Address: a, Data: code})
		}
	}
	f.compare(states, a, k)
}

func (f *fuzzer) compare(states [2]evm.State, a primitives.Address, k primitives.Hash) {
	m, s := states[0], states[1]
	f.t.Helper()
	if m.Exist(a) != s.Exist(a) || m.Empty(a) != s.Empty(a) {
		f.t.Fatalf("exist/empty of %s: model %v/%v state %v/%v", a, m.Exist(a), m.Empty(a), s.Exist(a), s.Empty(a))
	}
	if m.Balance(a) != s.Balance(a) || m.Nonce(a) != s.Nonce(a) || m.CodeHash(a) != s.CodeHash(a) {
		f.t.Fatalf("account %s differs", a)
	}
	if string(m.Code(a)) != string(s.Code(a)) {
		f.t.Fatalf("code of %s differs", a)
	}
	if m.Storage(a, k) != s.Storage(a, k) || m.OriginalStorage(a, k) != s.OriginalStorage(a, k) {
		f.t.Fatalf("slot %s/%s: model %v/%v state %v/%v", a, k, m.Storage(a, k), m.OriginalStorage(a, k), s.Storage(a, k), s.OriginalStorage(a, k))
	}
	if m.TransientStorage(a, k) != s.TransientStorage(a, k) || m.SlotWarm(a, k) != s.SlotWarm(a, k) {
		f.t.Fatalf("transient or warm slot %s/%s differs", a, k)
	}
	if m.HasStorage(a) != s.HasStorage(a) || m.HasSelfDestructed(a) != s.HasSelfDestructed(a) || m.CreatedInTx(a) != s.CreatedInTx(a) {
		f.t.Fatalf("flags of %s differ: has %v/%v destructed %v/%v created %v/%v", a, m.HasStorage(a), s.HasStorage(a), m.HasSelfDestructed(a), s.HasSelfDestructed(a), m.CreatedInTx(a), s.CreatedInTx(a))
	}
	if m.AddressWarm(a) != s.AddressWarm(a) || len(f.model.Logs()) != len(f.db.State().Logs()) {
		f.t.Fatalf("warm address or logs differ")
	}
	if m.Refund() != s.Refund() {
		f.t.Fatalf("refund differs")
	}
}

func (f *fuzzer) tx() {
	st := f.db.State()
	states := [2]evm.State{f.model, st}
	steps := 1 + f.rng.IntN(12)
	var snaps [2][]int
	for i := 0; i < steps; i++ {
		switch f.rng.IntN(6) {
		case 0:
			snaps[0] = append(snaps[0], f.model.Snapshot())
			snaps[1] = append(snaps[1], st.Snapshot())
		case 1:
			if len(snaps[0]) > 0 {
				j := f.rng.IntN(len(snaps[0]))
				f.model.RevertToSnapshot(snaps[0][j])
				st.RevertToSnapshot(snaps[1][j])
				snaps[0] = snaps[0][:j]
				snaps[1] = snaps[1][:j]
			}
		default:
			f.step(states)
		}
	}
	f.model.FinalizeTx()
	st.FinalizeTx()
}

func (f *fuzzer) block(number uint64) {
	txs := 1 + f.rng.IntN(5)
	for i := 0; i < txs; i++ {
		f.tx()
	}
	root, err := f.db.State().Commit(number)
	must(f.t, err)
	want := modelRoot(f.model)
	if root != want {
		f.t.Fatalf("block %d root %s want %s", number, root, want)
	}
	if f.db.Head().Root != root || f.db.Head().Number != number {
		f.t.Fatalf("head %+v after block %d", f.db.Head(), number)
	}
	f.checkAccounts()
}

func (f *fuzzer) checkAccounts() {
	st := f.db.State()
	for i := 0; i < fuzzAccounts; i++ {
		var a primitives.Address
		a[0] = byte(i)
		a[19] = 0x77
		want, ok := f.model.Get(a)
		got, gok := st.Account(a)
		if ok != gok {
			f.t.Fatalf("account %s exists %v want %v", a, gok, ok)
		}
		if !ok {
			continue
		}
		if got.Nonce != want.Nonce || got.Balance != want.Balance || got.CodeHash != keccak.Sum256(want.Code) {
			f.t.Fatalf("account %s: %+v", a, got)
		}
		if got.StorageRoot != storageRoot(want.Storage) {
			f.t.Fatalf("account %s storage root %s", a, got.StorageRoot)
		}
	}
}

type reopenMode bool

const (
	cleanClose      reopenMode = false
	crashAfterFlush reopenMode = true
)

func (f *fuzzer) reopen(mode reopenMode) {
	head := f.db.Head()
	if mode == crashAfterFlush {
		must(f.t, f.db.Flush())
		f.fs.Crash()
	} else {
		must(f.t, f.db.Close())
	}
	f.db = openDB(f.t, f.fs, f.opts)
	if f.db.Head() != head {
		f.t.Fatalf("reopened head %+v want %+v", f.db.Head(), head)
	}
	f.checkAccounts()
}

func storageRoot(slots map[primitives.Hash]u256.Int) primitives.Hash {
	t := trie.New()
	enc := rlp.NewEncoder(64)
	for _, k := range slices.SortedFunc(maps.Keys(slots), func(a, b primitives.Hash) int { return bytes.Compare(a[:], b[:]) }) {
		v := slots[k]
		if v.IsZero() {
			continue
		}
		b := v.Bytes32()
		enc.Reset()
		enc.WriteBytes(primitives.TrimLeftZeros(b[:]))
		hk := keccak.Sum256(k[:])
		t.Update(hk[:], append([]byte(nil), enc.Encoded()...))
	}
	return t.Hash()
}

func modelRoot(m *memstate.State) primitives.Hash {
	t := trie.New()
	enc := rlp.NewEncoder(128)
	for _, a := range m.Addresses() {
		acc, _ := m.Get(a)
		sr := storageRoot(acc.Storage)
		ch := keccak.Sum256(acc.Code)
		bal := acc.Balance.Bytes32()
		enc.Reset()
		enc.ListStart()
		enc.WriteUint64(acc.Nonce)
		enc.WriteUintBytes(bal[:])
		enc.WriteBytes(sr[:])
		enc.WriteBytes(ch[:])
		enc.ListEnd()
		ha := keccak.Sum256(a[:])
		t.Update(ha[:], append([]byte(nil), enc.Encoded()...))
	}
	return t.Hash()
}

func runFuzz(t *testing.T, seed uint64, opts state.Options, blocks int) {
	f := newFuzzer(t, seed, opts)
	for n := 1; n <= blocks; n++ {
		f.block(uint64(n))
		switch f.rng.IntN(8) {
		case 0:
			f.reopen(cleanClose)
		case 1:
			f.reopen(crashAfterFlush)
		}
	}
	must(t, f.db.Close())
}

func TestRandomBlocksMatchRebuiltTrie(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		runFuzz(t, seed, state.Options{}, 40)
	}
}

func TestRandomBlocksWithTinyCaches(t *testing.T) {
	opts := state.Options{
		MemtableBytes:  4 << 10,
		CacheAccounts:  4,
		CacheSlots:     8,
		AccountNodes:   4,
		StorageTries:   2,
		StorageNodes:   2,
		CodeCacheBytes: 4,
		TrieKeepDepth:  1,
	}
	for seed := uint64(100); seed <= 120; seed++ {
		runFuzz(t, seed, opts, 40)
	}
}
