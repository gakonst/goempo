package node

import (
	"errors"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/types"
)

const MainnetCertifyFrom = 25_165_748

const _ = uint(consensus.BatchMax - UpstreamBatchMax)

type Depth bool

const (
	NearTip Depth = false
	Buried  Depth = true
)

var ErrCertifyBatch = errors.New("node: certificate batch exceeds consensus.BatchMax")

type FinalitySource interface {
	Finalizations(from uint64, count int) ([][]byte, error)
}

type CertifyStats struct {
	Certified   uint64
	Linked      uint64
	Unchecked   uint64
	Transitions uint64
	Trusted     uint64
	Anchor      uint64
}

type Certifier struct {
	chain    *consensus.Chain
	src      FinalitySource
	from     uint64
	anchored bool
	stats    CertifyStats
}

func NewCertifier(chain *consensus.Chain, src FinalitySource, from uint64) *Certifier {
	return &Certifier{chain: chain, src: src, from: from}
}

func (c *Certifier) Stats() CertifyStats { return c.stats }

func (c *Certifier) epocher() consensus.Epocher { return c.chain.Epocher() }

func (c *Certifier) Anchor(boundary uint64, block *types.Block) error {
	if _, err := c.chain.RegisterBoundary(boundary, block.Header.Inner.ExtraData); err != nil {
		return err
	}
	c.anchored = true
	c.stats.Anchor = boundary
	return nil
}

func (c *Certifier) NeedsAnchor(first uint64) (uint64, bool) {
	if c.anchored || first < c.from {
		return 0, false
	}
	epoch := c.epocher().Epoch(first)
	if epoch == 0 {
		c.anchored = true
		return 0, false
	}
	return c.epocher().Last(epoch - 1), true
}

func (c *Certifier) span(blocks []types.Block) int {
	for i := range blocks {
		if c.epocher().IsLast(blocks[i].Header.Inner.Number) {
			return i + 1
		}
	}
	return len(blocks)
}

func (c *Certifier) Admit(blocks []types.Block, depth Depth) (int, error) {
	n := c.span(blocks)
	sub := blocks[:n]
	if sub[n-1].Header.Inner.Number < c.from {
		c.stats.Unchecked += uint64(n)
		return n, c.closeEpoch(&sub[n-1])
	}
	skip := 0
	for skip < n && sub[skip].Header.Inner.Number < c.from {
		skip++
	}
	k, err := c.verifySparse(sub[skip:], depth)
	if err != nil {
		return 0, err
	}
	admitted := skip + k
	c.stats.Unchecked += uint64(skip)
	if admitted == n {
		if c.epocher().IsLast(sub[n-1].Header.Inner.Number) {
			c.stats.Transitions++
		}
		return n, c.closeEpoch(&sub[n-1])
	}
	if depth == NearTip {
		return admitted, nil
	}
	c.stats.Unchecked += uint64(n - admitted)
	if c.epocher().IsLast(sub[n-1].Header.Inner.Number) {
		c.stats.Trusted++
	}
	return n, c.closeEpoch(&sub[n-1])
}

func (c *Certifier) verifySparse(sub []types.Block, depth Depth) (int, error) {
	if depth == NearTip || len(sub) < 2 {
		return c.verify(sub)
	}
	last := &sub[len(sub)-1]
	certs, err := c.fetch(last.Header.Inner.Number, 1)
	if err != nil {
		return 0, err
	}
	if certs[0] == nil {
		return c.verify(sub)
	}
	if err := c.chain.Verify([]consensus.Certified{certified(last, certs[0])}); err != nil {
		return 0, err
	}
	c.stats.Certified++
	c.stats.Linked += uint64(len(sub) - 1)
	return len(sub), nil
}

func (c *Certifier) verify(sub []types.Block) (int, error) {
	if len(sub) == 0 {
		return 0, nil
	}
	certs, err := c.fetch(sub[0].Header.Inner.Number, len(sub))
	if err != nil {
		return 0, err
	}
	batch := make([]consensus.Certified, 0, len(sub))
	covered := 0
	for i := range sub {
		if certs[i] == nil {
			continue
		}
		batch = append(batch, certified(&sub[i], certs[i]))
		covered = i + 1
	}
	if len(batch) == 0 {
		return 0, nil
	}
	if err := c.chain.Verify(batch); err != nil {
		return 0, err
	}
	c.stats.Certified += uint64(len(batch))
	c.stats.Linked += uint64(covered - len(batch))
	return covered, nil
}

func (c *Certifier) fetch(from uint64, count int) ([][]byte, error) {
	if count > consensus.BatchMax {
		return nil, ErrCertifyBatch
	}
	out := make([][]byte, 0, count)
	for i := 0; i < count; i += UpstreamParallel * UpstreamBatchMax {
		part, err := c.src.Finalizations(from+uint64(i), min(UpstreamParallel*UpstreamBatchMax, count-i))
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	if len(out) < count {
		return nil, ErrUpstreamReply
	}
	return out, nil
}

func certified(block *types.Block, cert []byte) consensus.Certified {
	item := consensus.Certified{Height: block.Header.Inner.Number, Hash: consensus.Digest(block.Hash()), Certificate: cert}
	if ctx := block.Header.Consensus; ctx != nil {
		item.Context = &consensus.Proposal{Round: consensus.Round{Epoch: ctx.Epoch, View: ctx.View}, Parent: ctx.ParentView}
	}
	return item
}

func (c *Certifier) closeEpoch(block *types.Block) error {
	h := block.Header.Inner.Number
	if !c.epocher().IsLast(h) || !c.anchored {
		return nil
	}
	_, err := c.chain.RegisterBoundary(h, block.Header.Inner.ExtraData)
	return err
}
