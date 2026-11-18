package storage_test

import (
	"bytes"
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/storage"
)

func open(t *testing.T, fs goio.FS, memBytes int) *storage.DB {
	t.Helper()
	db, err := storage.Open(fs, storage.Options{MemtableBytes: memBytes})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func checkModel(t *testing.T, db *storage.DB, model map[string]string) {
	t.Helper()
	for _, k := range slices.Sorted(maps.Keys(model)) {
		got, err := db.Get([]byte(k))
		if err != nil {
			t.Fatalf("get %q: %v", k, err)
		}
		if string(got) != model[k] {
			t.Fatalf("get %q: %q want %q", k, got, model[k])
		}
	}
	var seen []string
	err := db.Iterate(nil, func(e storage.Entry) error {
		seen = append(seen, string(e.Key))
		if model[string(e.Key)] != string(e.Value) {
			t.Fatalf("iterate %q value %q", e.Key, e.Value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, slices.Sorted(maps.Keys(model))) {
		t.Fatalf("iterate saw %d keys, model has %d", len(seen), len(model))
	}
}

func TestRandomAgainstModel(t *testing.T) {
	for seed := uint64(0); seed < 8; seed++ {
		r := rand.New(rand.NewPCG(seed, 5))
		fs := goio.NewMemFS()
		db := open(t, fs, 4096)
		model := map[string]string{}
		for step := 0; step < 3000; step++ {
			var b storage.Batch
			for range 1 + r.IntN(8) {
				k := "k" + strconv.Itoa(r.IntN(600))
				if r.IntN(4) == 0 {
					b.Delete([]byte(k))
					delete(model, k)
					continue
				}
				v := bytes.Repeat([]byte{byte(r.Uint32())}, r.IntN(90))
				b.Put([]byte(k), v)
				model[k] = string(v)
			}
			if err := db.Write(&b); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Get([]byte("absent")); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("absent key found")
			}
			if step%500 == 499 {
				checkModel(t, db, model)
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				db = open(t, fs, 4096)
			}
		}
		checkModel(t, db, model)
		st := db.Stats()
		if st.Segments > 12 {
			t.Fatalf("seed %d: stats %+v", seed, st)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCrashLosesOnlyUnflushed(t *testing.T) {
	fs := goio.NewMemFS()
	db := open(t, fs, 1<<20)
	var b storage.Batch
	b.Put([]byte("durable"), []byte("1"))
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	b.Put([]byte("volatile"), []byte("2"))
	b.Delete([]byte("durable"))
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	fs.Crash()
	crashed := db
	db = open(t, fs, 1<<20)
	got, err := db.Get([]byte("durable"))
	if err != nil || string(got) != "1" {
		t.Fatalf("durable: %q %v", got, err)
	}
	if _, err := db.Get([]byte("volatile")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unflushed write survived a crash")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := crashed.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOrphanFilesRemovedAndCorruptionDetected(t *testing.T) {
	fs := goio.NewMemFS()
	db := open(t, fs, 1<<20)
	var b storage.Batch
	for i := range 200 {
		b.Put([]byte("key"+strconv.Itoa(i)), bytes.Repeat([]byte{7}, 50))
	}
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	orphan, err := fs.Create("ff.seg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orphan.Write([]byte("junk")); err != nil {
		t.Fatal(err)
	}
	db = open(t, fs, 1<<20)
	names, err := fs.List()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "ff.seg") {
		t.Fatal("orphan segment kept")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	seg, err := fs.Open("1.seg")
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, seg.Size())
	if _, err := seg.ReadAt(raw, 0); err != nil {
		t.Fatal(err)
	}
	raw[10] ^= 0xff
	bad, err := fs.Create("1.seg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Write(raw); err != nil {
		t.Fatal(err)
	}
	db = open(t, fs, 1<<20)
	failed := 0
	for i := range 200 {
		if _, err := db.Get([]byte("key" + strconv.Itoa(i))); errors.Is(err, storage.ErrCorrupt) {
			failed++
		}
	}
	if failed == 0 {
		t.Fatal("flipped byte not detected")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestIteratePrefixAndDirFS(t *testing.T) {
	fs, err := goio.NewDirFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := open(t, fs, 2048)
	for i := range 500 {
		var b storage.Batch
		b.Put([]byte("a/"+strconv.Itoa(i)), []byte("x"))
		b.Put([]byte("b/"+strconv.Itoa(i)), []byte("y"))
		if i%3 == 0 {
			b.Delete([]byte("a/" + strconv.Itoa(i/2)))
		}
		if err := db.Write(&b); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open(t, fs, 2048)
	count := 0
	err = db.Iterate([]byte("b/"), func(e storage.Entry) error {
		if !bytes.HasPrefix(e.Key, []byte("b/")) || string(e.Value) != "y" {
			t.Fatalf("entry %q=%q", e.Key, e.Value)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 500 {
		t.Fatalf("prefix b/ has %d keys", count)
	}
	stop := errors.New("stop")
	err = db.Iterate([]byte("a/"), func(storage.Entry) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("visit error not returned: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkGetFromSegments(b *testing.B) {
	db, err := storage.Open(goio.NewMemFS(), storage.Options{MemtableBytes: 1 << 20})
	if err != nil {
		b.Fatal(err)
	}
	const n = 200000
	var batch storage.Batch
	key := make([]byte, 32)
	for i := range n {
		key[0], key[1], key[2] = byte(i>>16), byte(i>>8), byte(i)
		batch.Put(key, bytes.Repeat([]byte{1}, 100))
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
	b.Cleanup(func() {
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := i % n
		key[0], key[1], key[2] = byte(j>>16), byte(j>>8), byte(j)
		if _, err := db.Get(key); err != nil {
			b.Fatal(err)
		}
	}
}
