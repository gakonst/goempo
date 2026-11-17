package main

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"

	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	chunkBlocks       = 10_000
	chunkBlockBytes   = 1 << 22
	chunkBytesMax     = 1 << 36
	chunkLengthPrefix = 4
	chunkRequestsMax  = 20
	chunkSuffix       = ".blkz"
	chunkRawSuffix    = ".blk"
	chunksMax         = 1 << 20
	shardsMax         = 256
)

const _ = uint(chunkBytesMax - chunkBlocks*chunkBlockBytes)

const _ = uint(chunkBlocks - chunkRequestsMax)

const _ = uint(rpcBatchMax - chunkRequestsMax)

const _ = uint(chunkBlocks % chunkRequestsMax)

const _ = uint(chunksMax - shardsMax)

var (
	errChunk      = errors.New("chunk file is malformed")
	errChunkRange = errors.New("chunk range is too large")
	errShard      = errors.New("shard out of range")
)

type rangeOptions struct {
	dir    string
	from   uint64
	to     uint64
	shard  int
	shards int
}

func defaultChunkDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("cache", "mainnet")
	}
	return filepath.Join(home, "cache", "mainnet")
}

func runFetchRange(args []string) int {
	fs := flag.NewFlagSet("fetch-range", flag.ContinueOnError)
	rpcURL := fs.String("rpc", mainnetRPC, "JSON-RPC endpoint")
	dir := fs.String("dir", defaultChunkDir(), "directory of block chunk files")
	from := fs.Uint64("from", 0, "first block")
	to := fs.Uint64("to", chunkBlocks-1, "last block")
	shard := fs.Int("shard", 0, "this process's shard of the chunks")
	shards := fs.Int("shards", 1, "number of shard processes run in parallel")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := rangeOptions{dir: *dir, from: *from, to: *to, shard: *shard, shards: *shards}
	if err := fetchRange(newRPCClient(*rpcURL), opts); err != nil {
		fmt.Fprintln(os.Stderr, "fetch-range:", err)
		return 1
	}
	return 0
}

func chunkPath(dir string, start uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%010d%s", start, chunkSuffix))
}

func chunkRawPath(dir string, start uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%010d%s", start, chunkRawSuffix))
}

func chunkStart(number uint64) uint64 {
	return number / chunkBlocks * chunkBlocks
}

func chunkPresent(dir string, start uint64) bool {
	if _, err := os.Stat(chunkPath(dir, start)); err == nil {
		return true
	}
	_, err := os.Stat(chunkRawPath(dir, start))
	return err == nil
}

func fetchRange(c *rpcClient, opts rangeOptions) error {
	if opts.shards < 1 || opts.shards > shardsMax || opts.shard < 0 || opts.shard >= opts.shards {
		return errShard
	}
	if err := os.MkdirAll(opts.dir, dirMode); err != nil {
		return err
	}
	first := opts.from / chunkBlocks
	last := opts.to / chunkBlocks
	if last < first || last-first >= chunksMax {
		return errChunkRange
	}
	count := min(last-first+1, chunksMax)
	for i := uint64(0); i < count; i++ {
		idx := first + i
		if int(idx%uint64(opts.shards)) != opts.shard {
			continue
		}
		start := idx * chunkBlocks
		if chunkPresent(opts.dir, start) {
			continue
		}
		if err := fetchChunk(c, opts.dir, start); err != nil {
			return fmt.Errorf("chunk %d: %w", start, err)
		}
		fmt.Fprintf(os.Stderr, "fetch-range: chunk %d done\n", start)
	}
	return nil
}

func fetchChunk(c *rpcClient, dir string, start uint64) error {
	var plain bytes.Buffer
	for offset := uint64(0); offset < chunkBlocks; offset += chunkRequestsMax {
		reqs := make([]rpcRequest, chunkRequestsMax)
		for i := range reqs {
			reqs[i] = rpcRequest{JSONRPC: jsonRPCVersion, ID: i, Method: "debug_getRawBlock", Params: []any{blockTag(start + offset + uint64(i))}}
		}
		results, err := c.batch(reqs)
		if err != nil {
			return err
		}
		if err := appendRawBlocks(&plain, results); err != nil {
			return err
		}
	}
	var packed bytes.Buffer
	zw, err := flate.NewWriter(&packed, flate.BestSpeed)
	if err != nil {
		return err
	}
	if _, err := zw.Write(plain.Bytes()); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	tmp := chunkPath(dir, start) + ".tmp"
	if err := os.WriteFile(tmp, packed.Bytes(), fileMode); err != nil {
		return err
	}
	return os.Rename(tmp, chunkPath(dir, start))
}

