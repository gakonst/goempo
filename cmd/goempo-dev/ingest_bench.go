package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"syscall"
	"time"
)

// runIngestBench reads, inflates, decodes and recovers the senders of a
// block range without executing it, and reports the CPU it took.
func runIngestBench(args []string) int {
	fs := flag.NewFlagSet("ingest-bench", flag.ContinueOnError)
	dir := fs.String("dir", defaultChunkDir(), "directory of chunk files")
	to := fs.Uint64("to", chunkBlocks*chunksMax-1, "last block")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := ingestBench(*dir, *to); err != nil {
		fmt.Fprintln(os.Stderr, "ingest-bench:", err)
		return 1
	}
	return 0
}

func ingestBench(dir string, to uint64) error {
	ctx := context.Background()
	pool := newChunkPool()
	chunks := min(to/chunkBlocks+1, chunksMax)
	began := time.Now()
	blocks, txs := 0, 0
	for i := range chunks {
		p := prefetchChunk(ctx, pool, dir, i*chunkBlocks)
		got := p.take(ctx)
		p.Wait()
		if got.err != nil {
			return got.err
		}
		for _, b := range got.blocks {
			if b.err != nil {
				return errors.Join(errChunk, b.err)
			}
			txs += len(b.senders)
		}
		blocks += len(got.blocks)
		pool.put(got.arena)
	}
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return err
	}
	cpu := time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	fmt.Printf("ingest blocks=%d txs=%d wall=%.2fs cpu=%.2fs\n", blocks, txs, time.Since(began).Seconds(), cpu.Seconds())
	return nil
}
