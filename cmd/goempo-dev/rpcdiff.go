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
	"strconv"
	"strings"
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/rpcexec"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	corpusFileName          = "corpus.jsonl"
	stateCorpusFileName     = "corpus-b.jsonl"
	extraCorpusFileName     = "corpus-d.jsonl"
	auxCorpusFileName       = "corpus-e.jsonl"
	userTraceCorpusFileName = "corpus-f.jsonl"
	tracerCorpusFileName    = "corpus-g.jsonl"
	aaTraceCorpusFileName   = "corpus-h.jsonl"
	replayCorpusFileName    = "corpus-i.jsonl"
	methodsCorpusFileName   = "corpus-j.jsonl"
	statusForbidden         = 403
	corpusEntriesMax        = 1 << 20
	corpusBytesMax          = 1 << 34
	diffShown               = 400
	percent                 = 100
)

const _ = uint(corpusBytesMax - corpusEntriesMax)

var nodeLocal = map[string]bool{
	"net_peerCount":                   true,
	"eth_newFilter":                   true,
	"eth_newBlockFilter":              true,
	"eth_newPendingTransactionFilter": true,
	"eth_subscribe":                   true,
}

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
	GatewaySkipped   int                     `json:"gateway_skipped"`
	NodeLocalSkipped int                     `json:"node_local_skipped"`
	ReplayHeight     uint64                  `json:"replay_height"`
	Score            string                  `json:"score"`
	PerMethod        map[string]*methodScore `json:"per_method"`
	Mismatches       []mismatchCase          `json:"-"`
}

type mismatchCase struct {
	Request string          `json:"request"`
	Want    json.RawMessage `json:"want"`
	Got     json.RawMessage `json:"got"`
}

func defaultCorpusPath() string {
	dir := filepath.Join("cache", "rpc")
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, dir)
	}
	return strings.Join([]string{
		filepath.Join(dir, corpusFileName),
		filepath.Join(dir, stateCorpusFileName),
		filepath.Join(dir, extraCorpusFileName),
		filepath.Join(dir, auxCorpusFileName),
		filepath.Join(dir, userTraceCorpusFileName),
		filepath.Join(dir, tracerCorpusFileName),
		filepath.Join(dir, aaTraceCorpusFileName),
		filepath.Join(dir, replayCorpusFileName),
		filepath.Join(dir, methodsCorpusFileName),
	}, ",")
}

func runRPCDiff(args []string) int {
	fs := flag.NewFlagSet("rpcdiff", flag.ContinueOnError)
	corpus := fs.String("corpus", defaultCorpusPath(), "recorded corpora, comma separated (scripts/rpc-record.py)")
	blocks := fs.String("blocks", defaultBlockDir(), "fetched blocks with receipts (fetch)")
	chunks := fs.String("chunks", defaultChunkDir(), "raw block chunks for replay (fetch-range)")
	verbose := fs.Bool("v", false, "print the first difference of every method")
	dump := fs.String("dump", "", "write every mismatching case to this JSONL file")
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
	if *dump != "" {
		if err := writeMismatches(*dump, res.Mismatches); err != nil {
			fmt.Fprintln(os.Stderr, "rpcdiff:", err)
			return 1
		}
	}
	line, err := json.Marshal(struct {
		Rung             string `json:"rung"`
		Score            string `json:"score"`
		Methods          int    `json:"methods"`
		MethodsIdentical int    `json:"methods_identical"`
		Cases            int    `json:"cases"`
		CasesIdentical   int    `json:"cases_identical"`
		LiveSkipped      int    `json:"live_skipped"`
		GatewaySkipped   int    `json:"gateway_skipped"`
		NodeLocalSkipped int    `json:"node_local_skipped"`
		ReplayHeight     uint64 `json:"replay_height"`
	}{res.Rung, res.Score, res.Methods, res.MethodsIdentical, res.Cases, res.CasesIdentical, res.LiveSkipped, res.GatewaySkipped, res.NodeLocalSkipped, res.ReplayHeight})
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
	r  *replayer
	mu sync.Mutex
}

type lockedReader struct {
	mu    *sync.Mutex
	inner rpc.StateReader
}

func (l lockedReader) Balance(addr primitives.Address) u256.Int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Balance(addr)
}

func (l lockedReader) Nonce(addr primitives.Address) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Nonce(addr)
}

func (l lockedReader) Code(addr primitives.Address) []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Code(addr)
}

func (l lockedReader) Storage(addr primitives.Address, key primitives.Hash) u256.Int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Storage(addr, key)
}

func (s *replayState) ExecState(number uint64) (rpcexec.State, error) {
	if number != s.r.head {
		return nil, errStateMissing
	}
	return s.r.db.State(), nil
}

