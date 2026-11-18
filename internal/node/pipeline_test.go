package node_test

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/node/nodetest"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

// buriedTip is a tip far enough past the fixture blocks that the node
// catches up through its pipeline.
const buriedTip = fixtureHead + node.BatchMax + 64

type limited struct {
	inner node.BlockSource
	last  uint64
}

func (s limited) RawBlocks(from uint64, count int) ([][]byte, error) {
	if from > s.last {
		return nil, node.ErrNoBlock
	}
	return s.inner.RawBlocks(from, int(min(uint64(count), s.last-from+1)))
}

func pipelined(sources ...node.BlockSource) node.Config {
	return node.Config{State: goio.NewMemFS(), Sources: sources, RootEvery: 64, FetchParallel: 4, PipelineMin: 1}
}

func TestPipelinedCatchUpOverHTTP(t *testing.T) {
	up := &nodetest.LocalUpstream{Archive: testArchive(t), Tip: buriedTip, RejectEvery: 3}
	srv := httptest.NewServer(up)
	defer srv.Close()
	src := node.NewUpstream(goio.NewHTTPCaller(srv.URL, 10e9))
	cfg := pipelined(src)
	n, err := node.Open(cfg)
	assert.NoError(err, "open")
	for range advanceRounds {
		if n.Head().Number >= fixtureHead {
			break
		}
		_, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip})
		assert.NoError(err, "catch up over HTTP")
	}
	assert.Equal(n.Head().Hash.String(), block200Hash, "head hash matches mainnet")
	s := n.Stats()
	assert.Equal(s.Blocks, uint64(fixtureHead), "every block executed once")
	assert.Equal(s.RootChecks, uint64(4), "roots checked at 64, 128, 192 and the target")
	assert.Ok(up.Rejected.Load() > 0, "the upstream refused some requests with 429")
	assert.Ok(s.FetchNanos > 0, "fetch time is recorded")
	assert.Ok(s.VerifyNanos > 0, "verify time is recorded")
	assert.Ok(s.ExecNanos > 0, "exec time is recorded")
	b, ok := n.Recent().Block(fixtureHead - 1)
	assert.Ok(ok, "recent blocks are kept")
	assert.Ok(b.Receipts != nil, "recent blocks keep their receipts")
	assert.NoError(n.Close(), "close")
	n, err = node.Open(cfg)
	assert.NoError(err, "reopen")
	assert.Equal(n.Head().Hash.String(), block200Hash, "resumes at the head")
	assert.NoError(n.Close(), "close")
}

func TestPipelinedCheckpointOnClose(t *testing.T) {
	archive := testArchive(t)
	cfg := pipelined(limited{inner: archive, last: 150})
	cfg.RootEvery = 100
	n, err := node.Open(cfg)
	assert.NoError(err, "open")
	_, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip})
	for range advanceRounds {
		if err != nil || n.Head().Number >= 128 {
			break
		}
		_, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip})
	}
	assert.Ok(errors.Is(err, node.ErrNoBlock), "the source runs out mid-segment")
	assert.NoError(n.Failed(), "a missing block does not latch")
	head := n.Head()
	assert.Equal(head.Number, uint64(128), "whole segments up to the missing block execute")
	assert.NoError(n.Close(), "close checks and writes the unchecked blocks")
	n, err = node.Open(cfg)
	assert.NoError(err, "reopen")
	assert.Equal(n.Head(), head, "resumes at the head the last run reached")
	assert.Equal(n.Stats().RootChecks, uint64(0), "nothing executed yet")
	assert.NoError(n.Close(), "close")
}

func TestPipelinedBrokenLinkDoesNotLatch(t *testing.T) {
	archive := testArchive(t)
	bad := tampered{inner: archive, number: 70, edit: func(raw []byte) []byte {
		blk, err := types.DecodeBlock(raw)
		assert.NoError(err, "decode")
		parent := blk.Header.Inner.ParentHash
		raw[bytes.Index(raw, parent[:])] ^= 0xff
		return raw
	}}
	n, err := node.Open(pipelined(bad))
	assert.NoError(err, "open")
	defer func() { assert.NoError(n.Close(), "close") }()
	var p node.Progress
	for range advanceRounds {
		if p, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip}); err != nil {
			break
		}
	}
	assert.Ok(errors.Is(err, node.ErrChainLink), "the broken link is reported")
	assert.NoError(n.Failed(), "a broken link does not latch")
	assert.Equal(n.Head().Number, uint64(69), "blocks before the broken link execute")
	assert.Equal(p.Head, uint64(69), "progress names the head")
	_, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip})
	assert.Ok(errors.Is(err, node.ErrChainLink), "a retry meets the same link")
	assert.Equal(n.Head().Number, uint64(69), "and executes nothing past it")
}

