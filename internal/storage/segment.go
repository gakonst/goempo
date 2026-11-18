package storage

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math"
	"sort"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const blockRecordsMax = 64

const blockBytesTarget = 4096

const footerBytes = 7 * 8

const segmentMagic = 0x676f656d706f5331

const recordsMax = 1 << 40

const writeBufferBytes = 1 << 20

const keySlabBytes = 64 << 10

const readAheadBytes = 256 << 10

const _ = uint(math.MaxInt64/(segmentsMax+1) - recordsMax)

const _ = uint(blockBytesTarget - blockRecordsMax)

const _ = uint(readAheadBytes - blockBytesTarget)

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
	id      uint64
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
	out     []byte
	block   []byte
	first   []byte
	slab    []byte
	inBlock int
	records int
	index   []indexEntry
	hashes  []uint64
	err     error
}

func newSegmentWriter(file goio.File, records int) *segmentWriter {
	return &segmentWriter{
		file:   file,
		out:    make([]byte, 0, writeBufferBytes+2*blockBytesTarget),
		block:  make([]byte, 0, 2*blockBytesTarget),
		hashes: make([]uint64, 0, records),
		index:  make([]indexEntry, 0, records/blockRecordsMax+1),
	}
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
	w.hashes = append(w.hashes, hashKey(r.key))
	if w.inBlock == blockRecordsMax || len(w.block) >= blockBytesTarget {
		w.flushBlock()
	}
}

func (w *segmentWriter) write(p []byte) {
	w.out = append(w.out, p...)
	w.offset += int64(len(p))
	if len(w.out) >= writeBufferBytes {
		w.drain()
	}
}

func (w *segmentWriter) drain() {
	if w.err != nil || len(w.out) == 0 {
		w.out = w.out[:0]
		return
	}
	_, w.err = w.file.Write(w.out)
	w.out = w.out[:0]
}

func (w *segmentWriter) keepKey(k []byte) []byte {
	if len(k) > cap(w.slab)-len(w.slab) {
		w.slab = make([]byte, 0, max(keySlabBytes, len(k)))
	}
	off := len(w.slab)
	w.slab = append(w.slab, k...)
	return w.slab[off:len(w.slab):len(w.slab)]
}

func (w *segmentWriter) flushBlock() {
	if w.inBlock == 0 {
		return
	}
	w.index = append(w.index, indexEntry{
		firstKey: w.keepKey(w.first),
		offset:   w.offset,
		length:   len(w.block),
		crc:      crc32.Checksum(w.block, castagnoli),
		records:  min(w.inBlock, blockRecordsMax),
	})
	w.write(w.block)
	w.block = w.block[:0]
	w.inBlock = 0
}

func appendIndex(meta []byte, index []indexEntry) []byte {
	meta = binary.AppendUvarint(meta, uint64(len(index)))
	for _, e := range index {
		meta = binary.AppendUvarint(meta, uint64(len(e.firstKey)))
		meta = append(meta, e.firstKey...)
		meta = binary.AppendUvarint(meta, uint64(e.offset))
		meta = binary.AppendUvarint(meta, uint64(e.length))
		meta = binary.AppendUvarint(meta, uint64(e.records))
		meta = binary.LittleEndian.AppendUint32(meta, e.crc)
	}
	return meta
}

func (w *segmentWriter) finish(name string, id uint64) (*segment, error) {
	w.flushBlock()
	indexOffset := w.offset
	meta := appendIndex(nil, w.index)
	indexLength := len(meta)
	filter := newBloom(w.records)
	for _, h := range w.hashes {
		filter.addHash(h)
	}
	for _, word := range filter.words {
		meta = binary.LittleEndian.AppendUint64(meta, word)
	}
	footer := [footerBytes / 8]uint64{
		uint64(indexOffset), uint64(indexLength), uint64(len(filter.words)),
		uint64(w.records), uint64(crc32.Checksum(meta, castagnoli)), filter.version, segmentMagic,
	}
	for _, v := range footer {
		meta = binary.LittleEndian.AppendUint64(meta, v)
	}
	w.write(meta)
	w.drain()
	if w.err != nil {
		return nil, w.err
	}
	if err := w.file.Sync(); err != nil {
		return nil, err
	}
	assert.Equal(w.file.Size(), w.offset, "segment size matches what was written")
	return &segment{
		id: id, name: name, file: w.file, size: w.offset,
		records: w.records, index: w.index, filter: filter,
	}, nil
}

