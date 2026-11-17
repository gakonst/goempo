package state_test

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	benchAccounts  = 100_000
	benchContracts = 1_000
	benchSlots     = 200
)

func benchAddr(i int) primitives.Address {
	var a primitives.Address
	binary.BigEndian.PutUint64(a[12:], uint64(i)*0x9e3779b97f4a7c15)
	return a
}

func benchSlot(i int) primitives.Hash {
	var h primitives.Hash
	binary.BigEndian.PutUint64(h[24:], uint64(i))
	return h
}

func populate(b *testing.B, fs goio.FS) {
	db := openDB(b, fs, state.Options{})
	st := db.State()
	for i := 0; i < benchAccounts; i++ {
		st.AddBalance(benchAddr(i), u256.FromUint64(uint64(i)+1))
		if i < benchContracts {
			st.SetCode(benchAddr(i), []byte{0x60, byte(i)})
			for j := 0; j < benchSlots; j++ {
				st.SetStorage(benchAddr(i), benchSlot(j), u256.FromUint64(uint64(j)+1))
			}
		}
	}
	_, err := st.Commit(1)
	must(b, err)
	must(b, db.Close())
}

func BenchmarkCommitBlock(b *testing.B) {
	fs := goio.NewMemFS()
	populate(b, fs)
	db := openDB(b, fs, state.Options{})
	st := db.State()
	rng := rand.New(rand.NewPCG(1, 2))
	number := uint64(1)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i < 200; i++ {
			a := benchAddr(rng.IntN(benchAccounts))
			st.AddBalance(a, u256.FromUint64(1))
			st.SetNonce(a, st.Nonce(a)+1)
		}
		for i := 0; i < 600; i++ {
			a := benchAddr(rng.IntN(benchContracts))
			k := benchSlot(rng.IntN(benchSlots * 2))
			st.SetStorage(a, k, st.Storage(a, k).Add(u256.FromUint64(1)))
		}
		st.FinalizeTx()
		number++
		_, err := st.Commit(number)
		must(b, err)
	}
	must(b, db.Close())
}

func BenchmarkColdSlotRead(b *testing.B) {
	fs := goio.NewMemFS()
	populate(b, fs)
	db := openDB(b, fs, state.Options{CacheSlots: 1, CacheAccounts: 1})
	st := db.State()
	rng := rand.New(rand.NewPCG(3, 4))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		a := benchAddr(rng.IntN(benchContracts))
		st.Storage(a, benchSlot(rng.IntN(benchSlots)))
		st.Discard()
	}
	must(b, db.Close())
}

func BenchmarkColdAccountRead(b *testing.B) {
	fs := goio.NewMemFS()
	populate(b, fs)
	db := openDB(b, fs, state.Options{CacheSlots: 1, CacheAccounts: 1})
	st := db.State()
	rng := rand.New(rand.NewPCG(5, 6))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		st.Balance(benchAddr(rng.IntN(benchAccounts)))
		st.Discard()
	}
	must(b, db.Close())
}

func BenchmarkCommitEmptyBlock(b *testing.B) {
	fs := goio.NewMemFS()
	populate(b, fs)
	db := openDB(b, fs, state.Options{})
	st := db.State()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		_, err := st.Commit(uint64(n) + 2)
		must(b, err)
	}
	must(b, db.Close())
}

func BenchmarkCommitSmallBlock(b *testing.B) {
	fs := goio.NewMemFS()
	populate(b, fs)
	db := openDB(b, fs, state.Options{})
	st := db.State()
	rng := rand.New(rand.NewPCG(7, 8))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i < 3; i++ {
			a := benchAddr(rng.IntN(benchAccounts))
			st.AddBalance(a, u256.FromUint64(1))
			st.SetNonce(a, st.Nonce(a)+1)
		}
		for i := 0; i < 10; i++ {
			a := benchAddr(rng.IntN(benchContracts / 50))
			k := benchSlot(rng.IntN(benchSlots * 2))
			st.SetStorage(a, k, st.Storage(a, k).Add(u256.FromUint64(1)))
		}
		st.FinalizeTx()
		_, err := st.Commit(uint64(n) + 2)
		must(b, err)
	}
	must(b, db.Close())
}
