package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/state"
	gosync "github.com/gakonst/goempo/internal/sync"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	BatchMax        = 512
	hashWindow      = 256
	healedFileName  = "healed.json"
	fetchAttempts   = BatchMax
	deepMarginBlock = BatchMax
)

const (
	recoverWorkersMax = 64
	NearTipRoots      = 64
)

const _ = uint(512 - BatchMax)

const _ = uint(BatchMax - recoverWorkersMax)

var (
	ErrNoSource   = errors.New("node: no block source")
	ErrNoBlock    = errors.New("node: no source holds the block")
	ErrResumeRoot = errors.New("node: head block's state root differs from the database root")
	ErrHealed     = errors.New("node: datadir was healed past a mismatch; its state is not trusted")
	ErrNoState    = errors.New("node: state is kept at the head only")
	ErrChainLink  = errors.New("node: block does not link to its parent")
	ErrTainted    = errors.New("node: a mismatching block was committed; refusing to flush")
)

type BlockError struct {
	Number uint64
	Field  string
	Got    string
	Want   string
}

func (e *BlockError) Error() string {
	return fmt.Sprintf("block %d: %s: got %s, want %s", e.Number, e.Field, e.Got, e.Want)
}

type BlockSource interface {
	RawBlocks(from uint64, count int) ([][]byte, error)
}

type Config struct {
	State         goio.FS
	StateOptions  state.Options
	Sources       []BlockSource
	Archive       *Archive
	Finality      FinalitySource
	CertifyFrom   uint64
	RecentBlocks  int
	RootEvery     uint64
	FetchParallel int
	PipelineMin   int
}

type Stats struct {
	Blocks       uint64
	Txs          uint64
	Gas          uint64
	ExecNanos    uint64
	CommitNanos  uint64
	FetchNanos   uint64
	VerifyNanos  uint64
	CertifyNanos uint64
	WaitNanos    uint64
	FlushNanos   uint64
	RootChecks   uint64
	Certify      CertifyStats
}

type Target struct {
	To  uint64
	Tip uint64
}

type Progress struct {
	Head     uint64
	Executed int
	Waiting  bool
	Blocks   []*rpc.BlockData
}

type Node struct {
	mu       sync.Mutex
	db       *state.DB
	exec     *executor.Executor
	sources  []BlockSource
	cert     *Certifier
	recent   *Recent
	hashes   [hashWindow]primitives.Hash
	head     uint64
	headHash primitives.Hash
	start    uint64
	failed   error
	tainted  bool
	stats    Stats

	rootEvery     uint64
	fetchParallel int
	pipelineMin   uint64
	pipe          *pipeline
	stage         stageNanos
	certStats     CertifyStats
	headRoot      primitives.Hash
	unchecked     uint64
	pending       *rootCheck
	stale         bool
}

type rootCheck struct {
	from   uint64
	number uint64
	want   primitives.Hash
}

func Open(cfg Config) (*Node, error) {
	if len(cfg.Sources) == 0 {
		return nil, ErrNoSource
	}
	if f, err := cfg.State.Open(healedFileName); err == nil {
		return nil, errors.Join(ErrHealed, f.Close())
	}
	db, err := state.Open(cfg.State, cfg.StateOptions)
	if err != nil {
		return nil, err
	}
	n, err := open(db, &cfg)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return n, nil
}

func open(db *state.DB, cfg *Config) (*Node, error) {
	g, err := genesis.Mainnet()
	if err != nil {
		return nil, err
	}
	if !db.Head().Loaded {
		if _, err := db.LoadGenesis(g); err != nil {
			return nil, err
		}
	}
	var extra struct {
		EpochLength uint64 `json:"epochLength"`
	}
	if err := json.Unmarshal(g.Config.Raw, &extra); err != nil {
		return nil, err
	}
	recent := cfg.RecentBlocks
	if recent == 0 {
		recent = DefaultRecentBlocks
	}
	n := &Node{
		db:            db,
		exec:          &executor.Executor{ChainID: hardfork.MainnetChainID, EpochLength: extra.EpochLength, Parallel: executor.Parallel{Workers: 16, MinTxs: 64}},
		sources:       cfg.Sources,
		recent:        NewRecent(recent, cfg.Archive),
		rootEvery:     max(cfg.RootEvery, 1),
		fetchParallel: FetchParallelDefault,
		pipelineMin:   PipelineMinDefault,
	}
	if cfg.FetchParallel > 0 {
		n.fetchParallel = min(cfg.FetchParallel, fetchParallelMax)
	}
	if cfg.PipelineMin > 0 {
		n.pipelineMin = uint64(cfg.PipelineMin)
	}
	if cfg.PipelineMin < 0 {
		n.pipelineMin = 0
	}
	if cfg.Finality != nil {
		n.cert = NewCertifier(consensus.NewMainnetChain(), cfg.Finality, cfg.CertifyFrom)
	}
	return n, n.resume()
}

