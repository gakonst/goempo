package main

import (
	"compress/flate"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

const convertWorkersMax = 64

var errConvertWorkers = errors.New("convert-chunks: workers out of range")

type convertOptions struct {
	src     string
	dst     string
	from    uint64
	to      uint64
	workers int
}

// runConvertChunks rewrites deflated chunk files as plain .blk files, which
// replay reads without inflating. The block bytes are unchanged.
func runConvertChunks(args []string) int {
	fs := flag.NewFlagSet("convert-chunks", flag.ContinueOnError)
	src := fs.String("dir", defaultChunkDir(), "directory of chunk files to read")
	dst := fs.String("out", "", "directory the plain chunk files are written to")
	from := fs.Uint64("from", 0, "first block")
	to := fs.Uint64("to", chunkBlocks*chunksMax-1, "last block")
	workers := fs.Int("workers", 4, "chunks converted at once")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := convertOptions{src: *src, dst: *dst, from: *from, to: *to, workers: *workers}
	if err := convertChunks(opts); err != nil {
		fmt.Fprintln(os.Stderr, "convert-chunks:", err)
		return 1
	}
	return 0
}

func convertChunks(opts convertOptions) error {
	if opts.workers < 1 || opts.workers > convertWorkersMax {
		return errConvertWorkers
	}
	if opts.dst == "" || opts.dst == opts.src {
		return errors.New("convert-chunks: -out must name another directory")
	}
	if err := os.MkdirAll(opts.dst, dirMode); err != nil {
		return err
	}
	first := opts.from / chunkBlocks
	last := min(opts.to/chunkBlocks, chunksMax-1)
	if last < first {
		return errChunkRange
	}
	count := int64(last - first + 1)
	var cursor atomic.Int64
	var wg sync.WaitGroup
	errs := make([]error, opts.workers)
	for w := range opts.workers {
		wg.Go(func() {
			a := &chunkArena{}
			a.inflate = flate.NewReader(&a.packedRd)
			defer a.inflate.Close()
			for range count {
				k := cursor.Add(1) - 1
				if k >= count {
					return
				}
				start := (first + uint64(k)) * chunkBlocks
				if !chunkPresent(opts.src, start) {
					continue
				}
				if err := convertChunk(a, opts, start); err != nil {
					errs[w] = fmt.Errorf("chunk %d: %w", start, err)
					return
				}
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

func convertChunk(a *chunkArena, opts convertOptions, start uint64) error {
	if err := a.readChunk(opts.src, start); err != nil {
		return err
	}
	path := chunkRawPath(opts.dst, start)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, a.plain, fileMode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
