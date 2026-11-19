package storage_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/storage"
	"go.uber.org/goleak"
)

func TestReadCursorPrivateBuffers(t *testing.T) {
	defer goleak.VerifyNone(t)
	db := open(t, goio.NewMemFS(), 1<<20)
	var b storage.Batch
	for i := 0; i < 1024; i++ {
		b.Put([]byte(fmt.Sprintf("k%04d", i)), bytes.Repeat([]byte{byte(i)}, 65+i%123))
	}
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	b.Delete([]byte("k0003"))
	b.Put([]byte("memory"), []byte("mem"))
	if err := db.Write(&b); err != nil {
		t.Fatal(err)
	}
	cursors := [4]*storage.ReadCursor{}
	for i := range cursors {
		cursors[i] = db.ReadCursor()
	}
	var group sync.WaitGroup
	for lane := range cursors {
		lane := lane
		group.Go(func() {
			cursor := cursors[lane]
			for step := 0; step < 2048; step++ {
				i := (step*37 + lane*23) % 1024
				key := []byte(fmt.Sprintf("k%04d", i))
				value, err := cursor.Get(key)
				if i == 3 {
					if !errors.Is(err, storage.ErrNotFound) {
						t.Error("tombstone", err)
					}
					continue
				}
				want := bytes.Repeat([]byte{byte(i)}, 65+i%123)
				if err != nil || !bytes.Equal(value, want) {
					t.Error("cursor read", err)
					return
				}
				retained := bytes.Clone(value)
				if _, err = cursor.Get([]byte("k0999")); err != nil {
					t.Error(err)
					return
				}
				if !bytes.Equal(retained, value) {
					t.Error("cursor aliases scratch")
					return
				}
			}
			if value, err := cursor.Get([]byte("memory")); err != nil || string(value) != "mem" {
				t.Error("memtable", err)
			}
		})
	}
	group.Wait()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