func TestPipelinedBatchedStateRootMismatch(t *testing.T) {
	archive := testArchive(t)
	const target = 100
	bad := tampered{inner: archive, number: target, edit: func(raw []byte) []byte {
		blk, err := types.DecodeBlock(raw)
		assert.NoError(err, "decode")
		root := blk.Header.Inner.StateRoot
		raw[bytes.Index(raw, root[:])] ^= 0xff
		return raw
	}}
	n, err := node.Open(pipelined(bad))
	assert.NoError(err, "open")
	for range advanceRounds {
		if _, err = n.Advance(node.Target{To: target, Tip: buriedTip}); err != nil || n.Head().Number >= target {
			break
		}
	}
	var be *node.BlockError
	assert.Ok(errors.As(err, &be), "a block error")
	assert.Equal(be.Number, uint64(target), "the checked block is named")
	assert.Equal(be.Field, "stateRoot (one check over blocks 65..100)", "the error names the blocks one check covered")
	assert.Equal(n.Failed(), err, "a state root mismatch latches")
	assert.Ok(errors.Is(n.Close(), node.ErrTainted), "a node with a bad root refuses to close cleanly")
}

func TestPipelinedReceiptsMismatchLatches(t *testing.T) {
	archive := testArchive(t)
	bad := tampered{inner: archive, number: 90, edit: func(raw []byte) []byte {
		blk, err := types.DecodeBlock(raw)
		assert.NoError(err, "decode")
		root := blk.Header.Inner.ReceiptsRoot
		raw[bytes.Index(raw, root[:])] ^= 0xff
		return raw
	}}
	cfg := pipelined(bad)
	n, err := node.Open(cfg)
	assert.NoError(err, "open")
	for range advanceRounds {
		if _, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip}); err != nil {
			break
		}
	}
	var be *node.BlockError
	assert.Ok(errors.As(err, &be), "a block error")
	assert.Equal(be.Number, uint64(90), "the bad block is named")
	assert.Equal(be.Field, "receiptsRoot", "receipts root")
	assert.Equal(n.Head().Number, uint64(89), "the head stays before the bad block")
	assert.NoError(n.Close(), "close keeps the last checked state")
	n, err = node.Open(cfg)
	assert.NoError(err, "reopen")
	assert.Equal(n.Head().Number, uint64(64), "resumes at the last checked root")
	assert.NoError(n.Close(), "close")
}

type countingFinality struct {
	mu    sync.Mutex
	calls map[[2]uint64]int
}

func (f *countingFinality) Finalizations(from uint64, count int) ([][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[[2]uint64{from, uint64(count)}]++
	return make([][]byte, count), nil
}

func TestPipelinedPrefetchesCertificates(t *testing.T) {
	archive := testArchive(t)
	fin := &countingFinality{calls: map[[2]uint64]int{}}
	cfg := pipelined(archive)
	cfg.Finality, cfg.CertifyFrom = fin, 1
	n, err := node.Open(cfg)
	assert.NoError(err, "open")
	defer func() { assert.NoError(n.Close(), "close") }()
	for range advanceRounds {
		if n.Head().Number >= fixtureHead {
			break
		}
		_, err = n.Advance(node.Target{To: fixtureHead, Tip: buriedTip})
		assert.NoError(err, "buried blocks without certificates are admitted")
	}
	assert.Equal(n.Head().Hash.String(), block200Hash, "head hash matches mainnet")
	assert.Equal(n.Stats().Certify.Unchecked, uint64(fixtureHead), "every block is admitted unchecked")
	assert.Equal(fin.calls[[2]uint64{fixtureHead, 1}], 1, "the batch's last certificate is fetched once, ahead of the certifier")
}
