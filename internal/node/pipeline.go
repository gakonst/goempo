package node

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	gosync "github.com/gakonst/goempo/internal/sync"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	segmentBlocks        = UpstreamBatchMax
	FetchParallelDefault = 32
	fetchParallelMax     = 64
	aheadSegments        = 128
	preparedBatches      = 4
	segmentAttempts      = 8
	linkedWindow         = 1024
	PipelineMinDefault   = 2 * BatchMax
	certCacheMax         = 2 * aheadSegments
	batchBlocksMax       = BatchMax + segmentBlocks
	pipelineSegmentsMax  = 1 << 40
)

const _ = uint(aheadSegments - fetchParallelMax)

const _ = uint(linkedWindow - BatchMax)

const _ = uint(-(BatchMax % segmentBlocks))

const _ = uint(certCacheMax - 2*aheadSegments)

const _ = uint(linkedWindow - batchBlocksMax)

const _ = uint(pipelineSegmentsMax - aheadSegments)

var errPipelineStopped = errors.New("node: pipeline stopped")

type segment struct {
	blocks  []types.Block
	datas   []*rpc.BlockData
	senders [][]executor.Sender
	err     error
	done    chan struct{}
}

type prepared struct {
	from    uint64
	blocks  []types.Block
	datas   []*rpc.BlockData
	senders [][]executor.Sender
	cert    CertifyStats
	fatal   bool
	err     error
}

type span struct {
	from uint64
	to   uint64
}

type pipeline struct {
	n        *Node
	from     uint64
	to       uint64
	tip      uint64
	next     uint64
	parent   gosync.Link
	segs     []segment
	jobs     chan int
	out      chan prepared
	stop     chan struct{}
	done     chan struct{}
	cpu      chan struct{}
	linked   [linkedWindow]primitives.Hash
	wg       sync.WaitGroup
	stopOnce sync.Once
	sent     int
}

type stageNanos struct {
	fetch   atomic.Uint64
	verify  atomic.Uint64
	certify atomic.Uint64
	wait    atomic.Uint64
}

type chainPart struct {
	blocks []types.Block
	hashes []primitives.Hash
}

func startPipeline(n *Node, head gosync.Link, s span, tip uint64) *pipeline {
	assert.Ok(s.to > head.Number, "pipeline covers at least one block")
	assert.Ok(s.from == head.Number+1, "pipeline starts after the head")
	p := &pipeline{
		n: n, from: s.from, to: s.to, tip: tip, next: s.from, parent: head,
		segs: make([]segment, aheadSegments),
		jobs: make(chan int, aheadSegments),
		out:  make(chan prepared, preparedBatches),
		stop: make(chan struct{}),
		done: make(chan struct{}),
		cpu:  make(chan struct{}, max(runtime.GOMAXPROCS(0)-1, 1)),
	}
	assert.Ok(p.total() < pipelineSegmentsMax, "pipeline span is bounded")
	for i := range p.segs {
		p.segs[i].done = make(chan struct{}, 1)
	}
	if n.cert != nil {
		n.cert.pre = &certCache{certs: make(map[uint64][]byte, certCacheMax)}
	}
	for range n.fetchParallel {
		p.wg.Go(p.work)
	}
	p.wg.Go(p.run)
	return p
}

func (p *pipeline) Close() {
	p.stopOnce.Do(func() { close(p.stop) })
	p.wg.Wait()
	if p.n.cert != nil {
		p.n.cert.pre = nil
	}
}

func (p *pipeline) total() int {
	return int((p.to - p.from + segmentBlocks) / segmentBlocks)
}

func (p *pipeline) work() {
	for range min(p.total(), pipelineSegmentsMax) {
		select {
		case k, ok := <-p.jobs:
			if !ok {
				return
			}
			s := &p.segs[k%aheadSegments]
			p.fill(s, k)
			select {
			case s.done <- struct{}{}:
			case <-p.stop:
				return
			}
		case <-p.stop:
			return
		}
	}
}

