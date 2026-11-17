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
)

const (
	chunkBlocks       = 10_000
	chunkBlockBytes   = 1 << 22
	chunkBytesMax     = 1 << 36
	chunkLengthPrefix = 4
	chunkRequestsMax  = rpcBatchMax
	chunkSuffix       = ".blkz"
	chunkRawSuffix    = ".blk"
	chunksMax         = 1 << 20
	shardsMax         = 256
)

const _ = uint(chunkBytesMax - chunkBlocks*chunkBlockBytes)

const _ = uint(chunkBlocks - chunkRequestsMax)

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

func readChunkBytes(dir string, start uint64) ([]byte, error) {
	f, err := os.Open(chunkPath(dir, start))
	if errors.Is(err, os.ErrNotExist) {
		return os.ReadFile(chunkRawPath(dir, start))
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr := flate.NewReader(f)
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, chunkBytesMax))
}

func readChunk(dir string, start uint64) ([][]byte, error) {
	data, err := readChunkBytes(dir, start)
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