func (n *Node) resume() error {
	head := n.db.Head()
	first := uint64(0)
	if head.Number >= hashWindow {
		first = head.Number - hashWindow + 1
	}
	if first > 0 {
		if err := n.loadGenesisBlock(); err != nil {
			return err
		}
	}
	raws, err := n.fetchRange(first, int(head.Number-first+1))
	if err != nil {
		return err
	}
	var parent primitives.Hash
	for i, raw := range raws {
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			return err
		}
		hash := blk.Hash()
		if i > 0 && blk.Header.Inner.ParentHash != parent {
			return &BlockError{Number: blk.Header.Inner.Number, Field: "parentHash", Got: blk.Header.Inner.ParentHash.String(), Want: parent.String()}
		}
		if i == len(raws)-1 && blk.Header.Inner.StateRoot != head.Root {
			return ErrResumeRoot
		}
		parent = hash
		n.hashes[blk.Header.Inner.Number%hashWindow] = hash
		if data, err := rpc.NewBlockData(&blk, raw, nil); err == nil {
			n.recent.Add(data)
		}
	}
	n.head, n.headHash, n.headRoot, n.start = head.Number, parent, head.Root, head.Number
	n.recent.SetHead(head.Number)
	return nil
}

func (n *Node) loadGenesisBlock() error {
	raws, err := n.fetchRange(0, 1)
	if err != nil {
		return err
	}
	blk, err := types.DecodeBlock(raws[0])
	if err != nil {
		return err
	}
	data, err := rpc.NewBlockData(&blk, raws[0], nil)
	if err != nil {
		return err
	}
	n.recent.Add(data)
	return nil
}

func (n *Node) fetch(from uint64, count int) ([][]byte, error) {
	var errs []error
	for _, s := range n.sources {
		raws, err := s.RawBlocks(from, count)
		if len(raws) > 0 {
			return raws, nil
		}
		if err != nil && !errors.Is(err, ErrNotArchived) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, fmt.Errorf("%w: %d", ErrNoBlock, from)
}

func (n *Node) fetchRange(from uint64, count int) ([][]byte, error) {
	count = min(count, hashWindow)
	out := make([][]byte, 0, count)
	for range fetchAttempts {
		if len(out) >= count {
			return out, nil
		}
		raws, err := n.fetch(from+uint64(len(out)), count-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, raws[:min(len(raws), count-len(out))]...)
	}
	return nil, fmt.Errorf("%w: %d", ErrNoBlock, from)
}

func (n *Node) Head() gosync.Link {
	n.mu.Lock()
	defer n.mu.Unlock()
	return gosync.Link{Number: n.head, Hash: n.headHash}
}

func (n *Node) Start() uint64 { return n.start }

func (n *Node) Stats() Stats {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.stats
	s.Certify = n.certStats
	s.FetchNanos = n.stage.fetch.Load()
	s.VerifyNanos = n.stage.verify.Load()
	s.CertifyNanos = n.stage.certify.Load()
	s.WaitNanos = n.stage.wait.Load()
	return s
}

func (n *Node) Failed() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.failed
}

func (n *Node) Recent() *Recent { return n.recent }

func (n *Node) Flush() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tainted {
		return ErrTainted
	}
	if err := n.checkpointLocked(); err != nil {
		return n.latchLocked(err)
	}
	t0 := time.Now()
	err := n.db.Flush()
	n.stats.FlushNanos += uint64(time.Since(t0))
	return err
}

