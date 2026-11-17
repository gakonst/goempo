package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultPerFork   = 24
	defaultBusy      = 12
	defaultProbe     = 400
	sampleMax        = 4096
	fetchBlocksMax   = 1 << 16
	blocksPerBatch   = 16
	requestsPerBlock = 3
	dirMode          = 0o755
	fileMode         = 0o644
	samplesFileName  = "samples.json"
)

const _ = uint(rpcBatchMax - requestsPerBlock*blocksPerBatch)

const _ = uint(fetchBlocksMax - sampleMax)

var errTooMany = errors.New("too many blocks requested")

type fetchOptions struct {
	dir     string
	perFork int
	busy    busyOptions
}

type sampleSet struct {
	RPC     string       `json:"rpc"`
	Windows []forkWindow `json:"windows"`
	Blocks  []uint64     `json:"blocks"`
}

func defaultBlockDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("cache", "blocks")
	}
	return filepath.Join(home, "cache", "blocks")
}

func runFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	rpcURL := fs.String("rpc", mainnetRPC, "JSON-RPC endpoint")
	dir := fs.String("dir", defaultBlockDir(), "cache directory for blocks")
	perFork := fs.Int("per-fork", defaultPerFork, "blocks sampled per fork window")
	busy := fs.Int("busy", defaultBusy, "busiest blocks added per fork window")
	probe := fs.Int("probe", defaultProbe, "blocks probed per fork window to find the busiest")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := fetchOptions{
		dir:     *dir,
		perFork: min(*perFork, sampleMax),
		busy:    busyOptions{busy: min(*busy, sampleMax), probe: min(*probe, sampleMax)},
	}
	if err := fetch(newRPCClient(*rpcURL), opts, fs.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		return 1
	}
	return 0
}

func fetch(c *rpcClient, opts fetchOptions, explicit []string) error {
	if err := os.MkdirAll(opts.dir, dirMode); err != nil {
		return err
	}
	numbers, err := plannedBlocks(c, opts, explicit)
	if err != nil {
		return err
	}
	cached, err := loadCache(opts.dir)
	if err != nil {
		return err
	}
	missing := missingBlocks(cached, numbers)
	if len(missing) > fetchBlocksMax {
		return fmt.Errorf("%w: %d > %d", errTooMany, len(missing), fetchBlocksMax)
	}
	fmt.Fprintf(os.Stderr, "fetch: %d blocks planned, %d to download\n", len(numbers), len(missing))
	for start := 0; start < len(missing); start += blocksPerBatch {
		end := min(start+blocksPerBatch, len(missing))
		if err := fetchBatch(c, opts.dir, missing[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func plannedBlocks(c *rpcClient, opts fetchOptions, explicit []string) ([]uint64, error) {
	if len(explicit) > fetchBlocksMax {
		return nil, errTooMany
	}
	numbers, err := explicitNumbers(explicit)
	if err != nil {
		return nil, err
	}
	if len(numbers) > 0 {
		return numbers, nil
	}
	set, err := planSamples(c, opts)
	return set.Blocks, err
}

func explicitNumbers(args []string) ([]uint64, error) {
	out := make([]uint64, 0, len(args))
	for _, a := range args {
		n, err := parseBlockArg(a)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func parseBlockArg(a string) (uint64, error) {
	if strings.HasPrefix(a, "0x") {
		return parseQuantity(a)
	}
	var n uint64
	_, err := fmt.Sscanf(a, "%d", &n)
	return n, err
}

func planSamples(c *rpcClient, opts fetchOptions) (sampleSet, error) {
	windows, err := c.forkWindows()
	if err != nil {
		return sampleSet{}, err
	}
	set := sampleSet{RPC: c.url, Windows: windows}
	for _, w := range windows {
		set.Blocks = append(set.Blocks, sampleWindow(w, opts.perFork)...)
		busy, err := c.busiestBlocks(w, opts.busy)
		if err != nil {
			return set, err
		}
		set.Blocks = append(set.Blocks, busy...)
	}
	raw, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return set, err
	}
	return set, os.WriteFile(filepath.Join(opts.dir, samplesFileName), raw, fileMode)
}

func missingBlocks(cached []cachedBlock, numbers []uint64) []uint64 {
	seen := make(map[uint64]bool, len(cached)+len(numbers))
	for i := range cached {
		seen[cached[i].Number] = true
	}
	var out []uint64
	for _, n := range numbers {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func fetchBatch(c *rpcClient, dir string, numbers []uint64) error {
	reqs := make([]rpcRequest, 0, requestsPerBlock*len(numbers))
	for _, n := range numbers {
		reqs = append(reqs,
			rpcRequest{JSONRPC: jsonRPCVersion, ID: len(reqs), Method: "debug_getRawBlock", Params: []any{blockTag(n)}},
			rpcRequest{JSONRPC: jsonRPCVersion, ID: len(reqs) + 1, Method: "eth_getBlockByNumber", Params: []any{blockTag(n), true}},
			rpcRequest{JSONRPC: jsonRPCVersion, ID: len(reqs) + 2, Method: "eth_getBlockReceipts", Params: []any{blockTag(n)}})
	}
	results, err := c.batch(reqs)
	if err != nil {
		return err
	}
	blocks := make([]cachedBlock, len(numbers))
	for i, n := range numbers {
		got := results[requestsPerBlock*i : requestsPerBlock*(i+1)]
		blocks[i] = cachedBlock{Number: n, Block: got[1], Receipts: got[2]}
		if err := json.Unmarshal(got[0], &blocks[i].Raw); err != nil {
			return fmt.Errorf("block %d raw: %w", n, err)
		}
	}
	return appendCache(dir, blocks)
}
