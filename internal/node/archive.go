package node

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	goio "github.com/gakonst/goempo/internal/io"
)

const (
	ArchiveChunkBlocks  = 10_000
	archiveBlockBytes   = 1 << 22
	archiveChunkBytes   = 1 << 36
	archiveLengthPrefix = 4
	archiveCacheChunks  = 2
	archiveSuffix       = ".blkz"
	archiveRawSuffix    = ".blk"
)

const _ = uint(archiveChunkBytes - ArchiveChunkBlocks*archiveBlockBytes)

const _ = uint(ArchiveChunkBlocks - archiveCacheChunks)

var (
	ErrNotArchived  = errors.New("node: block is not in the archive")
	ErrArchiveChunk = errors.New("node: archive chunk is malformed")
)

type Archive struct {
	mu     sync.Mutex
	fs     goio.FS
	chunks map[uint64][][]byte
}

func NewArchive(fs goio.FS) *Archive {
	return &Archive{fs: fs, chunks: map[uint64][][]byte{}}
}

func ArchiveChunkName(start uint64) string {
	return fmt.Sprintf("%010d%s", start, archiveSuffix)
}

func (a *Archive) RawBlocks(from uint64, count int) ([][]byte, error) {
	out := make([][]byte, 0, count)
	for i := range count {
		raw, err := a.Raw(from + uint64(i))
		if err != nil {
			return out, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func (a *Archive) Raw(number uint64) ([]byte, error) {
	start := number / ArchiveChunkBlocks * ArchiveChunkBlocks
	a.mu.Lock()
	defer a.mu.Unlock()
	raws, ok := a.chunks[start]
	if !ok {
		var err error
		if raws, err = a.load(start); err != nil {
			return nil, err
		}
		if len(a.chunks) >= archiveCacheChunks {
			clear(a.chunks)
		}
		a.chunks[start] = raws
	}
	i := number - start
	if i >= uint64(len(raws)) {
		return nil, ErrNotArchived
	}
	return raws[i], nil
}

func (a *Archive) load(start uint64) ([][]byte, error) {
	data, err := a.read(fmt.Sprintf("%010d%s", start, archiveSuffix), true)
	if errors.Is(err, goio.ErrNotExist) {
		data, err = a.read(fmt.Sprintf("%010d%s", start, archiveRawSuffix), false)
	}
	if errors.Is(err, goio.ErrNotExist) {
		return nil, ErrNotArchived
	}
	if err != nil {
		return nil, err
	}
	return SplitChunk(data)
}

type compression bool

func (a *Archive) read(name string, packed compression) ([]byte, error) {
	f, err := a.fs.Open(name)
	if err != nil {
		return nil, fmt.Errorf("archive %s: %w", name, err)
	}
	size := f.Size()
	if size < 0 || size > archiveChunkBytes {
		return nil, errors.Join(ErrArchiveChunk, f.Close())
	}
	buf := make([]byte, size)
	_, err = f.ReadAt(buf, 0)
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, fmt.Errorf("archive %s: %w", name, err)
	}
	if !packed {
		return buf, nil
	}
	zr := flate.NewReader(bytes.NewReader(buf))
	out, err := io.ReadAll(io.LimitReader(zr, archiveChunkBytes))
	if err = errors.Join(err, zr.Close()); err != nil {
		return nil, fmt.Errorf("archive %s: %w", name, err)
	}
	return out, nil
}

func SplitChunk(data []byte) ([][]byte, error) {
	blocks := make([][]byte, 0, ArchiveChunkBlocks)
	for i := 0; i < ArchiveChunkBlocks && len(data) > 0; i++ {
		if len(data) < archiveLengthPrefix {
			return nil, ErrArchiveChunk
		}
		n := int(binary.BigEndian.Uint32(data))
		data = data[archiveLengthPrefix:]
		if n > len(data) {
			return nil, ErrArchiveChunk
		}
		blocks = append(blocks, data[:n])
		data = data[n:]
	}
	if len(data) != 0 {
		return nil, ErrArchiveChunk
	}
	return blocks, nil
}

func PackChunk(raws [][]byte) ([]byte, error) {
	var plain bytes.Buffer
	for _, raw := range raws {
		if len(raw) == 0 || len(raw) > archiveBlockBytes {
			return nil, ErrArchiveChunk
		}
		var prefix [archiveLengthPrefix]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(raw)))
		plain.Write(prefix[:])
		plain.Write(raw)
	}
	var packed bytes.Buffer
	zw, err := flate.NewWriter(&packed, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(plain.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return packed.Bytes(), nil
}
