package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gakonst/goempo/internal/genesis"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

const (
	hashWindow       = 256
	progressInterval = 50_000
	replayBlocksMax  = 1 << 40
)

const _ = uint(replayBlocksMax - chunkBlocks)

var errCacheGap = errors.New("block cache has a gap")

type replayResult struct {
	Rung      string      `json:"rung"`
	Chain     string      `json:"chain"`
	Height    uint64      `json:"height"`
	Target    uint64      `json:"target"`
	Mismatch  string      `json:"mismatch,omitempty"`
	Seconds   float64     `json:"seconds"`
	BlocksSec float64     `json:"blocks_per_sec"`
	Bench     *benchStats `json:"bench,omitempty"`
}

type mismatch struct {
	number uint64
	field  string
	got    string
	want   string
}

func (m *mismatch) String() string {
	return fmt.Sprintf("block %d: %s: got %s, want %s", m.number, m.field, m.got, m.want)
}

type replayer struct {
	db          *state.DB
	exec        *executor.Executor
	hashes      hashRing
	head        uint64
	dir         string
	failed      *mismatch
	failedBlock *types.Block
	result      *executor.Result

	heal      *healer
	firstHead uint64

	store      storeConfig
	ctx        context.Context
	startHead  uint64
	healedFrom *mismatch
	healedHead uint64
	stopped    bool
	counters   benchCounters
}

func defaultResultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "r3-mainnet.json"
	}
	return filepath.Join(home, "cache", "r3-mainnet.json")
}

func runReplay(args []string) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	dir := fs.String("dir", defaultChunkDir(), "directory of block chunk files (fetch-range)")
	to := fs.Uint64("to", replayBlocksMax, "last block to execute")
	rpcURL := fs.String("rpc", mainnetRPC, "JSON-RPC endpoint used to diagnose a mismatch")
	out := fs.String("result", defaultResultPath(), "where the R3 result is written")
	diagnose := fs.String("diagnose", "on", "on: fetch traces to diagnose the first mismatch; off: don't")
	heals := fs.Int("heal", 0, "after a mismatch, apply the block's traced state diff and keep going, up to this many blocks")
	datadir := fs.String("datadir", "", "keep the replay state on disk here and resume from it (empty: in memory, from genesis)")
	every := fs.Uint64("checkpoint", checkpointDefault, "with -datadir, flush the state to disk every this many blocks (0: only at exit)")
	cpuProfile := fs.String("cpuprofile", "", "write a CPU profile of the run here")
	memProfile := fs.String("memprofile", "", "write an allocation profile at the end of the run here")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r, err := newReplayerStore(*dir, storeConfig{datadir: *datadir, checkpoint: *every})
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		return 1
	}
	r.ctx = ctx
	if r.head > 0 {
		fmt.Fprintf(os.Stderr, "replay: resuming from block %d in %s\n", r.head, *datadir)
	}
	if *heals > 0 {
		r.heal = &healer{client: newRPCClient(*rpcURL), remaining: min(*heals, healsMax), verbose: *diagnose == "on"}
	}
	prof, err := startProfiles(profilePaths{cpu: *cpuProfile, mem: *memProfile})
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		return 1
	}
	start := time.Now()
	runErr := r.run(*to)
	took := time.Since(start)
	if err := prof.stop(); err != nil {
		runErr = errors.Join(runErr, err)
	}
	if r.failed != nil && *diagnose == "on" {
		r.diagnose(newRPCClient(*rpcURL))
	}
	if err := r.closeStore(); err != nil {
		runErr = errors.Join(runErr, err)
	}
	r.reportHeals()
	res := r.summary(*to, took)
	bench := r.counters.stats(took)
	res.Bench = &bench
	if err := writeResult(*out, &res); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
	}
	line, err := json.Marshal(res)
	if err == nil {
		fmt.Println(string(line))
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "replay:", runErr)
	}
	if r.failed != nil {
		return 1
	}
	return 0
}

func newReplayer(dir string) (*replayer, error) {
	return newReplayerStore(dir, storeConfig{})
}

func newReplayerStore(dir string, cfg storeConfig) (*replayer, error) {
	db, err := openReplayState(cfg)
	if err != nil {
		return nil, err
	}
	g, err := genesis.Mainnet()
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if !db.Head().Loaded {
		if _, err := db.LoadGenesis(g); err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	var extra struct {
		EpochLength uint64 `json:"epochLength"`
	}
	if err := json.Unmarshal(g.Config.Raw, &extra); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	x := &executor.Executor{ChainID: hardfork.MainnetChainID, EpochLength: extra.EpochLength, Configs: chain.NewCache()}
	r := &replayer{db: db, exec: x, dir: dir, store: cfg, ctx: context.Background()}
	if err := r.resume(); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	r.startHead = r.head
	return r, nil
}

func (r *replayer) summary(target uint64, took time.Duration) replayResult {
	res := replayResult{Rung: "R3", Chain: "mainnet", Height: r.head, Target: target, Seconds: took.Seconds()}
	if r.healedFrom != nil {
		res.Height = r.healedHead
		res.Mismatch = r.healedFrom.String()
		return res
	}
	if r.heal != nil && r.heal.first != nil {
		res.Height = r.firstHead
		res.Mismatch = r.heal.first.String()
		return res
	}
	if took > 0 {
		res.BlocksSec = float64(r.head-r.startHead) / took.Seconds()
	}
	if r.failed != nil {
		res.Mismatch = r.failed.String()
	}
	return res
}

func writeResult(path string, res *replayResult) error {
	line, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(line, '\n'), fileMode)
}

func (r *replayer) run(to uint64) error {
	chunks := min(to/chunkBlocks+1, chunksMax)
	first := (r.head + 1) / chunkBlocks
	if first >= chunks {
		return nil
	}
	next := prefetchChunk(r.ctx, r.dir, first*chunkBlocks)
	defer func() { next.Wait() }()
	for i := first; i < chunks; i++ {
		start := i * chunkBlocks
		got := next.take(r.ctx)
		if stopRequested(r.ctx) {
			r.stopped = true
			fmt.Fprintf(os.Stderr, "replay: stopped after block %d\n", r.head)
			return nil
		}
		next.Wait()
		if i+1 < chunks {
			next = prefetchChunk(r.ctx, r.dir, start+chunkBlocks)
		}
		raws, err := got.raws, got.err
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cache ends before block %d", start)
		}
		if err != nil {
			return err
		}
		done, err := r.runChunk(raws, chunkSpan{start: start, to: to})
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return nil
}

type chunkSpan struct {
	start uint64
	to    uint64
}

func (r *replayer) runChunk(raws [][]byte, span chunkSpan) (bool, error) {
	start, to := span.start, span.to
	for j := 0; j < len(raws); j++ {
		number := start + uint64(j)
		if number <= r.head {
			continue
		}
		if number > to {
			return true, nil
		}
		if stopRequested(r.ctx) {
			r.stopped = true
			fmt.Fprintf(os.Stderr, "replay: stopped after block %d\n", r.head)
			return true, nil
		}
		if number != r.head+1 {
			return false, errCacheGap
		}
		blk, err := types.DecodeBlock(raws[j])
		if err != nil {
			return false, fmt.Errorf("block %d: %w", number, err)
		}
		if !r.stepOrHeal(&blk) {
			return true, nil
		}
		if err := r.checkpoint(number); err != nil {
			return false, err
		}
		if number%progressInterval == 0 {
			fmt.Fprintf(os.Stderr, "replay: block %d ok\n", number)
		}
	}
	return false, nil
}
