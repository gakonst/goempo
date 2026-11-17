package storage

import (
	"bytes"
	"errors"
	"hash/crc32"
	"math/bits"
	"strconv"
	"strings"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const MemtableBytesDefault = 64 << 20

const segmentsMax = 64

const tierWidth = 4

const _ = uint(segmentsMax - tierWidth)

const manifestName = "MANIFEST"

const manifestTemp = "MANIFEST.tmp"

const manifestHeader = "goempo-storage v1"

type Options struct {
	MemtableBytes int
}

type DB struct {
	fs    goio.FS
	mem   *memtable
	segs  []*segment
	seq   uint64
	limit int
	stats Stats
}

type Stats struct {
	Segments     int
	SegmentBytes int64
	MemtableKeys int
	Flushes      int
	Merges       int
	BytesWritten int64
}

func Open(fs goio.FS, opts Options) (*DB, error) {
	limit := opts.MemtableBytes
	if limit <= 0 {
		limit = MemtableBytesDefault
	}
	db := &DB{fs: fs, mem: newMemtable(), limit: limit}
	man, err := readManifest(fs)
	if err != nil {
		return nil, err
	}
	db.seq = man.seq
	for _, name := range man.names {
		file, err := fs.Open(name)
		if err != nil {
			return nil, errors.Join(err, db.closeSegments())
		}
		s, err := openSegment(name, file)
		if err != nil {
			return nil, errors.Join(err, file.Close(), db.closeSegments())
		}
		db.segs = append(db.segs, s)
	}
	if err := db.removeUnlisted(man.names); err != nil {
		return nil, errors.Join(err, db.closeSegments())
	}
	return db, nil
}

type manifest struct {
	names []string
	seq   uint64
}

func readManifest(fs goio.FS) (manifest, error) {
	file, err := fs.Open(manifestName)
	if errors.Is(err, goio.ErrNotExist) {
		return manifest{}, nil
	}
	if err != nil {
		return manifest{}, err
	}
	raw := make([]byte, file.Size())
	_, err = file.ReadAt(raw, 0)
	if err = errors.Join(err, file.Close()); err != nil {
		return manifest{}, err
	}
	return parseManifest(string(raw))
}

func parseManifest(text string) (manifest, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) < 3 || lines[0] != manifestHeader {
		return manifest{}, ErrManifest
	}
	body := strings.Join(lines[:len(lines)-1], "\n")
	sum := strconv.FormatUint(uint64(crc32.Checksum([]byte(body), castagnoli)), 10)
	if lines[len(lines)-1] != "crc "+sum {
		return manifest{}, ErrManifest
	}
	seq, err := strconv.ParseUint(strings.TrimPrefix(lines[1], "seq "), 10, 64)
	if err != nil {
		return manifest{}, ErrManifest
	}
	names := lines[2 : len(lines)-1]
	if len(names) > segmentsMax {
		return manifest{}, ErrManifest
	}
	return manifest{names: names, seq: seq}, nil
}