func (p *pipeline) fill(s *segment, k int) {
	from := p.from + uint64(k)*segmentBlocks
	count := int(min(segmentBlocks, p.to-from+1))
	t0 := time.Now()
	raws, err := p.n.fetchExactly(from, count)
	p.n.stage.fetch.Add(uint64(time.Since(t0)))
	s.blocks, s.datas, s.senders, s.err = nil, nil, nil, err
	if err != nil {
		return
	}
	select {
	case p.cpu <- struct{}{}:
	case <-p.stop:
		s.err = errPipelineStopped
		return
	}
	defer p.release()
	t1 := time.Now()
	part, err := verifySegment(from, raws)
	s.blocks, s.err = part.blocks, err
	p.prefetchCerts(k, from, len(raws))
	blocks := min(len(s.blocks), segmentBlocks)
	s.datas = make([]*rpc.BlockData, blocks)
	s.senders = make([][]executor.Sender, blocks)
	for i := range blocks {
		s.senders[i] = blockSenders(&s.blocks[i])
		s.datas[i] = prepareData(&s.blocks[i], raws[i], part.hashes[i])
	}
	p.n.stage.verify.Add(uint64(time.Since(t1)))
}

func (p *pipeline) release() {
	select {
	case <-p.cpu:
	default:
		assert.Ok(false, "node: a worker releases the CPU slot it holds")
	}
}

func prepareData(blk *types.Block, raw []byte, hash primitives.Hash) *rpc.BlockData {
	data := &rpc.BlockData{Hash: hash, Block: blk, Raw: raw}
	if len(blk.Transactions) > 0 {
		data.TxHash(0)
	}
	return data
}

func verifySegment(from uint64, raws [][]byte) (chainPart, error) {
	assert.Ok(len(raws) <= segmentBlocks, "segment is bounded")
	part := chainPart{blocks: make([]types.Block, 0, len(raws)), hashes: make([]primitives.Hash, 0, len(raws))}
	parent := gosync.Link{Number: from - 1}
	for i := range min(len(raws), segmentBlocks) {
		split, err := gosync.SplitBlock(raws[i])
		if err != nil {
			return part, fmt.Errorf("%w: block %d: %w", ErrChainLink, parent.Number+1, err)
		}
		if i == 0 {
			h, err := types.DecodeHeader(split.Header)
			if err != nil {
				return part, fmt.Errorf("%w: block %d: %w", ErrChainLink, from, err)
			}
			parent.Hash = h.Inner.ParentHash
		}
		v, err := gosync.Verify(parent, split)
		if err != nil {
			return part, fmt.Errorf("%w: block %d: %w", ErrChainLink, parent.Number+1, err)
		}
		part.blocks = append(part.blocks, v.Block)
		part.hashes = append(part.hashes, v.Link.Hash)
		parent = v.Link
	}
	return part, nil
}

func (n *Node) fetchExactly(from uint64, count int) ([][]byte, error) {
	out := make([][]byte, 0, count)
	for range segmentAttempts {
		if len(out) >= count {
			return out, nil
		}
		raws, err := n.fetch(from+uint64(len(out)), count-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, raws[:min(len(raws), count-len(out))]...)
	}
	if len(out) >= count {
		return out, nil
	}
	return nil, fmt.Errorf("%w: %d", ErrNoBlock, from+uint64(len(out)))
}

func (p *pipeline) run() {
	defer close(p.done)
	defer close(p.out)
	defer close(p.jobs)
	total := min(p.total(), pipelineSegmentsMax)
	batch := prepared{from: p.from}
	for next := range total {
		p.dispatch(min(total, next+aheadSegments))
		s := &p.segs[next%aheadSegments]
		select {
		case <-s.done:
		case <-p.stop:
			return
		}
		segErr := p.accept(s, &batch)
		if segErr == nil && len(batch.blocks) < BatchMax && next < total-1 {
			continue
		}
		p.certify(&batch)
		if segErr != nil {
			batch.err = errors.Join(batch.err, segErr)
		}
		if !p.emit(batch) {
			return
		}
		if batch.err != nil {
			return
		}
		batch = prepared{from: p.parent.Number + 1}
	}
}

func (p *pipeline) dispatch(until int) {
	assert.Ok(until-p.sent <= aheadSegments, "dispatch stays within the ring")
	for range min(until-p.sent, aheadSegments) {
		select {
		case p.jobs <- p.sent:
			p.sent++
		case <-p.stop:
			return
		}
	}
}

func (p *pipeline) receive() (prepared, bool) {
	select {
	case b, ok := <-p.out:
		return b, ok
	case <-p.done:
		return p.drain()
	case <-p.stop:
		return prepared{}, false
	}
}