func appendRawBlocks(out *bytes.Buffer, results []json.RawMessage) error {
	for _, res := range results {
		var s string
		if err := json.Unmarshal(res, &s); err != nil {
			return err
		}
		raw, err := decodeHex(s)
		if err != nil {
			return err
		}
		if len(raw) == 0 || len(raw) > chunkBlockBytes {
			return errChunk
		}
		var prefix [chunkLengthPrefix]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(raw)))
		out.Write(prefix[:])
		out.Write(raw)
	}
	return nil
}

// chunkArenas is how many chunks' buffers replay keeps: one executing, one
// prefetching and one spare, so a prefetch never waits on the executor.
const chunkArenas = 3

// chunkArena holds one chunk's file bytes, its decompressed blocks and their
// decoded form. Decoded blocks alias plain and the decoders' slabs, so an
// arena returns to its pool only once replay is done with every block in it.
type chunkArena struct {
	packed   []byte
	plain    []byte
	raws     [][]byte
	blocks   []decodedBlock
	decoders []types.BlockDecoder
	senders  [][]executor.Sender
	inflate  io.ReadCloser
	packedRd bytes.Reader
}

// chunkPool hands out the arenas replay allocates at startup.
type chunkPool struct {
	free chan *chunkArena
}

func newChunkPool() *chunkPool {
	p := &chunkPool{free: make(chan *chunkArena, chunkArenas)}
	workers := decodeWorkers()
	for i := 0; i < chunkArenas; i++ {
		a := &chunkArena{
			raws:     make([][]byte, 0, chunkBlocks),
			blocks:   make([]decodedBlock, 0, chunkBlocks),
			decoders: make([]types.BlockDecoder, workers),
			senders:  make([][]executor.Sender, workers),
		}
		a.inflate = flate.NewReader(&a.packedRd)
		p.free <- a
	}
	return p
}

func (p *chunkPool) get(ctx context.Context) (*chunkArena, error) {
	select {
	case a := <-p.free:
		return a, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *chunkPool) put(a *chunkArena) {
	assert.Ok(a != nil, "only arenas return to the pool")
	clear(a.blocks)
	a.blocks = a.blocks[:0]
	a.raws = a.raws[:0]
	select {
	case p.free <- a:
	default:
		assert.Fail("chunk pool returned more arenas than it holds")
	}
}

func decodeWorkers() int {
	return min(max(runtime.GOMAXPROCS(0)-1, 1), decodeWorkersMax)
}

// readPlain reads the chunk's decompressed bytes into a.plain, reusing the
// arena's file buffer and inflater.
func (a *chunkArena) readPlain(dir string, start uint64) error {
	packed, err := readFileInto(a.packed[:0], chunkPath(dir, start))
	if errors.Is(err, os.ErrNotExist) {
		a.plain, err = readFileInto(a.plain[:0], chunkRawPath(dir, start))
		return err
	}
	if err != nil {
		return err
	}
	a.packed = packed
	a.packedRd.Reset(a.packed)
	resetter, ok := a.inflate.(flate.Resetter)
	assert.Ok(ok, "flate readers reset")
	if err := resetter.Reset(&a.packedRd, nil); err != nil {
		return err
	}
	a.plain, err = readAllInto(a.plain[:0], a.inflate)
	return err
}

func readFileInto(buf []byte, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return buf, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return buf, err
	}
	if info.Size() > chunkBytesMax {
		return buf, errChunk
	}
	// Headroom past the file's size lets the next, usually similar, chunk
	// reuse the buffer.
	if need := int(info.Size()) + 1; cap(buf) < need {
		buf = make([]byte, 0, need+need/4)
	}
	return readAllInto(buf, f)
}

