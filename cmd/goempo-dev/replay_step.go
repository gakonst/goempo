package main

import (
	"strconv"
	"time"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/types"
)

func (r *replayer) getHash(n uint64) primitives.Hash {
	if n >= r.head+1 {
		return primitives.Hash{}
	}
	if r.head+1-n > hashWindow {
		return primitives.Hash{}
	}
	return r.hashes.at(n)
}

func (r *replayer) step(blk *types.Block) bool {
	r.failedBlock = blk
	h := &blk.Header.Inner
	if h.Number == 1 {
		r.hashes.setHash(0, h.ParentHash)
	}
	if r.getHashFn == nil {
		r.getHashFn = r.getHash
	}
	began := time.Now()
	res, err := r.exec.Execute(r.db.State(), executor.Input{Block: blk, GetHash: r.getHashFn, Senders: r.senders, Receipts: r.resultSlot.Receipts})
	r.counters.exec(len(blk.Transactions), time.Since(began))
	if err != nil {
		r.failed = &mismatch{number: h.Number, field: "execution", got: err.Error(), want: "ok"}
		return false
	}
	// resultSlot and its receipts are reused by the next block: a caller
	// that keeps a result copies it.
	r.resultSlot = res
	r.result = &r.resultSlot
	if m := compareBlock(h, &res); m != nil {
		r.failed = m
		return false
	}
	if r.rootEvery > 1 && h.Number%r.rootEvery != 0 && h.Number != r.target &&
		(r.store.checkpoint == 0 || h.Number%r.store.checkpoint != 0) && !r.lastInChunk(h.Number) {
		r.db.State().FinalizeTx()
	} else if r.pipeline {
		if !r.settle() {
			return false
		}
		if err := r.db.State().CommitAsync(h.Number); err != nil {
			r.failed = &mismatch{number: h.Number, field: "commit", got: err.Error(), want: "ok"}
			return false
		}
		r.pending = &pendingRootCheck{number: h.Number, root: h.StateRoot}
	} else {
		root, err := r.db.State().Commit(h.Number)
		if err != nil {
			r.failed = &mismatch{number: h.Number, field: "commit", got: err.Error(), want: "ok"}
			return false
		}
		if root != h.StateRoot {
			r.failed = &mismatch{number: h.Number, field: "stateRoot", got: root.String(), want: h.StateRoot.String()}
			return false
		}
	}
	r.hashes.setHeader(h.Number, &blk.Header)
	r.head = h.Number
	r.counters.add(res.GasUsed, len(blk.Transactions))
	return true
}

func compareBlock(h *types.EthHeader, res *executor.Result) *mismatch {
	if res.GasUsed != h.GasUsed {
		got := strconv.FormatUint(res.GasUsed, 10)
		return &mismatch{number: h.Number, field: "gasUsed", got: got, want: strconv.FormatUint(h.GasUsed, 10)}
	}
	if res.ReceiptsRoot != h.ReceiptsRoot {
		got := res.ReceiptsRoot.String()
		return &mismatch{number: h.Number, field: "receiptsRoot", got: got, want: h.ReceiptsRoot.String()}
	}
	if res.Bloom != h.LogsBloom {
		return &mismatch{number: h.Number, field: "logsBloom", got: "differs", want: "header bloom"}
	}
	return nil
}

// settle waits for the pipelined commit in flight and checks its state root.
// On a mismatch the replay's head goes back to the block before it.
func (r *replayer) settle() bool {
	p := r.pending
	if p == nil {
		return true
	}
	r.pending = nil
	c, err := r.db.State().WaitCommit()
	switch {
	case err != nil:
		r.failed = &mismatch{number: p.number, field: "commit", got: err.Error(), want: "ok"}
	case c.Root != p.root:
		r.failed = &mismatch{number: p.number, field: "stateRoot", got: c.Root.String(), want: p.root.String()}
	default:
		return true
	}
	r.head = p.number - 1
	return false
}

// lastInChunk reports whether number is the last block of a cache chunk, so
// batched roots are checked at least once a chunk and a stop lands on a
// checked block.
func (r *replayer) lastInChunk(number uint64) bool { return (number+1)%chunkBlocks == 0 }