func (n *Node) Close() error {
	n.stopPipeline()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tainted {
		return ErrTainted
	}
	if err := n.checkpointLocked(); err != nil {
		return n.latchLocked(err)
	}
	return n.db.Close()
}

func (n *Node) stopPipeline() {
	if n.pipe != nil {
		n.pipe.Close()
		n.pipe = nil
	}
}

func (n *Node) getHash(number uint64) primitives.Hash {
	if number > n.head || n.head-number >= hashWindow {
		return primitives.Hash{}
	}
	return n.hashes[number%hashWindow]
}

func (n *Node) Advance(t Target) (Progress, error) {
	head := n.Head()
	p := Progress{Head: head.Number}
	if err := n.Failed(); err != nil {
		n.stopPipeline()
		return p, err
	}
	if t.To <= head.Number {
		return p, nil
	}
	if pipe := n.pipelineFor(head, t); pipe != nil {
		return n.advancePipelined(pipe, head, t)
	}
	t0 := time.Now()
	raws, err := n.fetch(head.Number+1, int(min(t.To-head.Number, BatchMax)))
	n.stage.fetch.Add(uint64(time.Since(t0)))
	if err != nil {
		return p, err
	}
	raws = raws[:min(len(raws), BatchMax)]
	t1 := time.Now()
	part, linkErr := link(head, raws)
	blocks, hashes := part.blocks, part.hashes
	n.stage.verify.Add(uint64(time.Since(t1)))
	if len(blocks) == 0 {
		return p, linkErr
	}
	t2 := time.Now()
	admitted, err := n.admit(blocks, t.Tip, n.hashAt)
	n.stage.certify.Add(uint64(time.Since(t2)))
	n.publishCertStats()
	if err != nil {
		return p, n.latch(err)
	}
	admitted = min(admitted, len(blocks))
	t3 := time.Now()
	senders := recoverSenders(blocks[:admitted])
	n.stage.verify.Add(uint64(time.Since(t3)))
	for i := range admitted {
		data, err := n.execute(&rpc.BlockData{Hash: hashes[i], Block: &blocks[i], Raw: raws[i]}, senders[i], t)
		if err != nil {
			return p, n.latch(err)
		}
		p.Head, p.Executed = data.Number(), p.Executed+1
		p.Blocks = append(p.Blocks, data)
	}
	p.Waiting = admitted == 0
	if admitted == len(blocks) {
		return p, linkErr
	}
	return p, nil
}

func (n *Node) pipelineFor(head gosync.Link, t Target) *pipeline {
	if n.pipe != nil && n.pipe.next == head.Number+1 {
		return n.pipe
	}
	n.stopPipeline()
	if n.pipelineMin == 0 || t.Tip < deepMarginBlock {
		return nil
	}
	limit := min(t.To, t.Tip-deepMarginBlock)
	if limit < head.Number+n.pipelineMin {
		return nil
	}
	n.pipe = startPipeline(n, head, span{from: head.Number + 1, to: limit}, t.Tip)
	return n.pipe
}

func (n *Node) advancePipelined(pipe *pipeline, head gosync.Link, t Target) (Progress, error) {
	p := Progress{Head: head.Number}
	t0 := time.Now()
	b, ok := pipe.receive()
	n.stage.wait.Add(uint64(time.Since(t0)))
	if !ok {
		n.stopPipeline()
		return p, nil
	}
	if n.cert != nil {
		n.mu.Lock()
		n.certStats = b.cert
		n.mu.Unlock()
	}
	assert.Ok(len(b.datas) == 0 || b.from == head.Number+1, "pipeline delivers the block after the head")
	for i, data := range b.datas {
		if _, err := n.execute(data, b.senders[i], t); err != nil {
			n.stopPipeline()
			return p, n.latch(err)
		}
		p.Head, p.Executed = data.Number(), p.Executed+1
		p.Blocks = append(p.Blocks, data)
	}
	pipe.next = p.Head + 1
	if b.err == nil {
		return p, nil
	}
	n.stopPipeline()
	if b.fatal {
		return p, n.latch(b.err)
	}
	return p, b.err
}

func (n *Node) publishCertStats() {
	if n.cert == nil {
		return
	}
	s := n.cert.Stats()
	n.mu.Lock()
	n.certStats = s
	n.mu.Unlock()
}

