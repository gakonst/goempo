package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
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
	Rung      string  `json:"rung"`
	Chain     string  `json:"chain"`
	Height    uint64  `json:"height"`
	Target    uint64  `json:"target"`
	Mismatch  string  `json:"mismatch,omitempty"`
	Seconds   float64 `json:"seconds"`
	BlocksSec float64 `json:"blocks_per_sec"`
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
	db     *state.DB
	exec   *executor.Executor
	hashes [hashWindow]primitives.Hash
	head   uint64
	dir    string
	failed *mismatch
	result *executor.Result
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := newReplayer(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		return 1
	}
	start := time.Now()
	runErr := r.run(*to)
	res := r.summary(*to, time.Since(start))
	if err := writeResult(*out, &res); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
	}
	line, err := json.Marshal(res)
	if err == nil {
		fmt.Println(string(line))
	}
	if r.failed != nil && *diagnose == "on" {
		r.diagnose(newRPCClient(*rpcURL))
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
	db, err := state.Open(goio.NewMemFS(), state.Options{})
	if err != nil {
		return nil, err
	}
	g, err := genesis.Mainnet()
	if err != nil {
		return nil, err
	}
	if _, err := db.LoadGenesis(g); err != nil {
		return nil, err
	}
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	return &replayer{db: db, exec: x, dir: dir}, nil
}

func (r *replayer) summary(target uint64, took time.Duration) replayResult {
	res := replayResult{Rung: "R3", Chain: "mainnet", Height: r.head, Target: target, Seconds: took.Seconds()}
	if took > 0 {
		res.BlocksSec = float64(r.head) / took.Seconds()
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
	for i := uint64(0); i < chunks; i++ {
		start := i * chunkBlocks
		raws, err := readChunk(r.dir, start)
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
		if number == 0 {
			continue
		}
		if number > to {
			return true, nil
		}
		if number != r.head+1 {
			return false, errCacheGap
		}
		blk, err := types.DecodeBlock(raws[j])
		if err != nil {
			return false, fmt.Errorf("block %d: %w", number, err)
		}
		if !r.step(&blk) {
			return true, nil
		}
		if number%progressInterval == 0 {
			fmt.Fprintf(os.Stderr, "replay: block %d ok\n", number)
		}
	}
	return false, nil
}
