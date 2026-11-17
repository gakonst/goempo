package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

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
)

const (
	BatchMax        = 512
	hashWindow      = 256
	healedFileName  = "healed.json"
	fetchAttempts   = BatchMax
	deepMarginBlock = BatchMax
)

const _ = uint(512 - BatchMax)

var (
	ErrNoSource   = errors.New("node: no block source")
	ErrNoBlock    = errors.New("node: no source holds the block")
	ErrResumeRoot = errors.New("node: head block's state root differs from the database root")
	ErrHealed     = errors.New("node: datadir was healed past a mismatch; its state is not trusted")
	ErrNoState    = errors.New("node: state is kept at the head only")
	ErrChainLink  = errors.New("node: block does not link to its parent")
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
	State        goio.FS
	StateOptions state.Options
	Sources      []BlockSource
	Archive      *Archive
	Finality     FinalitySource
	CertifyFrom  uint64
	RecentBlocks int
}

type Stats struct {
	Blocks  uint64
	Txs     uint64
	Gas     uint64
	Certify CertifyStats
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
	stats    Stats
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
		db:      db,
		exec:    &executor.Executor{ChainID: hardfork.MainnetChainID, EpochLength: extra.EpochLength},
		sources: cfg.Sources,
		recent:  NewRecent(recent, cfg.Archive),
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
	n.head, n.headHash, n.start = head.Number, parent, head.Number
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
	if n.cert != nil {
		s.Certify = n.cert.Stats()
	}
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
	return n.db.Flush()
}

func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.db.Close()
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
		return p, err
	}
	if t.To <= head.Number {
		return p, nil
	}
	raws, err := n.fetch(head.Number+1, int(min(t.To-head.Number, BatchMax)))
	if err != nil {
		return p, err
	}
	raws = raws[:min(len(raws), BatchMax)]
	blocks, linkErr := link(head, raws)
	if len(blocks) == 0 {
		return p, linkErr
	}
	admitted, err := n.admit(blocks, t.Tip)
	if err != nil {
		return p, n.latch(err)
	}
	admitted = min(admitted, len(blocks))
	for i := range admitted {
		data, err := n.execute(&blocks[i], raws[i])
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

func (n *Node) latch(err error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.failed == nil {
		n.failed = err
	}
	return n.failed
}

func link(parent gosync.Link, raws [][]byte) ([]types.Block, error) {
	blocks := make([]types.Block, 0, len(raws))
	for _, raw := range raws {
		split, err := gosync.SplitBlock(raw)
		if err != nil {
			return blocks, fmt.Errorf("%w: block %d: %w", ErrChainLink, parent.Number+1, err)
		}
		v, err := gosync.Verify(parent, split)
		if err != nil {
			return blocks, fmt.Errorf("%w: block %d: %w", ErrChainLink, parent.Number+1, err)
		}
		blocks = append(blocks, v.Block)
		parent = v.Link
	}
	return blocks, nil
}

func (n *Node) admit(blocks []types.Block, tip uint64) (int, error) {
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
		if blk.Hash() != n.hashAt(boundary, &blk) {
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

func (n *Node) execute(blk *types.Block, raw []byte) (*rpc.BlockData, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	h := &blk.Header.Inner
	st := n.db.State()
	res, err := n.exec.Execute(st, executor.Input{Block: blk, GetHash: n.getHash})
	if err != nil {
		st.Discard()
		return nil, &BlockError{Number: h.Number, Field: "execution", Got: err.Error(), Want: "ok"}
	}
	if e := compareBlock(h, &res); e != nil {
		st.Discard()
		return nil, e
	}
	root, err := st.Commit(h.Number)
	if err != nil {
		return nil, &BlockError{Number: h.Number, Field: "commit", Got: err.Error(), Want: "ok"}
	}
	if root != h.StateRoot {
		return nil, &BlockError{Number: h.Number, Field: "stateRoot", Got: root.String(), Want: h.StateRoot.String()}
	}
	data, err := rpc.NewBlockData(blk, raw, res.Receipts)
	if err != nil {
		return nil, err
	}
	n.hashes[h.Number%hashWindow] = data.Hash
	n.head, n.headHash = h.Number, data.Hash
	n.stats.Blocks++
	n.stats.Txs += uint64(len(blk.Transactions))
	n.stats.Gas += res.GasUsed
	n.recent.Add(data)
	return data, nil
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
