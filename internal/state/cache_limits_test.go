package state_test

import (
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
)

type cacheReadFS struct {
	goio.FS
	reads int
}

type cacheReadFile struct {
	goio.File
	owner *cacheReadFS
}

func (fs *cacheReadFS) Create(name string) (goio.File, error) {
	file, err := fs.FS.Create(name)
	if err != nil {
		return nil, err
	}
	return &cacheReadFile{File: file, owner: fs}, nil
}

func (fs *cacheReadFS) Open(name string) (goio.File, error) {
	file, err := fs.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return &cacheReadFile{File: file, owner: fs}, nil
}

func (file *cacheReadFile) ReadAt(p []byte, off int64) (int, error) {
	file.owner.reads++
	return file.File.ReadAt(p, off)
}

func TestStorageCacheEvictionActuallyReloadsSavedNodes(t *testing.T) {
	modes := []struct {
		name  string
		opts  state.Options
		evict bool
	}{
		{name: "default"},
		{name: "per-trie", opts: state.Options{StorageNodes: 32}, evict: true},
		{name: "aggregate", opts: state.Options{StorageNodesTotal: 1}, evict: true},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			fs := &cacheReadFS{FS: goio.NewMemFS()}
			db := openDB(t, fs, mode.opts)
			st := db.State()
			addr := benchAddr(1)
			st.AddBalance(addr, u256.FromUint64(1))
			for i := 0; i < 4096; i++ {
				st.SetStorage(addr, benchSlot(i), u256.FromUint64(uint64(i+1)))
			}
			root, err := st.Commit(1)
			must(t, err)
			must(t, db.Flush())
			fs.reads = 0
			for i := 0; i < 4096; i++ {
				st.SetStorage(addr, benchSlot(i), u256.FromUint64(uint64(i+1)))
			}
			again, err := st.Commit(2)
			must(t, err)
			if again != root {
				t.Fatal("cache eviction changed the state root")
			}
			if mode.evict != (fs.reads > 0) {
				t.Fatalf("evict %v, actual persisted node reads %d", mode.evict, fs.reads)
			}
			t.Logf("persisted node reads after cache trim: %d", fs.reads)
			must(t, db.Close())
		})
	}
}
