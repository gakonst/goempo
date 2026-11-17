package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gakonst/goempo/internal/builder"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/txpool"
	"github.com/gakonst/goempo/internal/types"
)

const (
	chunkBlocks       = 10_000
	chunkLengthPrefix = 4
	chunkBytesMax     = 4 << 30
	hashWindow        = 256
	millisPerSecond   = 1000
	stateCacheShift   = 20
	blocksMax         = 1 << 24
	_                 = uint(chunkBytesMax - blocksMax)
	_                 = uint(blocksMax - chunkBlocks)
)

var (
	errChunk   = errors.New("buildcheck: malformed chunk")
	errMissing = errors.New("buildcheck: block missing from the chunks")
)

type options struct {
	datadir string
	chunks  string
	to      uint64
	out     string
}

type report struct {
	From        uint64   `json:"from"`
	To          uint64   `json:"to"`
	Checked     int      `json:"checked"`
	Identical   int      `json:"identical"`
	Txs         int      `json:"txs"`
	Rejected    int      `json:"rejected"`
	Reordered   int      `json:"reordered"`
	Mismatches  []string `json:"mismatches"`
	Reorders    []string `json:"reorders"`
	Rejections  []string `json:"rejections"`
	ExecutedAll bool     `json:"executed_all"`
}

type checker struct {
	db     *state.DB
	x      *executor.Executor
	opts   options
	cache  map[uint64][][]byte
	hashes map[uint64]primitives.Hash
	head   *types.Block
	rep    report
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("goempo-buildcheck", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.datadir, "datadir", "", "state datadir positioned at the parent of the first block (it is advanced)")
	fs.StringVar(&o.chunks, "chunks", "", "directory of block chunk files written by goempo-dev fetch-range")
	fs.Uint64Var(&o.to, "to", 0, "last block to rebuild")
	fs.StringVar(&o.out, "result", "", "where the JSON report is written (empty: stdout only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, err := open(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "buildcheck:", err)
		return 1
	}
	err = c.runAll()
	closeErr := c.db.Close()
	if err = errors.Join(err, closeErr); err != nil {
		fmt.Fprintln(os.Stderr, "buildcheck:", err)
		return 1
	}
	data, err := json.MarshalIndent(&c.rep, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "buildcheck:", err)
		return 1
	}
	fmt.Println(string(data))
	if o.out != "" {
		if err := os.WriteFile(o.out, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "buildcheck:", err)
			return 1
		}
	}
	if len(c.rep.Mismatches) != 0 || !c.rep.ExecutedAll {
		return 1
	}
	return 0
}