func (db *DB) removeUnlisted(live []string) error {
	all, err := db.fs.List()
	if err != nil {
		return err
	}
	keep := map[string]bool{manifestName: true}
	for _, name := range live {
		keep[name] = true
	}
	for _, name := range all {
		if keep[name] {
			continue
		}
		if err := db.fs.Remove(name); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) writeManifest() error {
	var sb strings.Builder
	sb.WriteString(manifestHeader + "\nseq " + strconv.FormatUint(db.seq, 10))
	for _, s := range db.segs {
		sb.WriteString("\n" + s.name)
	}
	body := sb.String()
	text := body + "\ncrc " + strconv.FormatUint(uint64(crc32.Checksum([]byte(body), castagnoli)), 10) + "\n"
	file, err := db.fs.Create(manifestTemp)
	if err != nil {
		return err
	}
	_, err = file.Write([]byte(text))
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return db.fs.Rename(goio.Move{From: manifestTemp, To: manifestName})
}

func (db *DB) Get(key []byte) ([]byte, error) {
	if e, ok := db.mem.get(key); ok {
		if e.deleted {
			return nil, ErrNotFound
		}
		return e.value, nil
	}
	for i := len(db.segs) - 1; i >= 0; i-- {
		found, err := db.segs[i].get(key)
		if err != nil {
			return nil, err
		}
		if !found.found {
			continue
		}
		if found.deleted {
			return nil, ErrNotFound
		}
		return found.value, nil
	}
	return nil, ErrNotFound
}

func (db *DB) Write(b *Batch) error {
	for _, o := range b.ops {
		db.mem.apply(o)
	}
	if db.mem.bytes >= db.limit {
		return db.Flush()
	}
	return nil
}

func (db *DB) nextName() string {
	db.seq++
	return strconv.FormatUint(db.seq, 16) + ".seg"
}

func (db *DB) Flush() error {
	if len(db.mem.entries) == 0 {
		return nil
	}
	name := db.nextName()
	file, err := db.fs.Create(name)
	if err != nil {
		return err
	}
	w := newSegmentWriter(file)
	for _, key := range db.mem.sortedKeys() {
		e := db.mem.entries[key]
		w.add(record{key: []byte(key), value: e.value, deleted: e.deleted})
	}
	if err := w.finish(); err != nil {
		return errors.Join(err, file.Close())
	}
	s, err := openSegment(name, file)
	if err != nil {
		return errors.Join(err, file.Close())
	}
	db.segs = append(db.segs, s)
	if err := db.writeManifest(); err != nil {
		return err
	}
	db.stats.Flushes++
	db.stats.BytesWritten += s.size
	db.mem = newMemtable()
	return db.compact()
}

func (db *DB) sizeClass(s *segment) int {
	units := uint64(s.size) / uint64(max(db.limit/2, 1))
	return bits.Len64(units) / 2
}

func (db *DB) compact() error {
	for round := 0; round < segmentsMax; round++ {
		n := len(db.segs)
		if n < tierWidth {
			return nil
		}
		tail := db.segs[n-tierWidth:]
		class := db.sizeClass(tail[0])
		same := true
		for _, s := range tail[1:] {
			same = same && db.sizeClass(s) == class
		}
		if !same && n < segmentsMax {
			return nil
		}
		if err := db.merge(n - tierWidth); err != nil {
			return err
		}
	}
	assert.Fail("storage compaction did not settle")
	return nil
}

func (db *DB) merge(from int) error {
	inputs := db.segs[from:]
	dropTombstones := from == 0
	name := db.nextName()
	file, err := db.fs.Create(name)
	if err != nil {
		return err
	}
	w := newSegmentWriter(file)
	m, err := newMerger(mergeInput{segs: inputs})
	if err == nil {
		err = m.each(func(r record) error {
			if r.deleted && dropTombstones {
				return nil
			}
			w.add(r)
			return nil
		})
	}
	if err == nil {
		err = w.finish()
	}
	if err != nil {
		return errors.Join(err, file.Close())
	}
	merged, err := openSegment(name, file)
	if err != nil {
		return errors.Join(err, file.Close())
	}
	old := append([]*segment(nil), inputs...)
	db.segs = append(db.segs[:from], merged)
	if err := db.writeManifest(); err != nil {
		return err
	}
	db.stats.Merges++
	db.stats.BytesWritten += merged.size
	for _, s := range old {
		if err := errors.Join(s.file.Close(), db.fs.Remove(s.name)); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Iterate(prefix []byte, visit func(Entry) error) error {
	m, err := newMerger(mergeInput{segs: db.segs, mem: db.mem, prefix: prefix})
	if err != nil {
		return err
	}
	return m.each(func(r record) error {
		if !bytes.HasPrefix(r.key, prefix) {
			return errStop
		}
		if r.deleted {
			return nil
		}
		return visit(Entry{Key: r.key, Value: r.value})
	})
}

func (db *DB) Stats() Stats {
	s := db.stats
	s.Segments = len(db.segs)
	s.MemtableKeys = len(db.mem.entries)
	for _, seg := range db.segs {
		s.SegmentBytes += seg.size
	}
	return s
}

func (db *DB) closeSegments() error {
	var err error
	for _, s := range db.segs {
		err = errors.Join(err, s.file.Close())
	}
	db.segs = nil
	return err
}

func (db *DB) Close() error {
	return errors.Join(db.Flush(), db.closeSegments())
}
