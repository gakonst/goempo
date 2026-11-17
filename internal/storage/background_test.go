package storage_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"
	"sync"
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/storage"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func keyOf(i int) []byte {
	k := make([]byte, 9)
	k[0] = 's'
	binary.BigEndian.PutUint64(k[1:], uint64(i)*0x9e3779b97f4a7c15)
	return k
}

type generation int

func valueOf(i int, gen generation) []byte {
	return bytes.Repeat([]byte{byte(i), byte(gen)}, 1+i%40)
}

func TestBackgroundFlushAndParallelMergeWithConcurrentReads(t *testing.T) {
	fs := goio.NewMemFS()
	db, err := storage.Open(fs, storage.Options{MemtableBytes: 16 << 10, MergeWorkers: 4, BlockCacheBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	const keys = 4000
	gen := make([]int, keys)
	deleted := make([]bool, keys)
	for round := 0; round < 40; round++ {
		var b storage.Batch
		for j := 0; j < 400; j++ {
			i := (round*397 + j*31) % keys
			if (round+j)%11 == 0 {
				b.Delete(keyOf(i))
				deleted[i] = true
				continue
			}
			gen[i] = round + 1
			deleted[i] = false
			b.Put(keyOf(i), valueOf(i, generation(gen[i])))
		}
		if err := db.Write(&b); err != nil {
			t.Fatal(err)
		}
		if b.Len() != 0 {
			t.Fatal("write did not consume the batch")
		}
		var wg sync.WaitGroup
		errs := make([]error, 6)
		for w := range errs {
			wg.Go(func() {
				for j := range keys/len(errs) + 1 {
					i := w + j*len(errs)
					if i >= keys {
						return
					}
					got, err := db.Get(keyOf(i))
					switch {
					case gen[i] == 0 || deleted[i]:
						if !errors.Is(err, storage.ErrNotFound) {
							errs[w] = errors.New("key " + strconv.Itoa(i) + " should be absent")
							return
						}
					case err != nil || !bytes.Equal(got, valueOf(i, generation(gen[i]))):
						errs[w] = errors.New("key " + strconv.Itoa(i) + " has the wrong value")
						return
					}
				}
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	live := 0
	err = db.Iterate([]byte("s"), func(e storage.Entry) error {
		live++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for i := range keys {
		if gen[i] > 0 && !deleted[i] {
			want++
		}
	}
	if live != want {
		t.Fatalf("iterate saw %d live keys, want %d", live, want)
	}
	st := db.Stats()
	if st.Merges == 0 || st.Flushes == 0 {
		t.Fatalf("no background work: %+v", st)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(fs, storage.Options{MemtableBytes: 16 << 10})
	if err != nil {
		t.Fatal(err)
	}
	for i := range keys {
		got, err := db.Get(keyOf(i))
		if gen[i] == 0 || deleted[i] {
			if !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("reopened key %d should be absent", i)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, valueOf(i, generation(gen[i]))) {
			t.Fatalf("reopened key %d: %v", i, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemtableValuesSurviveLaterWrites(t *testing.T) {
	db := open(t, goio.NewMemFS(), 1<<20)
	var b storage.Batch
	b.Put([]byte("a"), []byte("first"))
	b.Put([]byte("b"), []byte("second"))
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	got, err := db.Get([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	_ = append(got, "XXXXXX"...)
	b.Put([]byte("a"), []byte("third"))
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("an earlier value changed to %q", got)
	}
	if v, err := db.Get([]byte("b")); err != nil || string(v) != "second" {
		t.Fatalf("append through a returned value clobbered a neighbour: %q %v", v, err)
	}
	if v, err := db.Get([]byte("a")); err != nil || string(v) != "third" {
		t.Fatalf("overwrite: %q %v", v, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(&b); !errors.Is(err, storage.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestBatchAppendMovesOperations(t *testing.T) {
	db := open(t, goio.NewMemFS(), 1<<20)
	var a, other storage.Batch
	other.Put([]byte("x"), []byte("1"))
	a.Append(&other)
	other.Put([]byte("y"), []byte("2"))
	if a.Len() != 1 || other.Len() != 1 {
		t.Fatalf("lens %d %d", a.Len(), other.Len())
	}
	if err := db.Write(&a); err != nil {
		t.Fatal(err)
	}
	if v, err := db.Get([]byte("x")); err != nil || string(v) != "1" {
		t.Fatalf("x: %q %v", v, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkWriteBatches(b *testing.B) {
	db, err := storage.Open(goio.NewMemFS(), storage.Options{MemtableBytes: 8 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	})
	key := make([]byte, 54)
	value := bytes.Repeat([]byte{3}, 90)
	var batch storage.Batch
	b.ReportAllocs()
	b.SetBytes(int64(len(key) + len(value)))
	for i := range b.N {
		binary.BigEndian.PutUint64(key[1:], uint64(i%300000)*0x9e3779b97f4a7c15)
		batch.Put(key, value)
		if batch.Len() == 1000 {
			if err := db.Write(&batch); err != nil {
				b.Fatal(err)
			}
			batch.Reset()
		}
	}
}

func BenchmarkGetParallel(b *testing.B) {
	db, err := storage.Open(goio.NewMemFS(), storage.Options{MemtableBytes: 1 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	})
	const n = 200000
	var batch storage.Batch
	for i := range n {
		batch.Put(keyOf(i), bytes.Repeat([]byte{1}, 100))
		if batch.Len() == 1000 {
			if err := db.Write(&batch); err != nil {
				b.Fatal(err)
			}
			batch.Reset()
		}
	}
	if err := db.Flush(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	var wg sync.WaitGroup
	const readers = 6
	for w := range readers {
		wg.Go(func() {
			for j := range b.N/readers + 1 {
				i := (w*104729 + j*7919) % 20000
				if _, err := db.Get(keyOf(i)); err != nil {
					b.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
