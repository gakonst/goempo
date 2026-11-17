package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/types"
)

const (
	corpusFileName   = "corpus.jsonl"
	corpusEntriesMax = 1 << 20
	corpusBytesMax   = 1 << 34
	diffShown        = 400
	percent          = 100
)

const _ = uint(corpusBytesMax - corpusEntriesMax)

var (
	errCorpusTooLarge = errors.New("rpc corpus exceeds its limit")
	errStateMissing   = errors.New("state at that block is not replayed")
)

type corpusEntry struct {
	Block    *uint64 `json:"block"`
	Live     bool    `json:"live"`
	Aux      bool    `json:"aux"`
	Request  string  `json:"request"`
	Status   int     `json:"status"`
	Response string  `json:"response"`
}

type methodScore struct {
	Cases     int    `json:"cases"`
	Identical int    `json:"identical"`
	FirstDiff string `json:"first_diff,omitempty"`
}

type r5Result struct {
	Rung             string                  `json:"rung"`
	Methods          int                     `json:"methods"`
	MethodsIdentical int                     `json:"methods_identical"`
	Cases            int                     `json:"cases"`
	CasesIdentical   int                     `json:"cases_identical"`
	LiveSkipped      int                     `json:"live_skipped"`
	ReplayHeight     uint64                  `json:"replay_height"`
	Score            string                  `json:"score"`
	PerMethod        map[string]*methodScore `json:"per_method"`
}

func defaultCorpusPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("cache", "rpc", corpusFileName)
	}
	return filepath.Join(home, "cache", "rpc", corpusFileName)
}

func runRPCDiff(args []string) int {
	fs := flag.NewFlagSet("rpcdiff", flag.ContinueOnError)
	corpus := fs.String("corpus", defaultCorpusPath(), "recorded corpus (scripts/rpc-record.py)")
	blocks := fs.String("blocks", defaultBlockDir(), "fetched blocks with receipts (fetch)")
	chunks := fs.String("chunks", defaultChunkDir(), "raw block chunks for replay (fetch-range)")
	verbose := fs.Bool("v", false, "print the first difference of every method")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	res, err := rpcDiff(diffPaths{corpus: *corpus, blocks: *blocks, chunks: *chunks})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rpcdiff:", err)
		return 1
	}
	if *verbose {
		printDiffs(res)
	}
	line, err := json.Marshal(struct {
		Rung             string `json:"rung"`
		Score            string `json:"score"`
		Methods          int    `json:"methods"`
		MethodsIdentical int    `json:"methods_identical"`
		Cases            int    `json:"cases"`
		CasesIdentical   int    `json:"cases_identical"`
		LiveSkipped      int    `json:"live_skipped"`
		ReplayHeight     uint64 `json:"replay_height"`
	}{res.Rung, res.Score, res.Methods, res.MethodsIdentical, res.Cases, res.CasesIdentical, res.LiveSkipped, res.ReplayHeight})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rpcdiff:", err)
		return 1
	}
	fmt.Println(string(line))
	return 0
}

func printDiffs(res *r5Result) {
	names := slices.Sorted(maps.Keys(res.PerMethod))
	for _, name := range names {
		m := res.PerMethod[name]
		mark := "ok  "
		if m.Identical != m.Cases {
			mark = "DIFF"
		}
		fmt.Fprintf(os.Stderr, "%s %-45s %d/%d\n", mark, name, m.Identical, m.Cases)
		if m.FirstDiff != "" {
			fmt.Fprintln(os.Stderr, "     "+m.FirstDiff)
		}
	}
}