func open(o options) (*checker, error) {
	dir, err := goio.NewDirFS(o.datadir)
	if err != nil {
		return nil, err
	}
	db, err := state.Open(dir, state.Options{MemtableBytes: 256 << 20, CacheSlots: 1 << (stateCacheShift + 2), CacheAccounts: 1 << stateCacheShift})
	if err != nil {
		return nil, err
	}
	g, err := genesis.Mainnet()
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	var extra struct {
		EpochLength uint64 `json:"epochLength"`
	}
	if err := json.Unmarshal(g.Config.Raw, &extra); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	x := &executor.Executor{ChainID: hardfork.MainnetChainID, EpochLength: extra.EpochLength, Configs: chain.NewCache()}
	c := &checker{db: db, x: x, opts: o, cache: map[uint64][][]byte{}, hashes: map[uint64]primitives.Hash{}}
	if err := c.loadHashes(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return c, nil
}

func (c *checker) block(n uint64) (*types.Block, error) {
	start := n / chunkBlocks * chunkBlocks
	raws, ok := c.cache[start]
	if !ok {
		var err error
		if raws, err = readChunk(c.opts.chunks, start); err != nil {
			return nil, err
		}
		clear(c.cache)
		c.cache[start] = raws
	}
	i := int(n - start)
	if i >= len(raws) {
		return nil, fmt.Errorf("%w: %d", errMissing, n)
	}
	blk, err := types.DecodeBlock(raws[i])
	if err != nil {
		return nil, err
	}
	if blk.Header.Inner.Number != n {
		return nil, fmt.Errorf("%w: chunk slot %d holds %d", errChunk, n, blk.Header.Inner.Number)
	}
	return &blk, nil
}

func (c *checker) loadHashes() error {
	head := c.db.Head().Number
	first := head - min(head, hashWindow-1)
	count := min(head-first+1, hashWindow)
	for i := uint64(0); i < count; i++ {
		n := first + i
		blk, err := c.block(n)
		if err != nil {
			return err
		}
		c.hashes[n] = blk.Hash()
		c.head = blk
	}
	if c.head.Header.Inner.StateRoot != c.db.Head().Root {
		return fmt.Errorf("datadir root %s is not block %d's state root", c.db.Head().Root, head)
	}
	return nil
}

func (c *checker) getHash(n uint64) primitives.Hash {
	return c.hashes[n]
}

func (c *checker) runAll() error {
	from := c.head.Header.Inner.Number + 1
	c.rep.From = from
	if c.opts.to < from || c.opts.to-from >= blocksMax {
		return fmt.Errorf("-to %d must be in [%d, %d)", c.opts.to, from, from+blocksMax)
	}
	count := min(c.opts.to-from+1, blocksMax)
	for i := uint64(0); i < count; i++ {
		n := from + i
		blk, err := c.block(n)
		if err != nil {
			return err
		}
		if len(blk.Transactions) != 0 {
			c.check(blk)
		}
		if err := c.execute(blk); err != nil {
			return err
		}
		c.rep.To = n
	}
	c.rep.ExecutedAll = c.rep.To == c.opts.to
	return nil
}

func (c *checker) check(blk *types.Block) {
	h := &blk.Header
	now := h.Inner.Timestamp
	pool := txpool.New(txpool.Config{Executor: c.x, Now: func() uint64 { return now }})
	raws := blk.TransactionEnvelopes()
	st := c.db.State()
	for i := range raws {
		c.rep.Txs++
		if _, err := pool.Add(raws[i], st, &c.head.Header); err != nil {
			c.rep.Rejected++
			c.rep.Rejections = append(c.rep.Rejections, fmt.Sprintf("block %d tx %d: %v", h.Inner.Number, i, err))
		}
	}
	attrs := builder.Attributes{
		TimestampMillis: h.Inner.Timestamp*millisPerSecond + h.TimestampMillisPart,
		ExtraData:       h.Inner.ExtraData,
		Beneficiary:     h.Inner.Beneficiary,
		PrevRandao:      h.Inner.MixHash,
		Consensus:       h.Consensus,
		GasLimit:        h.Inner.GasLimit,
		GetHash:         c.getHash,
	}
	built, err := builder.New(pool).Build(st, c.x, c.head, attrs)
	st.Discard()
	c.rep.Checked++
	if err != nil {
		c.rep.Mismatches = append(c.rep.Mismatches, fmt.Sprintf("block %d: build: %v", h.Inner.Number, err))
		return
	}
	if built.Block.Hash() == blk.Hash() {
		c.rep.Identical++
		return
	}
	p := pair{got: &built.Block, want: blk}
	if p.sameTxSet() && p.sameOutcome() {
		c.rep.Reordered++
		c.rep.Reorders = append(c.rep.Reorders, fmt.Sprintf("block %d: %s", h.Inner.Number, p.diff()))
		return
	}
	c.rep.Mismatches = append(c.rep.Mismatches, fmt.Sprintf("block %d: %s", h.Inner.Number, p.diff()))
}

type pair struct {
	got  *types.Block
	want *types.Block
}

func (p pair) sameTxSet() bool {
	a, b := p.got, p.want
	if len(a.Transactions) != len(b.Transactions) {
		return false
	}
	seen := map[primitives.Hash]bool{}
	for i := range a.Transactions {
		seen[a.Transactions[i].Hash()] = true
	}
	for i := range b.Transactions {
		if !seen[b.Transactions[i].Hash()] {
			return false
		}
	}
	return true
}

func (p pair) sameOutcome() bool {
	g, w := &p.got.Header.Inner, &p.want.Header.Inner
	return g.GasUsed == w.GasUsed && g.StateRoot == w.StateRoot && g.LogsBloom == w.LogsBloom
}

func (p pair) diff() string {
	got, want := p.got, p.want
	g, w := &got.Header, &want.Header
	var out bytes.Buffer
	field := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			fmt.Fprintf(&out, "%s got %v want %v; ", name, a, b)
		}
	}
	field("txs", len(got.Transactions), len(want.Transactions))
	field("gasUsed", g.Inner.GasUsed, w.Inner.GasUsed)
	field("gasLimit", g.Inner.GasLimit, w.Inner.GasLimit)
	field("baseFee", g.Inner.BaseFeePerGas, w.Inner.BaseFeePerGas)
	field("stateRoot", g.Inner.StateRoot, w.Inner.StateRoot)
	field("txRoot", g.Inner.TransactionsRoot, w.Inner.TransactionsRoot)
	field("receiptsRoot", g.Inner.ReceiptsRoot, w.Inner.ReceiptsRoot)
	field("bloom", g.Inner.LogsBloom, w.Inner.LogsBloom)
	field("ommers", g.Inner.OmmersHash, w.Inner.OmmersHash)
	field("withdrawalsRoot", g.Inner.WithdrawalsRoot, w.Inner.WithdrawalsRoot)
	field("blobGasUsed", g.Inner.BlobGasUsed, w.Inner.BlobGasUsed)
	field("excessBlobGas", g.Inner.ExcessBlobGas, w.Inner.ExcessBlobGas)
	field("beaconRoot", g.Inner.ParentBeaconBlockRoot, w.Inner.ParentBeaconBlockRoot)
	field("requestsHash", g.Inner.RequestsHash, w.Inner.RequestsHash)
	field("optionalFields", g.Inner.OptionalFields, w.Inner.OptionalFields)
	field("difficulty", g.Inner.Difficulty, w.Inner.Difficulty)
	field("nonce", g.Inner.Nonce, w.Inner.Nonce)
	field("generalGasLimit", g.GeneralGasLimit, w.GeneralGasLimit)
	field("sharedGasLimit", g.SharedGasLimit, w.SharedGasLimit)
	field("millis", g.TimestampMillisPart, w.TimestampMillisPart)
	if out.Len() == 0 {
		fmt.Fprintf(&out, "header bytes differ: got %x want %x", g.Encode(), w.Encode())
	}
	return out.String()
}

