package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/types"
)

const followPendingMax = 4 * followCheckBatch

const _ = uint(consensus.BatchMax - followPendingMax)

const _ = uint(certifySpanMax - followPendingMax)

type pendingBlock struct {
	height     uint64
	hash       consensus.Digest
	ctx        consensus.Proposal
	hasContext bool
}

type followCertifier struct {
	chain       *consensus.Chain
	fetch       rawFetcher
	pending     []pendingBlock
	verified    uint64
	linked      uint64
	transitions uint64
	anchor      string
}

func newFollowCertifier(f rawFetcher, first uint64) (*followCertifier, error) {
	c := &followCertifier{chain: consensus.NewMainnetChain(), fetch: f}
	ep := c.chain.Epocher()
	epoch := ep.Epoch(first)
	if epoch == 0 {
		c.anchor = "genesis"
		return c, nil
	}
	boundary := ep.Last(epoch - 1)
	raw, err := f.rawBlock(boundary)
	if err != nil {
		return nil, err
	}
	blk, err := types.DecodeBlock(raw)
	if err != nil {
		return nil, err
	}
	if _, err := c.chain.RegisterBoundary(boundary, blk.Header.Inner.ExtraData); err != nil {
		return nil, err
	}
	id, _ := c.chain.Identity(epoch)
	b := id.Bytes()
	c.anchor = fmt.Sprintf("dkg outcome in block %d %s (identity %x..., taken on trust)", boundary, blk.Hash(), b[:8])
	return c, nil
}

func (c *followCertifier) add(block *types.Block) error {
	h := block.Header.Inner.Number
	p := pendingBlock{height: h, hash: consensus.Digest(block.Hash())}
	if ctx := block.Header.Consensus; ctx != nil {
		p.ctx = consensus.Proposal{Round: consensus.Round{Epoch: ctx.Epoch, View: ctx.View}, Parent: ctx.ParentView}
		p.hasContext = true
	}
	c.pending = append(c.pending, p)
	if c.chain.Epocher().IsLast(h) {
		return c.closeEpoch(h, block.Header.Inner.ExtraData)
	}
	if len(c.pending) >= followCheckBatch {
		return c.flush()
	}
	return nil
}

func (c *followCertifier) closeEpoch(h uint64, extra []byte) error {
	for attempt := range followRetries {
		if err := c.flush(); err != nil {
			return err
		}
		if len(c.pending) == 0 {
			break
		}
		time.Sleep(min(rpcRetryBackoff<<attempt, rpcBackoffMax))
	}
	if len(c.pending) > 0 {
		return &consensus.HeightError{Height: h, Err: consensus.ErrCertificate}
	}
	next, err := c.chain.RegisterBoundary(h, extra)
	if err != nil {
		return err
	}
	id, _ := c.chain.Identity(next)
	b := id.Bytes()
	c.transitions++
	fmt.Fprintf(os.Stderr, "epoch %d: identity %x... from the dkg outcome in certified block %d\n", next, b[:8], h)
	return nil
}

func (c *followCertifier) flush() error {
	if len(c.pending) == 0 {
		return nil
	}
	if len(c.pending) > followPendingMax {
		return &consensus.HeightError{Height: c.pending[0].height, Err: errUncovered}
	}
	certs, err := c.fetch.finalizations(certifyItem{lo: c.pending[0].height, hi: c.pending[len(c.pending)-1].height})
	if err != nil {
		return err
	}
	batch := make([]consensus.Certified, 0, len(c.pending))
	covered := 0
	for i := range c.pending {
		if certs[i] == nil {
			continue
		}
		item := consensus.Certified{Height: c.pending[i].height, Hash: c.pending[i].hash, Certificate: certs[i]}
		if c.pending[i].hasContext {
			item.Context = &c.pending[i].ctx
		}
		batch = append(batch, item)
		covered = i + 1
	}
	if len(batch) == 0 {
		return nil
	}
	if err := c.chain.Verify(batch); err != nil {
		return err
	}
	c.verified += uint64(len(batch))
	c.linked += uint64(covered - len(batch))
	c.pending = append(c.pending[:0], c.pending[covered:]...)
	return nil
}
