package state_test

import (
	"errors"
	"testing"

	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
)

func openDB(t testing.TB, fs goio.FS, opts state.Options) *state.DB {
	t.Helper()
	db, err := state.Open(fs, opts)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMainnetGenesis(t *testing.T) {
	g, err := genesis.Mainnet()
	must(t, err)
	fs := goio.NewMemFS()
	db := openDB(t, fs, state.Options{})
	if db.Head().Loaded {
		t.Fatal("empty database reports a head")
	}
	root, err := db.LoadGenesis(g)
	must(t, err)
	if root != genesis.MainnetStateRoot {
		t.Fatalf("genesis root %s want %s", root, genesis.MainnetStateRoot)
	}
	want := state.Head{Number: 0, Root: root, Loaded: true}
	if db.Head() != want {
		t.Fatalf("head %+v want %+v", db.Head(), want)
	}
	if _, err := db.LoadGenesis(g); !errors.Is(err, state.ErrNotEmpty) {
		t.Fatalf("second genesis load: %v", err)
	}
	if _, err := db.State().Commit(0); !errors.Is(err, state.ErrBlockOrder) {
		t.Fatalf("commit at the head: %v", err)
	}
	must(t, db.Close())

	fs.Crash()
	db = openDB(t, fs, state.Options{})
	if db.Head() != want {
		t.Fatalf("reopened head %+v want %+v", db.Head(), want)
	}
	st := db.State()
	for i := range g.Alloc {
		a := &g.Alloc[i]
		acc, ok := st.Account(a.Address)
		if !ok {
			t.Fatalf("genesis account %s missing", a.Address)
		}
		if acc.Nonce != a.Nonce || acc.Balance != u256.FromBytes32(a.Balance) || acc.StorageRoot != a.StorageRoot() {
			t.Fatalf("genesis account %s: %+v", a.Address, acc)
		}
		if st.CodeSize(a.Address) != len(a.Code) {
			t.Fatalf("genesis account %s code size %d want %d", a.Address, st.CodeSize(a.Address), len(a.Code))
		}
		for _, slot := range a.Storage {
			if got := st.Storage(a.Address, slot.Key); got != u256.FromBytes32(slot.Value) {
				t.Fatalf("genesis slot %s/%s: %v", a.Address, slot.Key, got)
			}
		}
	}
	must(t, db.Close())
}
