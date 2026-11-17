package storage

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"sort"
	"sync"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const blockRecordsMax = 64

// blockBufs holds block read buffers, so gets are safe from several
// goroutines at once (state commits read storage tries in parallel).
var blockBufs = sync.Pool{New: func() any { b := make([]byte, 0, 4096); return &b }}

const blockBytesTarget = 4096

const footerBytes = 7 * 8

const segmentMagic = 0x676f656d706f5331

const recordsMax = 1 << 40

const _ = uint(math.MaxInt64/(segmentsMax+1) - recordsMax)

const _ = uint(blockBytesTarget - blockRecordsMax)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type record struct {
	key     []byte
	value   []byte
	deleted bool
}

type indexEntry struct {
	firstKey []byte
	offset   int64
	length   int
	crc      uint32
	records  int
}

type segment struct {
	name    string
	file    goio.File
	size    int64
	records int
	index   []indexEntry
	filter  bloom
}

type segmentWriter struct {
	file    goio.File
	offset  int64
	block   []byte
	first   []byte
	inBlock int
	records int
	index   []indexEntry
	keys    []uint64
	err     error
}

func newSegmentWriter(file goio.File) *segmentWriter {
	return &segmentWriter{file: file, block: make([]byte, 0, 2*blockBytesTarget)}
}

func appendRecord(dst []byte, r record) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(r.key)))
	if r.deleted {
		dst = binary.AppendUvarint(dst, 0)
	} else {
		dst = binary.AppendUvarint(dst, uint64(len(r.value))+1)
	}
	dst = append(dst, r.key...)
	return append(dst, r.value...)
}

func (w *segmentWriter) add(r record) {
	if w.inBlock == 0 {
		w.first = append(w.first[:0], r.key...)
	}
	w.block = appendRecord(w.block, r)
	w.inBlock++
	w.records++
	w.keys = append(w.keys, keyHash(r.key))
	if w.inBlock == blockRecordsMax || len(w.block) >= blockBytesTarget {
		w.flushBlock()
	}
}

func (w *segmentWriter) write(p []byte) {
	if w.err != nil {
		return
	}
	n, err := w.file.Write(p)
	w.offset += int64(n)
	w.err = err
}

func (w *segmentWriter) flushBlock() {
	if w.inBlock == 0 {
		return
	}
	w.index = append(w.index, indexEntry{
		firstKey: append([]byte(nil), w.first...),
		offset:   w.offset,
		length:   len(w.block),
		crc:      crc32.Checksum(w.block, castagnoli),
		records:  min(w.inBlock, blockRecordsMax),
	})
	w.write(w.block)
	w.block = w.block[:0]
	w.inBlock = 0
}

func (w *segmentWriter) finish() error {
	w.flushBlock()
	indexOffset := w.offset
	var meta []byte
	meta = binary.AppendUvarint(meta, uint64(len(w.index)))
	for _, e := range w.index {
		meta = binary.AppendUvarint(meta, uint64(len(e.firstKey)))
		meta = append(meta, e.firstKey...)
		meta = binary.AppendUvarint(meta, uint64(e.offset))
		meta = binary.AppendUvarint(meta, uint64(e.length))
		meta = binary.AppendUvarint(meta, uint64(e.records))
		meta = binary.LittleEndian.AppendUint32(meta, e.crc)
	}
	indexLength := len(meta)
	filter := newBloom(w.records)
	for _, h := range w.keys {
		filter.addHash(h)
	}
	for _, word := range filter.words {
		meta = binary.LittleEndian.AppendUint64(meta, word)
	}
	footer := []uint64{
		uint64(indexOffset), uint64(indexLength), uint64(len(filter.words)),
		uint64(w.records), uint64(crc32.Checksum(meta, castagnoli)), 0, segmentMagic,
	}
	for _, v := range footer {
		meta = binary.LittleEndian.AppendUint64(meta, v)
	}
	w.write(meta)
	if w.err != nil {
		return w.err
	}
	return w.file.Sync()
}

