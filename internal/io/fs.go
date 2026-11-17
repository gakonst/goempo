package io

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
)

var (
	ErrNotExist  = errors.New("io: file does not exist")
	ErrClosed    = errors.New("io: file is closed")
	ErrShortRead = errors.New("io: read past the end of the file")
)

type File interface {
	ReadAt(p []byte, off int64) (int, error)
	Write(p []byte) (int, error)
	Sync() error
	Size() int64
	Close() error
}

type FS interface {
	Create(name string) (File, error)
	Open(name string) (File, error)
	Rename(m Move) error
	Remove(name string) error
	List() ([]string, error)
}

type Move struct {
	From string
	To   string
}

type DirFS struct {
	dir string
}

func NewDirFS(dir string) (*DirFS, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DirFS{dir: dir}, nil
}

type osFile struct {
	f    *os.File
	size int64
}

func (fs *DirFS) path(name string) string {
	return filepath.Join(fs.dir, filepath.Base(name))
}

func (fs *DirFS) Create(name string) (File, error) {
	f, err := os.OpenFile(fs.path(name), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &osFile{f: f}, nil
}

func (fs *DirFS) Open(name string) (File, error) {
	f, err := os.Open(fs.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return &osFile{f: f, size: info.Size()}, nil
}

func (fs *DirFS) Rename(m Move) error {
	if err := os.Rename(fs.path(m.From), fs.path(m.To)); err != nil {
		return err
	}
	d, err := os.Open(fs.dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func (fs *DirFS) Remove(name string) error {
	err := os.Remove(fs.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotExist
	}
	return err
}

func (fs *DirFS) List() ([]string, error) {
	entries, err := os.ReadDir(fs.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (f *osFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.f.ReadAt(p, off)
	if n == len(p) {
		return n, nil
	}
	if err == nil {
		err = ErrShortRead
	}
	return n, err
}

func (f *osFile) Write(p []byte) (int, error) {
	n, err := f.f.Write(p)
	f.size += int64(n)
	return n, err
}

func (f *osFile) Sync() error { return f.f.Sync() }

func (f *osFile) Size() int64 { return f.size }

func (f *osFile) Close() error { return f.f.Close() }

type MemFS struct {
	mu    sync.Mutex
	files map[string]*memData
}

type memData struct {
	data   []byte
	synced int
}

type memFile struct {
	d      *memData
	closed bool
}

func NewMemFS() *MemFS {
	return &MemFS{files: map[string]*memData{}}
}

func (fs *MemFS) Create(name string) (File, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	d := &memData{}
	fs.files[name] = d
	return &memFile{d: d}, nil
}

func (fs *MemFS) Open(name string) (File, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	d, ok := fs.files[name]
	if !ok {
		return nil, ErrNotExist
	}
	return &memFile{d: d}, nil
}

func (fs *MemFS) Rename(m Move) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	d, ok := fs.files[m.From]
	if !ok {
		return ErrNotExist
	}
	delete(fs.files, m.From)
	fs.files[m.To] = d
	return nil
}

func (fs *MemFS) Remove(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.files[name]; !ok {
		return ErrNotExist
	}
	delete(fs.files, name)
	return nil
}

func (fs *MemFS) List() ([]string, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	names := make([]string, 0, len(fs.files))
	for name := range fs.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (fs *MemFS) Crash() {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, name := range slices.Sorted(maps.Keys(fs.files)) {
		d := fs.files[name]
		d.data = d.data[:d.synced]
	}
}

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	if f.closed {
		return 0, ErrClosed
	}
	if off < 0 || off > int64(len(f.d.data)) {
		return 0, ErrShortRead
	}
	n := copy(p, f.d.data[off:])
	if n < len(p) {
		return n, ErrShortRead
	}
	return n, nil
}

func (f *memFile) Write(p []byte) (int, error) {
	if f.closed {
		return 0, ErrClosed
	}
	f.d.data = append(f.d.data, p...)
	return len(p), nil
}

func (f *memFile) Sync() error {
	if f.closed {
		return ErrClosed
	}
	f.d.synced = len(f.d.data)
	return nil
}

func (f *memFile) Size() int64 { return int64(len(f.d.data)) }

func (f *memFile) Close() error {
	f.closed = true
	return nil
}
