package state_test

import (
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
)

func TestScaleOnDisk(t *testing.T) {
	blocks := 2
	if v := os.Getenv("GOEMPO_STATE_SCALE_BLOCKS"); v != "" {
		n, err := strconv.Atoi(v)
		must(t, err)
		blocks = n
	}
	fs, err := goio.NewDirFS(t.TempDir())
	must(t, err)
	db := openDB(t, fs, state.Options{})
	st := db.State()
	rng := rand.New(rand.NewPCG(9, 10))
	start := time.Now()
	for n := 1; n <= blocks; n++ {
		for i := 0; i < 5000; i++ {
			st.AddBalance(benchAddr(rng.IntN(blocks*5000)), u256.FromUint64(1))
		}
		for i := 0; i < 5000; i++ {
			a := benchAddr(rng.IntN(1000))
			st.SetStorage(a, benchSlot(rng.IntN(blocks*5000)), u256.FromUint64(uint64(n)))
		}
		_, err := st.Commit(uint64(n))
		must(t, err)
		if n%50 == 0 {
			t.Logf("block %d: %v elapsed, %+v", n, time.Since(start), db.Store().Stats())
		}
	}
	must(t, db.Flush())
	t.Logf("populated %d blocks in %v", blocks, time.Since(start))
	start = time.Now()
	smallBlocks(t, db, rng, blocks)
	t.Logf("1000 small blocks over the populated state in %v", time.Since(start))
	head := db.Head()
	must(t, db.Close())
	db = openDB(t, fs, state.Options{})
	if db.Head() != head {
		t.Fatalf("reopened head %+v want %+v", db.Head(), head)
	}
	root, err := db.RebuildRoot()
	must(t, err)
	if root != head.Root {
		t.Fatalf("rebuilt root %s want %s", root, head.Root)
	}
	must(t, db.Close())
}

func smallBlocks(t *testing.T, db *state.DB, rng *rand.Rand, blocks int) {
	st := db.State()
	for n := 0; n < 1000; n++ {
		for i := 0; i < 5; i++ {
			a := benchAddr(rng.IntN(blocks * 5000))
			st.AddBalance(a, u256.FromUint64(1))
		}
		for i := 0; i < 10; i++ {
			a := benchAddr(rng.IntN(1000))
			k := benchSlot(rng.IntN(blocks * 5000))
			st.SetStorage(a, k, st.Storage(a, k).Add(u256.FromUint64(1)))
		}
		_, err := st.Commit(uint64(blocks + 1 + n))
		must(t, err)
	}
}
