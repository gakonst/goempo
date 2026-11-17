package main

import (
	"strconv"

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
	res, err := r.exec.Execute(r.db.State(), executor.Input{Block: blk, GetHash: r.getHash})
	if err != nil {
		r.failed = &mismatch{number: h.Number, field: "execution", got: err.Error(), want: "ok"}
		return false
	}
	r.result = &res
	if m := compareBlock(h, &res); m != nil {
		r.failed = m
		return false
	}
	root, err := r.db.State().Commit(h.Number)
	if err != nil {
		r.failed = &mismatch{number: h.Number, field: "commit", got: err.Error(), want: "ok"}
		return false
	}
	if root != h.StateRoot {
		r.failed = &mismatch{number: h.Number, field: "stateRoot", got: root.String(), want: h.StateRoot.String()}
		return false
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