func loadCorpus(path string) ([]corpusEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > corpusBytesMax {
		return nil, errCorpusTooLarge
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > corpusEntriesMax {
		return nil, errCorpusTooLarge
	}
	out := make([]corpusEntry, 0, len(lines))
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e corpusEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

type replayState struct {
	r *replayer
}

func (s *replayState) StateAt(number uint64) (rpc.StateReader, error) {
	if number != s.r.head {
		return nil, errStateMissing
	}
	return s.r.db.State(), nil
}

type diffPaths struct {
	corpus string
	blocks string
	chunks string
}

func rpcDiff(paths diffPaths) (*r5Result, error) {
	blockDir, chunkDir := paths.blocks, paths.chunks
	entries, err := loadCorpus(paths.corpus)
	if err != nil {
		return nil, err
	}
	chain := rpc.NewMemChain()
	if err := loadFetchedBlocks(chain, blockDir); err != nil {
		return nil, err
	}
	stateTo := uint64(0)
	for i := range entries {
		if entries[i].Block != nil {
			stateTo = max(stateTo, *entries[i].Block)
		}
	}
	stateTo = min(stateTo, diffBlocksMax)
	if err := loadChunkBlocks(chain, chunkDir, stateTo); err != nil {
		return nil, err
	}
	if err := loadAuxBlocks(chain, entries); err != nil {
		return nil, err
	}
	view := newChunkChain(chain, chunkDir)
	r, err := newReplayer(chunkDir)
	if err != nil {
		return nil, err
	}
	srv := rpc.NewServer(rpc.Config{ChainID: rpc.MainnetChainID}, view, &replayState{r: r}, nil)
	res := &r5Result{Rung: "R5", PerMethod: map[string]*methodScore{}}
	for _, e := range scoreOrder(entries) {
		if e.Block != nil && *e.Block > r.head && r.failed == nil {
			if err := replayTo(r, chain, *e.Block); err != nil {
				return nil, err
			}
		}
		res.score(srv, e)
	}
	res.ReplayHeight = r.head
	res.finish()
	return res, nil
}

func scoreOrder(entries []corpusEntry) []*corpusEntry {
	byBlock := map[uint64][]*corpusEntry{}
	var rest []*corpusEntry
	for i := range entries {
		e := &entries[i]
		if e.Block == nil {
			rest = append(rest, e)
			continue
		}
		byBlock[*e.Block] = append(byBlock[*e.Block], e)
	}
	out := make([]*corpusEntry, 0, len(entries))
	for _, n := range slices.Sorted(maps.Keys(byBlock)) {
		out = append(out, byBlock[n]...)
	}
	return append(out, rest...)
}

func replayTo(r *replayer, chain *rpc.MemChain, to uint64) error {
	steps := min(to-r.head, diffBlocksMax)
	for range steps {
		n := r.head + 1
		b, ok := chain.Block(n)
		if !ok {
			return fmt.Errorf("block %d is not cached", n)
		}
		if !r.step(b.Block) {
			return nil
		}
		data, err := rpc.NewBlockData(b.Block, b.Raw, r.result.Receipts)
		if err != nil {
			return err
		}
		chain.Add(data)
	}
	return nil
}

func loadFetchedBlocks(chain *rpc.MemChain, dir string) error {
	cached, err := loadCache(dir)
	if err != nil {
		return err
	}
	for i := range cached {
		c := &cached[i]
		raw, err := primitives.DecodeHex(c.Raw)
		if err != nil {
			return fmt.Errorf("block %d: %w", c.Number, err)
		}
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			return fmt.Errorf("block %d: %w", c.Number, err)
		}
		var rjs []receiptJSON
		if err := json.Unmarshal(c.Receipts, &rjs); err != nil {
			return fmt.Errorf("block %d receipts: %w", c.Number, err)
		}
		receipts := make([]types.Receipt, len(rjs))
		for j := range rjs {
			if receipts[j], err = rjs[j].toReceipt(); err != nil {
				return fmt.Errorf("block %d receipt %d: %w", c.Number, j, err)
			}
		}
		data, err := rpc.NewBlockData(&blk, raw, receipts)
		if err != nil {
			return fmt.Errorf("block %d: %w", c.Number, err)
		}
		chain.Add(data)
	}
	return nil
}

func loadChunkBlocks(chain *rpc.MemChain, dir string, to uint64) error {
	for c := range to/chunkBlocks + 1 {
		start := c * chunkBlocks
		raws, err := readChunk(dir, start)
		if err != nil {
			return err
		}
		if err := addChunk(chain, start, raws); err != nil {
			return err
		}
	}
	return nil
}

func addChunk(chain *rpc.MemChain, start uint64, raws [][]byte) error {
	for j, raw := range raws {
		n := start + uint64(j)
		if _, ok := chain.Block(n); ok {
			continue
		}
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			return fmt.Errorf("block %d: %w", n, err)
		}
		data, err := rpc.NewBlockData(&blk, raw, nil)
		if err != nil {
			return err
		}
		chain.Add(data)
	}
	return nil
}