func (c *checker) execute(blk *types.Block) error {
	h := &blk.Header.Inner
	res, err := c.x.Execute(c.db.State(), executor.Input{Block: blk, GetHash: c.getHash})
	if err != nil {
		return fmt.Errorf("execute %d: %w", h.Number, err)
	}
	if res.GasUsed != h.GasUsed || res.ReceiptsRoot != h.ReceiptsRoot {
		return fmt.Errorf("execute %d: gas or receipts differ from the header", h.Number)
	}
	root, err := c.db.State().Commit(h.Number)
	if err != nil {
		return err
	}
	if root != h.StateRoot {
		return fmt.Errorf("execute %d: state root %s want %s", h.Number, root, h.StateRoot)
	}
	c.hashes[h.Number] = blk.Hash()
	delete(c.hashes, h.Number-min(h.Number, hashWindow))
	c.head = blk
	return nil
}

func readChunk(dir string, start uint64) ([][]byte, error) {
	f, err := os.Open(filepath.Join(dir, fmt.Sprintf("%010d.blkz", start)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr := flate.NewReader(f)
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, chunkBytesMax))
	if err != nil {
		return nil, err
	}
	blocks := make([][]byte, 0, chunkBlocks)
	for i := 0; i < chunkBlocks && len(data) > 0; i++ {
		if len(data) < chunkLengthPrefix {
			return nil, errChunk
		}
		n := int(binary.BigEndian.Uint32(data))
		data = data[chunkLengthPrefix:]
		if n > len(data) {
			return nil, errChunk
		}
		blocks = append(blocks, data[:n])
		data = data[n:]
	}
	if len(data) != 0 {
		return nil, errChunk
	}
	return blocks, nil
}
