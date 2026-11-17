package storage_test

import (
	"bytes"
	"encoding/binary"
	"sync"
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/storage"
)

func cmpKey(i int) []byte {
	k := make([]byte, 33)
	k[0] = 'n'
	binary.BigEndian.PutUint64(k[1:], uint64(i)*0x9e3779b97f4a7c15)
	binary.BigEndian.PutUint64(k[9:], uint64(i))
	return k
}

func BenchmarkCmpWrite(b *testing.B) {
	db, err := storage.Open(goio.NewMemFS(), storage.Options{MemtableBytes: 16 << 20})
	if err != nil {
		b.Fatal(err)
	}
	value := bytes.Repeat([]byte{3}, 90)
	var batch storage.Batch
	b.ReportAllocs()
	b.SetBytes(33 + 90)
	for i := range b.N {
		batch.Put(cmpKey(i%2000000), value)
		if batch.Len() == 20000 {
			if err := db.Write(&batch); err != nil {
				b.Fatal(err)
			}
			batch.Reset()
		}
	}
	if err := db.Flush(); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
}

func cmpLoaded(b *testing.B, n int) *storage.DB {
	db, err := storage.Open(goio.NewMemFS(), storage.Options{MemtableBytes: 1 << 20})
	if err != nil {
		b.Fatal(err)
	}
	var batch storage.Batch
	for i := range n {
		batch.Put(cmpKey(i), bytes.Repeat([]byte{1}, 100))
		if batch.Len() == 5000 {
			if err := db.Write(&batch); err != nil {
				b.Fatal(err)
			}
			batch.Reset()
		}
	}
	if err := db.Flush(); err != nil {
		b.Fatal(err)
	}
	return db
}

func BenchmarkCmpGetHot(b *testing.B) {
	db := cmpLoaded(b, 300000)
	b.ReportAllocs()
	b.ResetTimer()
	var wg sync.WaitGroup
	const readers = 6
	for w := range readers {
		wg.Go(func() {
			for j := range b.N/readers + 1 {
				i := (w*104729 + j*7919) % 10000
				if _, err := db.Get(cmpKey(i)); err != nil {
					b.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	b.StopTimer()
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkCmpGetMiss(b *testing.B) {
	db := cmpLoaded(b, 300000)
	b.ReportAllocs()
	b.ResetTimer()
	var wg sync.WaitGroup
	const readers = 6
	for w := range readers {
		wg.Go(func() {
			for j := range b.N/readers + 1 {
				if _, err := db.Get(cmpKey(1000000 + w*b.N + j)); err == nil {
					b.Error("absent key found")
					return
				}
			}
		})
	}
	wg.Wait()
	b.StopTimer()
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
}