func openSegment(name string, id uint64, file goio.File) (*segment, error) {
	size := file.Size()
	if size < footerBytes {
		return nil, ErrCorrupt
	}
	var footer [footerBytes]byte
	if _, err := file.ReadAt(footer[:], size-footerBytes); err != nil {
		return nil, err
	}
	var f [footerBytes / 8]uint64
	for i := range f {
		f[i] = binary.LittleEndian.Uint64(footer[8*i:])
	}
	if f[6] != segmentMagic {
		return nil, ErrCorrupt
	}
	if f[5] > bloomBlocked {
		return nil, ErrCorrupt
	}
	indexOffset, indexLength, bloomWords := int64(f[0]), int64(f[1]), int64(f[2])
	metaLength := indexLength + 8*bloomWords
	if indexOffset < 0 || metaLength < 0 || indexOffset+metaLength+footerBytes != size {
		return nil, ErrCorrupt
	}
	if f[5] == bloomBlocked {
		if bloomWords == 0 || bloomWords%bloomBlockWords != 0 {
			return nil, ErrCorrupt
		}
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
	s := &segment{id: id, name: name, file: file, size: size, records: int(min(f[3], recordsMax)), index: index}
	s.filter = bloom{words: make([]uint64, bloomWords), version: f[5]}
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
	slab := append([]byte(nil), b...)
	r := reader{b: slab}
	count := min(r.uvarint(), recordsMax)
	if count > uint64(len(b)) {
		return nil, ErrCorrupt
	}
	index := make([]indexEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		var e indexEntry
		k := r.take(r.uvarint())
		e.firstKey = k[:len(k):len(k)]
		e.offset = int64(r.uvarint())
		e.length = int(min(r.uvarint(), uint64(dataEnd)))
		e.records = int(min(r.uvarint(), blockRecordsMax+1))
		crc := r.take(4)
		if r.err != nil {
			return nil, r.err
		}
		e.crc = binary.LittleEndian.Uint32(crc)
		if e.offset < 0 || e.offset+int64(e.length) > dataEnd || e.records > blockRecordsMax || e.records == 0 {
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

func (s *segment) firstKey() []byte { return s.index[0].firstKey }

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

func (s *segment) get(p *probeKey, cache *blockCache) (lookup, error) {
	if !s.filter.mayContain(p) {
		return lookup{}, nil
	}
	i := s.blockFor(p.key)
	if i < 0 {
		return lookup{}, nil
	}
	return cache.get(s, i, p.key)
}

type readMode bool

const (
	readBlocks readMode = false
	readAhead  readMode = true
)

type segmentIterator struct {
	s       *segment
	blockAt int
	inBlock int
	r       reader
	buf     []byte
	span    []byte
	spanOff int64
	spanEnd int
	mode    readMode
	current record
	done    bool
}

func (s *segment) iterator(seek []byte, mode readMode) (*segmentIterator, error) {
	it := &segmentIterator{s: s, blockAt: max(s.blockFor(seek), 0) - 1, mode: mode, spanEnd: -1}
	if err := it.loadNextBlock(); err != nil {
		return nil, err
	}
	for steps := 0; steps < blockRecordsMax+1; steps++ {
		if it.done {
			return it, nil
		}
		if bytes.Compare(it.current.key, seek) >= 0 {
			return it, nil
		}
		if err := it.next(); err != nil {
			return nil, err
		}
	}
	assert.Fail("segment seek crossed more than one block")
	return nil, ErrCorrupt
}

func (it *segmentIterator) fillSpan() error {
	idx := it.s.index
	first := idx[it.blockAt]
	end, last := first.offset+int64(first.length), it.blockAt
	for range min(len(idx)-it.blockAt-1, readAheadBytes) {
		e := idx[last+1]
		if e.offset != end {
			break
		}
		if end-first.offset+int64(e.length) > readAheadBytes {
			break
		}
		last++
		end += int64(e.length)
	}
	n := int(end - first.offset)
	if cap(it.buf) < n {
		it.buf = make([]byte, max(n, readAheadBytes))
	}
	it.span = it.buf[:n]
	if _, err := it.s.file.ReadAt(it.span, first.offset); err != nil {
		return err
	}
	it.spanEnd = last
	it.spanOff = first.offset
	return nil
}

func (it *segmentIterator) block() ([]byte, error) {
	if it.mode == readBlocks {
		block, err := it.s.readBlock(it.blockAt, it.buf)
		it.buf = block
		return block, err
	}
	if it.blockAt > it.spanEnd {
		if err := it.fillSpan(); err != nil {
			return nil, err
		}
	}
	e := it.s.index[it.blockAt]
	start := e.offset - it.spanOff
	block := it.span[start : start+int64(e.length)]
	if crc32.Checksum(block, castagnoli) != e.crc {
		return nil, ErrCorrupt
	}
	return block, nil
}

func (it *segmentIterator) loadNextBlock() error {
	it.blockAt++
	if it.blockAt >= len(it.s.index) {
		it.done = true
		return nil
	}
	block, err := it.block()
	if err != nil {
		return err
	}
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