func requestMethod(raw string) string {
	var env struct {
		Method any `json:"method"`
	}
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "[") {
		return "(batch)"
	}
	if json.Unmarshal([]byte(trimmed), &env) != nil {
		return "(malformed)"
	}
	name, ok := env.Method.(string)
	if !ok {
		return "(malformed)"
	}
	return name
}

func (res *r5Result) score(srv *rpc.Server, e *corpusEntry) {
	if e.Aux {
		return
	}
	if e.Live {
		res.LiveSkipped++
		return
	}
	name := requestMethod(e.Request)
	m := res.PerMethod[name]
	if m == nil {
		m = &methodScore{}
		res.PerMethod[name] = m
	}
	m.Cases++
	got := srv.Handle([]byte(e.Request))
	if got.Status == e.Status && sameJSON(got.Body, e.Response) {
		m.Identical++
		return
	}
	if m.FirstDiff == "" {
		m.FirstDiff = fmt.Sprintf("req %s\n     want %d %s\n     got  %d %s", clip(e.Request), e.Status, clip(e.Response), got.Status, clip(string(got.Body)))
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > diffShown {
		return s[:diffShown] + "..."
	}
	return s
}

func sameJSON(got []byte, want string) bool {
	var x, y any
	dx := json.NewDecoder(bytes.NewReader(got))
	dx.UseNumber()
	dy := json.NewDecoder(strings.NewReader(want))
	dy.UseNumber()
	if dx.Decode(&x) != nil || dy.Decode(&y) != nil {
		return string(bytes.TrimSpace(got)) == strings.TrimSpace(want)
	}
	return reflect.DeepEqual(x, y)
}

func (res *r5Result) finish() {
	for _, name := range slices.Sorted(maps.Keys(res.PerMethod)) {
		m := res.PerMethod[name]
		res.Methods++
		res.Cases += m.Cases
		res.CasesIdentical += m.Identical
		if m.Identical == m.Cases {
			res.MethodsIdentical++
		}
	}
	casePct := 0.0
	if res.Cases > 0 {
		casePct = float64(res.CasesIdentical) * percent / float64(res.Cases)
	}
	res.Score = fmt.Sprintf("%d/%d methods, %.1f%% of cases", res.MethodsIdentical, res.Methods, casePct)
}

const (
	chunkCacheMax = 4
	diffBlocksMax = 1 << 24
)

const (
	_ = uint(chunkBlocks - chunkCacheMax)
	_ = uint(replayBlocksMax - diffBlocksMax)
)

type chunkChain struct {
	*rpc.MemChain
	dir    string
	chunks map[uint64][][]byte
}

func newChunkChain(mem *rpc.MemChain, dir string) *chunkChain {
	return &chunkChain{MemChain: mem, dir: dir, chunks: map[uint64][][]byte{}}
}

func (c *chunkChain) Block(number uint64) (*rpc.BlockData, bool) {
	if b, ok := c.MemChain.Block(number); ok {
		return b, true
	}
	start := chunkStart(number)
	raws, ok := c.chunks[start]
	if !ok {
		var err error
		if raws, err = readChunk(c.dir, start); err != nil {
			return nil, false
		}
		if len(c.chunks) >= chunkCacheMax {
			clear(c.chunks)
		}
		c.chunks[start] = raws
	}
	i := number - start
	if i >= uint64(len(raws)) {
		return nil, false
	}
	blk, err := types.DecodeBlock(raws[i])
	if err != nil {
		return nil, false
	}
	data, err := rpc.NewBlockData(&blk, raws[i], nil)
	if err != nil {
		return nil, false
	}
	return data, true
}

func loadAuxBlocks(chain *rpc.MemChain, entries []corpusEntry) error {
	for i := range entries {
		e := &entries[i]
		if !e.Aux {
			continue
		}
		var resp struct {
			Result string `json:"result"`
		}
		if json.Unmarshal([]byte(e.Response), &resp) != nil || resp.Result == "" {
			continue
		}
		raw, err := primitives.DecodeHex(resp.Result)
		if err != nil {
			return err
		}
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			return err
		}
		if _, ok := chain.Block(blk.Header.Inner.Number); ok {
			continue
		}
		data, err := rpc.NewBlockData(&blk, raw, nil)
		if err != nil {
			return err
		}
		chain.Add(data)
	}
	return nil
}