func (n *Node) latch(err error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.latchLocked(err)
}

func (n *Node) latchLocked(err error) error {
	if n.failed == nil {
		n.failed = err
	}
	return n.failed
}

func link(parent gosync.Link, raws [][]byte) (chainPart, error) {
	part := chainPart{blocks: make([]types.Block, 0, len(raws)), hashes: make([]primitives.Hash, 0, len(raws))}
	for _, raw := range raws {
		split, err := gosync.SplitBlock(raw)
		if err != nil {
			return part, fmt.Errorf("%w: block %d: %w", ErrChainLink, parent.Number+1, err)
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

func (s Stats) Since(prev Stats) Stats {
	return Stats{
		Blocks: s.Blocks - prev.Blocks, Txs: s.Txs - prev.Txs, Gas: s.Gas - prev.Gas,
		ExecNanos: s.ExecNanos - prev.ExecNanos, CommitNanos: s.CommitNanos - prev.CommitNanos,
		FetchNanos: s.FetchNanos - prev.FetchNanos, VerifyNanos: s.VerifyNanos - prev.VerifyNanos,
		CertifyNanos: s.CertifyNanos - prev.CertifyNanos, WaitNanos: s.WaitNanos - prev.WaitNanos,
		FlushNanos: s.FlushNanos - prev.FlushNanos, RootChecks: s.RootChecks - prev.RootChecks,
		Certify: s.Certify,
	}
}

func (n *Node) admit(blocks []types.Block, tip uint64, hashAt func(uint64, *types.Block) primitives.Hash) (int, error) {
	if n.cert == nil {
		return len(blocks), nil
	}
	first := blocks[0].Header.Inner.Number
	if boundary, ok := n.cert.NeedsAnchor(first); ok {
		raws, err := n.fetchRange(boundary, 1)
		if err != nil {
			return 0, err
		}
		blk, err := types.DecodeBlock(raws[0])
		if err != nil {
			return 0, err
		}
		if blk.Hash() != hashAt(boundary, &blk) {
			return 0, &BlockError{Number: boundary, Field: "anchor hash", Got: blk.Hash().String(), Want: "executed chain"}
		}
		if err := n.cert.Anchor(boundary, &blk); err != nil {
			return 0, err
		}
	}
	depth := NearTip
	if tip >= blocks[len(blocks)-1].Header.Inner.Number+deepMarginBlock {
		depth = Buried
	}
	return n.cert.Admit(blocks, depth)
}

func (n *Node) hashAt(number uint64, blk *types.Block) primitives.Hash {
	n.mu.Lock()
	defer n.mu.Unlock()
	if h := n.getHash(number); h != (primitives.Hash{}) {
		return h
	}
	return blk.Hash()
}

func recoverSenders(blocks []types.Block) [][]executor.Sender {
	out := make([][]executor.Sender, len(blocks))
	workers := min(max(runtime.GOMAXPROCS(0)-1, 1), recoverWorkersMax)
	per := (len(blocks) + workers - 1) / workers
	var wg sync.WaitGroup
	for w := range workers {
		lo, hi := min(w*per, len(blocks)), min((w+1)*per, len(blocks))
		wg.Go(func() {
			for i := lo; i < hi; i++ {
				out[i] = blockSenders(&blocks[i])
			}
		})
	}
	wg.Wait()
	return out
}

func blockSenders(blk *types.Block) []executor.Sender {
	senders := make([]executor.Sender, len(blk.Transactions))
	for i := range blk.Transactions {
		if !executor.IsSystemTx(&blk.Transactions[i]) {
			senders[i] = executor.RecoverSender(&blk.Transactions[i])
		}
	}
	return senders
}

func (n *Node) execute(data *rpc.BlockData, senders []executor.Sender, t Target) (*rpc.BlockData, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	blk := data.Block
	h := &blk.Header.Inner
	st := n.db.State()
	t0 := time.Now()
	res, err := n.exec.Execute(st, executor.Input{Block: blk, GetHash: n.getHash, Senders: senders})
	n.stats.ExecNanos += uint64(time.Since(t0))
	if err != nil {
		return nil, n.discardLocked(&BlockError{Number: h.Number, Field: "execution", Got: err.Error(), Want: "ok"})
	}
	if e := compareBlock(h, &res); e != nil {
		return nil, n.discardLocked(e)
	}
	t1 := time.Now()
	err = n.commitLocked(h, t)
	n.stats.CommitNanos += uint64(time.Since(t1))
	if err != nil {
		return nil, err
	}
	if res.Receipts != nil && len(res.Receipts) != len(blk.Transactions) {
		return nil, rpc.ErrReceiptsCount
	}
	data.Receipts = res.Receipts
	n.hashes[h.Number%hashWindow] = data.Hash
	n.head, n.headHash, n.headRoot = h.Number, data.Hash, h.StateRoot
	n.stats.Blocks++
	n.stats.Txs += uint64(len(blk.Transactions))
	n.stats.Gas += res.GasUsed
	n.recent.Add(data)
	return data, nil
}

func (n *Node) rootDue(number uint64, t Target) bool {
	return n.rootEvery <= 1 || number%n.rootEvery == 0 || number == t.To || t.Tip < number+NearTipRoots
}

func (n *Node) commitLocked(h *types.EthHeader, t Target) error {
	st := n.db.State()
	if !n.rootDue(h.Number, t) {
		st.FinalizeTx()
		n.unchecked++
		return nil
	}
	if err := n.settleLocked(); err != nil {
		return err
	}
	check := rootCheck{from: h.Number - n.unchecked, number: h.Number, want: h.StateRoot}
	n.unchecked = 0
	n.stats.RootChecks++
	if n.rootEvery > 1 && h.Number != t.To && t.Tip >= h.Number+NearTipRoots {
		if err := st.CommitAsync(h.Number); err != nil {
			n.tainted = true
			return &BlockError{Number: h.Number, Field: "commit", Got: err.Error(), Want: "ok"}
		}
		n.pending = &check
		return nil
	}
	root, err := st.Commit(h.Number)
	return n.checkRootLocked(&check, root, err)
}

func (n *Node) settleLocked() error {
	check := n.pending
	if check == nil {
		return nil
	}
	n.pending = nil
	c, err := n.db.State().WaitCommit()
	return n.checkRootLocked(check, c.Root, err)
}

func (n *Node) checkRootLocked(check *rootCheck, root primitives.Hash, err error) error {
	if err != nil {
		n.tainted = true
		return &BlockError{Number: check.number, Field: "commit", Got: err.Error(), Want: "ok"}
	}
	if root == check.want {
		return nil
	}
	n.tainted = true
	field := "stateRoot"
	if check.from < check.number {
		field = fmt.Sprintf("stateRoot (one check over blocks %d..%d)", check.from, check.number)
	}
	return &BlockError{Number: check.number, Field: field, Got: root.String(), Want: check.want.String()}
}

func (n *Node) checkpointLocked() error {
	if err := n.settleLocked(); err != nil {
		return err
	}
	if n.unchecked == 0 || n.stale {
		return nil
	}
	check := rootCheck{from: n.head - n.unchecked + 1, number: n.head, want: n.headRoot}
	n.unchecked = 0
	n.stats.RootChecks++
	root, err := n.db.State().Commit(n.head)
	return n.checkRootLocked(&check, root, err)
}

func (n *Node) discardLocked(cause error) error {
	if err := n.settleLocked(); err != nil {
		return errors.Join(cause, err)
	}
	n.db.State().Discard()
	if n.unchecked > 0 {
		n.stale = true
		n.unchecked = 0
	}
	return cause
}

func compareBlock(h *types.EthHeader, res *executor.Result) *BlockError {
	if res.GasUsed != h.GasUsed {
		return &BlockError{Number: h.Number, Field: "gasUsed", Got: fmt.Sprint(res.GasUsed), Want: fmt.Sprint(h.GasUsed)}
	}
	if res.ReceiptsRoot != h.ReceiptsRoot {
		return &BlockError{Number: h.Number, Field: "receiptsRoot", Got: res.ReceiptsRoot.String(), Want: h.ReceiptsRoot.String()}
	}
	if res.Bloom != h.LogsBloom {
		return &BlockError{Number: h.Number, Field: "logsBloom", Got: "differs", Want: "header bloom"}
	}
	return nil
}
