package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/types"
)

const (
	healedFileName    = "healed.json"
	checkpointDefault = 100_000
	stateCacheScale   = 4
)

var errResumeRoot = errors.New("datadir head root does not match the block's state root")

type storeConfig struct {
	datadir    string
	checkpoint uint64
}

func openReplayState(cfg storeConfig) (*state.DB, error) {
	if cfg.datadir == "" {
		return state.Open(goio.NewMemFS(), state.Options{})
	}
	fs, err := goio.NewDirFS(cfg.datadir)
	if err != nil {
		return nil, err
	}
	opts := state.Options{
		MemtableBytes: 256 << 20,
		CacheSlots:    stateCacheScale << 22,
		CacheAccounts: stateCacheScale << 20,
	}
	return state.Open(fs, opts)
}

func (r *replayer) resume() error {
	head := r.db.Head()
	r.head = head.Number
	if head.Number == 0 {
		return nil
	}
	first := uint64(1)
	if head.Number >= hashWindow {
		first = head.Number - hashWindow + 1
	}
	var last *types.Block
	for start := first / chunkBlocks * chunkBlocks; start <= head.Number; start += chunkBlocks {
		raws, err := readChunk(r.dir, start)
		if err != nil {
			return err
		}
		blk, err := r.hashChunk(raws, chunkSpan{start: start, to: head.Number}, first)
		if err != nil {
			return err
		}
		if blk != nil {
			last = blk
		}
	}
	if last == nil || last.Header.Inner.Number != head.Number {
		return fmt.Errorf("cache lacks block %d to resume from", head.Number)
	}
	if last.Header.Inner.StateRoot != head.Root {
		return errResumeRoot
	}
	return r.loadHealed()
}

func (r *replayer) hashChunk(raws [][]byte, span chunkSpan, first uint64) (*types.Block, error) {
	var last *types.Block
	for j := range raws {
		number := span.start + uint64(j)
		if number < first || number > span.to {
			continue
		}
		blk, err := types.DecodeBlock(raws[j])
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", number, err)
		}
		if number == 1 {
			r.hashes.setHash(0, blk.Header.Inner.ParentHash)
		}
		r.hashes.setHeader(number, &blk.Header)
		last = &blk
	}
	return last, nil
}

func (r *replayer) checkpoint(number uint64) error {
	if r.store.datadir == "" || r.store.checkpoint == 0 || number%r.store.checkpoint != 0 {
		return nil
	}
	if err := r.db.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "replay: checkpoint at block %d\n", number)
	return nil
}

func (r *replayer) closeStore() error {
	if r.store.datadir == "" {
		return nil
	}
	if err := r.saveHealed(); err != nil {
		return errors.Join(err, r.db.Close())
	}
	if err := r.db.Flush(); err != nil {
		return errors.Join(err, r.db.Close())
	}
	return r.db.Close()
}

type healedRecord struct {
	Number uint64 `json:"number"`
	Field  string `json:"field"`
	Got    string `json:"got"`
	Want   string `json:"want"`
	Head   uint64 `json:"head"`
}

func (r *replayer) loadHealed() error {
	data, err := os.ReadFile(filepath.Join(r.store.datadir, healedFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var rec healedRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return err
	}
	r.healedFrom = &mismatch{number: rec.Number, field: rec.Field, got: rec.Got, want: rec.Want}
	r.healedHead = rec.Head
	return nil
}

func (r *replayer) saveHealed() error {
	if r.healedFrom != nil || r.heal == nil || r.heal.first == nil {
		return nil
	}
	m := r.heal.first
	rec := healedRecord{Number: m.number, Field: m.field, Got: m.got, Want: m.want, Head: r.firstHead}
	data, err := json.Marshal(&rec)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.store.datadir, healedFileName), append(data, '\n'), fileMode)
}

func stopRequested(ctx context.Context) bool {
	return ctx.Err() != nil
}