func openSegment(name string, file goio.File) (*segment, error) {
	size := file.Size()
	if size < footerBytes {
		return nil, ErrCorrupt
	}
	var footer [footerBytes]byte
	if _, err := file.ReadAt(footer[:], size-footerBytes); err != nil {
		return nil, err
	}
	f := make([]uint64, footerBytes/8)
	for i := range f {
		f[i] = binary.LittleEndian.Uint64(footer[8*i:])
	}
	if f[6] != segmentMagic {
		return nil, ErrCorrupt
	}
	indexOffset, indexLength, bloomWords := int64(f[0]), int64(f[1]), int64(f[2])
	metaLength := indexLength + 8*bloomWords
	if indexOffset < 0 || metaLength < 0 || indexOffset+metaLength+footerBytes != size {
		return nil, ErrCorrupt
	}
	meta := make([]byte, metaLength)
	if _, err := file.ReadAt(meta, indexOffset); err != nil {
		return nil, err
	}
	if uint64(crc32.Checksum(meta, castagnoli)) != f[4] {
		return nil, ErrCorrupt
	}
	index, err := parseIndex(meta[:indexLength], indexOffset)
	if err != nil {
		return nil, err
	}
	s := &segment{name: name, file: file, size: size, records: int(min(f[3], recordsMax)), index: index}
	s.filter = bloom{words: make([]uint64, bloomWords)}
	for i := range s.filter.words {
		s.filter.words[i] = binary.LittleEndian.Uint64(meta[indexLength+8*int64(i):])
	}
	return s, nil
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = ErrCorrupt
		r.b = nil
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) take(n uint64) []byte {
	if n > uint64(len(r.b)) {
		r.err = ErrCorrupt
		r.b = nil
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func parseIndex(b []byte, dataEnd int64) ([]indexEntry, error) {
	r := reader{b: b}
	count := min(r.uvarint(), recordsMax)
	if count > uint64(len(b)) {
		return nil, ErrCorrupt
	}
	index := make([]indexEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		var e indexEntry
		e.firstKey = append([]byte(nil), r.take(r.uvarint())...)
		e.offset = int64(r.uvarint())
		e.length = int(min(r.uvarint(), uint64(dataEnd)))
		e.records = int(min(r.uvarint(), blockRecordsMax+1))
		crc := r.take(4)
		if r.err != nil {
			return nil, r.err
		}
		e.crc = binary.LittleEndian.Uint32(crc)
		if e.offset < 0 || e.offset+int64(e.length) > dataEnd || e.records > blockRecordsMax {
			return nil, ErrCorrupt
		}
		index = append(index, e)
	}
	if len(r.b) != 0 {
		return nil, ErrCorrupt
	}
	return index, nil
}

func (s *segment) readBlock(i int, buf []byte) ([]byte, error) {
	e := s.index[i]
	if cap(buf) < e.length {
		buf = make([]byte, e.length)
	}
	buf = buf[:e.length]
	if _, err := s.file.ReadAt(buf, e.offset); err != nil {
		return nil, err
	}
	if crc32.Checksum(buf, castagnoli) != e.crc {
		return nil, ErrCorrupt
	}
	return buf, nil
}

func nextRecord(r *reader) record {
	klen := r.uvarint()
	tag := r.uvarint()
	key := r.take(klen)
	if tag == 0 {
		return record{key: key, deleted: true}
	}
	return record{key: key, value: r.take(tag - 1)}
}

func (s *segment) blockFor(key []byte) int {
	return sort.Search(len(s.index), func(i int) bool {
		return bytes.Compare(s.index[i].firstKey, key) > 0
	}) - 1
}

type lookup struct {
	value   []byte
	found   bool
	deleted bool
}

func (s *segment) get(key []byte) (lookup, error) {
	if !s.filter.mayContain(key) {
		return lookup{}, nil
	}
	i := s.blockFor(key)
	if i < 0 {
		return lookup{}, nil
	}
	bp := blockBufs.Get().(*[]byte)
	defer blockBufs.Put(bp)
	block, err := s.readBlock(i, *bp)
	if err != nil {
		return lookup{}, err
	}
	*bp = block
	r := reader{b: block}
	for range s.index[i].records {
		rec := nextRecord(&r)
		if r.err != nil {
			return lookup{}, r.err
		}
		c := bytes.Compare(rec.key, key)
		if c == 0 {
			// The block buffer goes back to the pool, so the value is copied out.
			return lookup{value: append([]byte(nil), rec.value...), found: true, deleted: rec.deleted}, nil
		}
		if c > 0 {
			return lookup{}, nil
		}
	}
	return lookup{}, nil
}

type segmentIterator struct {
	s       *segment
	blockAt int
	inBlock int
	r       reader
	buf     []byte
	current record
	done    bool
}

func (s *segment) iterator(prefix []byte) (*segmentIterator, error) {
	it := &segmentIterator{s: s, blockAt: max(s.blockFor(prefix), 0) - 1}
	if err := it.loadNextBlock(); err != nil {
		return nil, err
	}
	steps := 0
	for ; steps < blockRecordsMax+1 && !it.done && bytes.Compare(it.current.key, prefix) < 0; steps++ {
		if err := it.next(); err != nil {
			return nil, err
		}
	}
	assert.Ok(steps <= blockRecordsMax, "segment seek crossed more than one block")
	return it, nil
}

func (it *segmentIterator) loadNextBlock() error {
	it.blockAt++
	if it.blockAt >= len(it.s.index) {
		it.done = true
		return nil
	}
	block, err := it.s.readBlock(it.blockAt, it.buf)
	if err != nil {
		return err
	}
	it.buf = block
	it.r = reader{b: block}
	it.inBlock = 0
	return it.advance()
}

func (it *segmentIterator) advance() error {
	it.current = nextRecord(&it.r)
	it.inBlock++
	return it.r.err
}

func (it *segmentIterator) valid() bool { return !it.done }

func (it *segmentIterator) record() record { return it.current }

func (it *segmentIterator) next() error {
	if it.inBlock < it.s.index[it.blockAt].records {
		return it.advance()
	}
	return it.loadNextBlock()
}