// readAllInto appends everything r yields to buf, growing it only when it is
// full, and fails past chunkBytesMax.
func readAllInto(buf []byte, r io.Reader) ([]byte, error) {
	const readMin = 1 << 16
	for range chunkBytesMax / readMin {
		if cap(buf)-len(buf) < readMin {
			buf = slices.Grow(buf, max(readMin, cap(buf)))
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
		if len(buf) > chunkBytesMax {
			return buf, errChunk
		}
	}
	return buf, errChunk
}

type chunkRead struct {
	arena  *chunkArena
	blocks []decodedBlock
	err    error
}

type decodedBlock struct {
	blk     types.Block
	senders []executor.Sender
	err     error
}

type chunkPrefetch struct {
	wg  sync.WaitGroup
	out chan chunkRead
}

func prefetchChunk(ctx context.Context, pool *chunkPool, dir string, start uint64) *chunkPrefetch {
	p := &chunkPrefetch{out: make(chan chunkRead, 1)}
	p.wg.Go(func() {
		got := chunkRead{}
		a, err := pool.get(ctx)
		if err != nil {
			return
		}
		got.arena = a
		if got.err = a.readChunk(dir, start); got.err == nil {
			a.decodeChunk()
			got.blocks = a.blocks
		}
		select {
		case p.out <- got:
		case <-ctx.Done():
			pool.put(a)
		}
	})
	return p
}

func (p *chunkPrefetch) take(ctx context.Context) chunkRead {
	select {
	case got := <-p.out:
		return got
	case <-ctx.Done():
		return chunkRead{err: ctx.Err()}
	}
}

func (p *chunkPrefetch) Wait() {
	p.wg.Wait()
}

const decodeWorkersMax = 64

// sendersSlabMin is the smallest sender slab a decode worker allocates.
const sendersSlabMin = 1 << 10

const _ = uint(chunkBlocks - decodeWorkersMax)

// decodeChunk decodes a.raws into a.blocks in place, one decoder and sender
// slab per worker.
func (a *chunkArena) decodeChunk() {
	n := len(a.raws)
	a.blocks = a.blocks[:n]
	workers := len(a.decoders)
	assert.Ok(workers >= 1, "a chunk arena has decoders")
	per := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		part := a.blocks[min(w*per, n):min((w+1)*per, n)]
		in := a.raws[min(w*per, n):min((w+1)*per, n)]
		d := &a.decoders[w]
		senders := &a.senders[w]
		wg.Go(func() {
			d.Reset()
			*senders = (*senders)[:0]
			for i := range in {
				part[i] = decodeBlock(d, senders, in[i])
			}
		})
	}
	wg.Wait()
}

func decodeBlock(d *types.BlockDecoder, slab *[]executor.Sender, raw []byte) decodedBlock {
	blk, err := d.DecodeBlock(raw)
	if err != nil {
		return decodedBlock{err: err}
	}
	n := len(blk.Transactions)
	if cap(*slab)-len(*slab) < n {
		*slab = make([]executor.Sender, 0, max(2*cap(*slab), n, sendersSlabMin))
	}
	senders := (*slab)[len(*slab) : len(*slab)+n : len(*slab)+n]
	*slab = (*slab)[:len(*slab)+n]
	for i := range blk.Transactions {
		senders[i] = executor.Sender{}
		if !executor.IsSystemTx(&blk.Transactions[i]) {
			senders[i] = executor.RecoverSender(&blk.Transactions[i])
		}
	}
	return decodedBlock{blk: blk, senders: senders}
}

func (a *chunkArena) readChunk(dir string, start uint64) error {
	if err := a.readPlain(dir, start); err != nil {
		return err
	}
	data := a.plain
	a.raws = a.raws[:0]
	for i := 0; i < chunkBlocks && len(data) > 0; i++ {
		if len(data) < chunkLengthPrefix {
			return errChunk
		}
		n := int(binary.BigEndian.Uint32(data))
		data = data[chunkLengthPrefix:]
		if n > len(data) {
			return errChunk
		}
		a.raws = append(a.raws, data[:n:n])
		data = data[n:]
	}
	if len(data) != 0 {
		return errChunk
	}
	return nil
}

// readChunk reads one chunk's raw blocks into fresh buffers the caller owns,
// for tools that keep them; replay reuses pooled arenas instead.
func readChunk(dir string, start uint64) ([][]byte, error) {
	a := &chunkArena{}
	a.inflate = flate.NewReader(&a.packedRd)
	defer a.inflate.Close()
	if err := a.readChunk(dir, start); err != nil {
		return nil, err
	}
	return a.raws, nil
}