func (p *pipeline) drain() (prepared, bool) {
	select {
	case b, ok := <-p.out:
		return b, ok
	default:
		assert.Ok(false, "node: the pipeline closes its output before done")
		return prepared{}, false
	}
}

func (p *pipeline) accept(s *segment, batch *prepared) error {
	segErr := s.err
	if segErr == nil && len(s.datas) == 0 {
		segErr = fmt.Errorf("%w: %d", ErrNoBlock, p.parent.Number+1)
	}
	if len(s.datas) > 0 {
		first := &s.blocks[0].Header.Inner
		if first.ParentHash != p.parent.Hash || first.Number != p.parent.Number+1 {
			segErr = fmt.Errorf("%w: block %d: %w", ErrChainLink, first.Number, gosync.ErrParent)
			s.blocks, s.datas, s.senders = nil, nil, nil
		}
	}
	for _, d := range s.datas {
		p.linked[d.Number()%linkedWindow] = d.Hash
	}
	if len(s.datas) > 0 {
		p.parent = gosync.Link{Number: p.parent.Number + uint64(len(s.datas)), Hash: s.datas[len(s.datas)-1].Hash}
	}
	batch.blocks = append(batch.blocks, s.blocks...)
	batch.datas = append(batch.datas, s.datas...)
	batch.senders = append(batch.senders, s.senders...)
	s.blocks, s.datas, s.senders = nil, nil, nil
	return segErr
}

func (p *pipeline) emit(b prepared) bool {
	select {
	case p.out <- b:
		return true
	case <-p.stop:
		return false
	}
}

func (p *pipeline) certify(b *prepared) {
	if p.n.cert == nil {
		return
	}
	if len(b.blocks) == 0 {
		return
	}
	t0 := time.Now()
	defer func() { p.n.stage.certify.Add(uint64(time.Since(t0))) }()
	assert.Ok(len(b.blocks) <= batchBlocksMax, "batch is bounded")
	off := 0
	count := len(b.blocks)
	rounds := 0
	for ; rounds < batchBlocksMax; rounds++ {
		if off >= count {
			break
		}
		k, err := p.n.admit(b.blocks[off:], p.tip, p.hashAt)
		if err == nil && k == 0 {
			err = fmt.Errorf("%w: buried block %d admitted nothing", ErrNoBlock, b.blocks[off].Header.Inner.Number)
		}
		if err != nil {
			b.err, b.fatal = err, true
			b.blocks, b.datas, b.senders = b.blocks[:off], b.datas[:off], b.senders[:off]
			break
		}
		off += k
	}
	assert.Ok(rounds < batchBlocksMax || off >= count, "node: every certify round admits a block")
	b.cert = p.n.cert.Stats()
}

func (p *pipeline) hashAt(number uint64, blk *types.Block) primitives.Hash {
	if p.holds(number) {
		return p.linked[number%linkedWindow]
	}
	return p.n.hashAt(number, blk)
}

func (p *pipeline) holds(number uint64) bool {
	if number < p.from {
		return false
	}
	if number > p.parent.Number {
		return false
	}
	return p.parent.Number-number < linkedWindow
}

type certCache struct {
	mu    sync.Mutex
	certs map[uint64][]byte
}

func (c *certCache) put(height uint64, cert []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Ok(len(c.certs) < certCacheMax, "node: prefetched certificates stay bounded")
	c.certs[height] = cert
}

func (c *certCache) take(height uint64) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cert, ok := c.certs[height]
	for h := range c.certs {
		if h <= height {
			delete(c.certs, h)
		}
	}
	return cert, ok
}

func (p *pipeline) prefetchCerts(k int, from uint64, count int) {
	c := p.n.cert
	if c == nil || c.pre == nil || count == 0 {
		return
	}
	last := from + uint64(count) - 1
	if p.batchEnd(k) && last >= c.from {
		p.prefetchCert(last)
	}
	for i := range min(count-1, segmentBlocks) {
		h := from + uint64(i)
		if h >= c.from && c.epocher().IsLast(h) {
			p.prefetchCert(h)
		}
	}
}

func (p *pipeline) batchEnd(k int) bool {
	if k == p.total()-1 {
		return true
	}
	return (k+1)%(BatchMax/segmentBlocks) == 0
}

func (p *pipeline) prefetchCert(height uint64) {
	certs, err := p.n.cert.src.Finalizations(height, 1)
	if err != nil || len(certs) != 1 {
		return
	}
	p.n.cert.pre.put(height, certs[0])
}