func (s *replayState) ProofAt(number uint64, addr primitives.Address, keys []primitives.Hash) (rpc.AccountProof, error) {
	if number != s.r.head {
		return rpc.AccountProof{}, errStateMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.r.db.Proof(addr, keys)
	if err != nil {
		return rpc.AccountProof{}, err
	}
	out := rpc.AccountProof{
		Exists:      p.Exists,
		Balance:     p.Balance,
		Nonce:       p.Nonce,
		CodeHash:    p.CodeHash,
		StorageRoot: p.StorageRoot,
		Proof:       p.AccountProof,
		Storage:     make([]rpc.StorageProof, len(p.Storage)),
	}
	for i, sp := range p.Storage {
		out.Storage[i] = rpc.StorageProof{Value: sp.Value, Proof: sp.Proof}
	}
	return out, nil
}

func (s *replayState) StateAt(number uint64) (rpc.StateReader, error) {
	if number != s.r.head {
		return nil, errStateMissing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return lockedReader{mu: &s.mu, inner: s.r.db.State()}, nil
}

type diffPaths struct {
	corpus string
	blocks string
	chunks string
}

func rpcDiff(paths diffPaths) (*r5Result, error) {
	blockDir, chunkDir := paths.blocks, paths.chunks
	var entries []corpusEntry
	for _, path := range strings.Split(paths.corpus, ",") {
		part, err := loadCorpus(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, part...)
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
	states := &replayState{r: r}
	exec := &rpcexec.Executor{ChainID: hardfork.MainnetChainID, Chain: view, States: states, Blocks: r.exec, Lock: &states.mu}
	srv := rpc.NewServer(rpc.Config{ChainID: rpc.MainnetChainID}, view, states, exec)
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
	if e.Status == statusForbidden {
		res.GatewaySkipped++
		return
	}
	if nodeLocal[requestMethod(e.Request)] {
		res.NodeLocalSkipped++
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
	res.Mismatches = append(res.Mismatches, mismatchCase{Request: e.Request, Want: jsonOrString(e.Response), Got: jsonOrString(string(got.Body))})
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

func auxResult(e *corpusEntry) ([]byte, bool) {
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(e.Response), &resp) != nil {
		return nil, false
	}
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return nil, false
	}
	return resp.Result, true
}

func auxBlockNumber(e *corpusEntry) (uint64, bool) {
	var req struct {
		Params []string `json:"params"`
	}
	if json.Unmarshal([]byte(e.Request), &req) != nil || len(req.Params) != 1 {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(req.Params[0], "0x"), 16, 64)
	return n, err == nil
}

func loadAuxBlocks(chain *rpc.MemChain, entries []corpusEntry) error {
	receipts := map[uint64][]types.Receipt{}
	for i := range entries {
		e := &entries[i]
		if !e.Aux || requestMethod(e.Request) != "debug_getRawReceipts" {
			continue
		}
		res, ok := auxResult(e)
		n, okN := auxBlockNumber(e)
		if !ok || !okN {
			continue
		}
		var raws []string
		if err := json.Unmarshal(res, &raws); err != nil {
			return err
		}
		list := make([]types.Receipt, 0, len(raws))
		for _, r := range raws {
			b, err := primitives.DecodeHex(r)
			if err != nil {
				return err
			}
			rc, err := types.DecodeReceipt(b)
			if err != nil {
				return err
			}
			list = append(list, rc)
		}
		receipts[n] = list
	}
	for i := range entries {
		e := &entries[i]
		if !e.Aux || requestMethod(e.Request) != "debug_getRawBlock" {
			continue
		}
		if err := addAuxBlock(chain, e, receipts); err != nil {
			return err
		}
	}
	return nil
}

func addAuxBlock(chain *rpc.MemChain, e *corpusEntry, receipts map[uint64][]types.Receipt) error {
	res, ok := auxResult(e)
	if !ok {
		return nil
	}
	var hexRaw string
	if err := json.Unmarshal(res, &hexRaw); err != nil {
		return err
	}
	raw, err := primitives.DecodeHex(hexRaw)
	if err != nil {
		return err
	}
	blk, err := types.DecodeBlock(raw)
	if err != nil {
		return err
	}
	n := blk.Header.Inner.Number
	if _, ok := chain.Block(n); ok {
		return nil
	}
	data, err := rpc.NewBlockData(&blk, raw, receipts[n])
	if err != nil {
		return err
	}
	chain.Add(data)
	return nil
}

func jsonOrString(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func writeMismatches(path string, cases []mismatchCase) error {
	var buf []byte
	for _, c := range cases {
		line, err := json.Marshal(c)
		if err != nil {
			return err
		}
		buf = append(append(buf, line...), '\n')
	}
	return os.WriteFile(path, buf, 0o644)
}
