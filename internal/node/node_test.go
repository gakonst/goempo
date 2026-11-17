package node_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	block200Hash   = "0x2ff233e47fe16743bbfc0e4e5ec61a81b974590a630702375cdbdf60662ecac5"
	receiptsRoot5  = "b64408da6b8fe39ab764af88ece1e8cca1c35fd988db57806e99138c629365a0"
	fixtureHead    = 200
	advanceRounds  = 16
	boundary2040   = 44085599
	boundary2041   = 44107199
	firstOf2042    = 44107200
	finalizationsF = "../consensus/testdata/mainnet_finalizations.jsonl"
)

func testArchive(t *testing.T) *node.Archive {
	t.Helper()
	fs, err := goio.NewDirFS("testdata")
	assert.NoError(err, "err")
	return node.NewArchive(fs)
}

type failing struct{}

func (failing) RawBlocks(uint64, int) ([][]byte, error) { return nil, errors.New("offline") }

type tampered struct {
	inner  node.BlockSource
	number uint64
	edit   func([]byte) []byte
}

func (s tampered) RawBlocks(from uint64, count int) ([][]byte, error) {
	raws, err := s.inner.RawBlocks(from, count)
	out := make([][]byte, len(raws))
	for i, raw := range raws {
		out[i] = raw
		if from+uint64(i) == s.number {
			out[i] = s.edit(bytes.Clone(raw))
		}
	}
	return out, err
}

func advanceTo(t *testing.T, n *node.Node, to uint64) error {
	t.Helper()
	for range advanceRounds {
		if n.Head().Number >= to {
			return nil
		}
		if _, err := n.Advance(node.Target{To: to, Tip: to}); err != nil {
			return err
		}
	}
	return errors.New("advance did not reach the target")
}

func TestExecuteFromGenesisAndResume(t *testing.T) {
	archive := testArchive(t)
	fs := goio.NewMemFS()
	cfg := node.Config{State: fs, Sources: []node.BlockSource{archive, failing{}}, Archive: archive}
	n, err := node.Open(cfg)
	assert.NoError(err, "err")
	assert.NoError(advanceTo(t, n, fixtureHead), "advanceTo(t n fixtureHead)")
	h := n.Head()
	head, hash := h.Number, h.Hash
	assert.Equal(head, uint64(fixtureHead), "uint64(fixtureHead) head")
	assert.Equal(hash.String(), block200Hash, "block200Hash hash.String()")
	stats := n.Stats()
	assert.Equal(stats.Blocks, uint64(fixtureHead), "uint64(fixtureHead) stats.Blocks")
	assert.NoError(n.Close(), "n.Close()")

	n, err = node.Open(cfg)
	assert.NoError(err, "err")
	h = n.Head()
	head, hash = h.Number, h.Hash
	assert.Equal(head, uint64(fixtureHead), "uint64(fixtureHead) head")
	assert.Equal(hash.String(), block200Hash, "block200Hash hash.String()")
	assert.Equal(n.Start(), uint64(fixtureHead), "uint64(fixtureHead) n.Start()")
	b, ok := n.Recent().Block(fixtureHead - 1)
	assert.Ok(ok, "ok")
	assert.Equal(b.Number(), uint64(fixtureHead-1), "uint64(fixtureHead1) b.Number()")
	assert.NoError(n.Close(), "n.Close()")
}

func TestServeLiveState(t *testing.T) {
	archive := testArchive(t)
	n, err := node.Open(node.Config{State: goio.NewMemFS(), Sources: []node.BlockSource{archive}, Archive: archive})
	assert.NoError(err, "err")
	defer func() { assert.NoError(n.Close(), "n.Close()") }()
	assert.NoError(advanceTo(t, n, 20), "advanceTo(t n 20)")
	srv := n.Server(rpc.Config{})
	call := func(method string, params string) map[string]json.RawMessage {
		resp := srv.Handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`))
		var out map[string]json.RawMessage
		assert.NoError(json.Unmarshal(resp.Body, &out), "json.Unmarshal(resp.Body out)")
		return out
	}
	assert.Equal(string(call("eth_blockNumber", "[]")["result"]), `"0x14"`, "0x14 string(call(eth_blockNumber )result)")
	block := call("eth_getBlockByNumber", `["0x14", false]`)
	assert.Ok(strings.Contains(string(block["result"]), `"number":"0x14"`), "strings.Contains(string(blockresult) number0x14)")
	bal := call("eth_getBalance", `["0x0000000000000000000000000000000000000000", "latest"]`)
	assert.Ok(bal["result"] != nil, "balresult != nil")
	old := call("eth_getTransactionCount", `["0x0000000000000000000000000000000000000000", "0x5"]`)
	assert.Ok(old["error"] != nil, "state below the head is refused")
	proof := call("eth_getProof", `["0x0000000000000000000000000000000000000000", [], "latest"]`)
	assert.Ok(proof["result"] != nil, "proofresult != nil")
}

func TestBadReceiptsRootLatches(t *testing.T) {
	archive := testArchive(t)
	want, err := hex.DecodeString(receiptsRoot5)
	assert.NoError(err, "err")
	bad := tampered{inner: archive, number: 5, edit: func(raw []byte) []byte {
		i := bytes.Index(raw, want)
		raw[i] ^= 0xff
		return raw
	}}
	n, err := node.Open(node.Config{State: goio.NewMemFS(), Sources: []node.BlockSource{bad}})
	assert.NoError(err, "err")
	defer func() { assert.NoError(n.Close(), "n.Close()") }()
	err = advanceTo(t, n, 10)
	var be *node.BlockError
	assert.Ok(errors.As(err, &be), "errors.As(err be)")
	assert.Equal(be.Number, uint64(5), "uint64(5) be.Number")
	assert.Equal(be.Field, "receiptsRoot", "receiptsRoot be.Field")
	head := n.Head().Number
	assert.Equal(head, uint64(4), "uint64(4) head")
	_, err = n.Advance(node.Target{To: 10, Tip: 10})
	assert.Ok(errors.As(err, &be), "errors.As(err be)")
	assert.Equal(n.Failed(), err, "err n.Failed()")
}

func TestBrokenLinkDoesNotLatch(t *testing.T) {
	archive := testArchive(t)
	bad := tampered{inner: archive, number: 7, edit: func(raw []byte) []byte {
		blk, err := types.DecodeBlock(raw)
		assert.NoError(err, "err")
		parent := blk.Header.Inner.ParentHash
		i := bytes.Index(raw, parent[:])
		raw[i] ^= 0xff
		return raw
	}}
	n, err := node.Open(node.Config{State: goio.NewMemFS(), Sources: []node.BlockSource{bad}})
	assert.NoError(err, "err")
	defer func() { assert.NoError(n.Close(), "n.Close()") }()
	p, err := n.Advance(node.Target{To: 10, Tip: 10})
	assert.Ok(errors.Is(err, node.ErrChainLink), "errors.Is(err node.ErrChainLink)")
	assert.Equal(p.Executed, 6, "6 p.Executed")
	assert.NoError(n.Failed(), "n.Failed()")
	head := n.Head().Number
	assert.Equal(head, uint64(6), "uint64(6) head")
}

func TestArchiveChunks(t *testing.T) {
	raws := [][]byte{{1, 2, 3}, {4}, {5, 6}}
	packed, err := node.PackChunk(raws)
	assert.NoError(err, "err")
	fs := goio.NewMemFS()
	f, err := fs.Create(node.ArchiveChunkName(node.ArchiveChunkBlocks))
	assert.NoError(err, "err")
	_, err = f.Write(packed)
	assert.NoError(err, "err")
	assert.NoError(f.Close(), "f.Close()")
	a := node.NewArchive(fs)
	got, err := a.RawBlocks(node.ArchiveChunkBlocks, 3)
	assert.NoError(err, "err")
	assert.Equal(fmt.Sprint(got), fmt.Sprint(raws), "raws got")
	_, err = a.Raw(node.ArchiveChunkBlocks + 3)
	assert.Ok(errors.Is(err, node.ErrNotArchived), "errors.Is(err node.ErrNotArchived)")
	_, err = a.Raw(0)
	assert.Ok(errors.Is(err, node.ErrNotArchived), "errors.Is(err node.ErrNotArchived)")
	_, err = node.SplitChunk([]byte{0, 0, 0, 9, 1})
	assert.Ok(errors.Is(err, node.ErrArchiveChunk), "errors.Is(err node.ErrArchiveChunk)")
	_, err = node.PackChunk([][]byte{{}})
	assert.Ok(errors.Is(err, node.ErrArchiveChunk), "errors.Is(err node.ErrArchiveChunk)")
}

type fakeCaller struct {
	reply func(reqs []map[string]any) any
}

func (c fakeCaller) Call(body []byte) ([]byte, error) {
	var reqs []map[string]any
	if err := json.Unmarshal(body, &reqs); err != nil {
		return nil, err
	}
	return json.Marshal(c.reply(reqs))
}

func TestUpstream(t *testing.T) {
	up := node.NewUpstream(fakeCaller{reply: func(reqs []map[string]any) any {
		out := make([]map[string]any, len(reqs))
		for i, r := range reqs {
			out[i] = map[string]any{"jsonrpc": "2.0", "id": r["id"]}
			switch r["method"] {
			case "eth_blockNumber":
				out[i]["result"] = "0x2a"
			case "debug_getRawBlock":
				if r["params"].([]any)[0] == "0x2" {
					out[i]["error"] = map[string]any{"code": -32000, "message": "unknown block"}
					continue
				}
				out[i]["result"] = "0xc0ffee"
			case "consensus_getFinalization":
				if i == 1 {
					out[i]["error"] = map[string]any{"code": 503, "message": "not available"}
					continue
				}
				out[i]["result"] = map[string]any{"certificate": "abcd"}
			}
		}
		return out
	}})
	tip, err := up.Tip()
	assert.NoError(err, "err")
	assert.Equal(tip, uint64(42), "uint64(42) tip")
	raws, err := up.RawBlocks(0, 4)
	assert.NoError(err, "err")
	assert.Equal(fmt.Sprint(raws), fmt.Sprint([][]byte{{0xc0, 0xff, 0xee}, {0xc0, 0xff, 0xee}}), "raw blocks stop at the first error")
	certs, err := up.Finalizations(10, 3)
	assert.NoError(err, "err")
	assert.Equal(fmt.Sprint(certs), fmt.Sprint([][]byte{{0xab, 0xcd}, nil, {0xab, 0xcd}}), "503 leaves a gap")
}

type fixtureFinality map[uint64][]byte

func (f fixtureFinality) Finalizations(from uint64, count int) ([][]byte, error) {
	out := make([][]byte, count)
	for i := range out {
		out[i] = f[from+uint64(i)]
	}
	return out, nil
}

func loadBlock(t *testing.T, number string) types.Block {
	t.Helper()
	data, err := os.ReadFile("testdata/mainnet_block_" + number + ".hex")
	assert.NoError(err, "err")
	raw, err := hex.DecodeString(strings.TrimSpace(string(data)))
	assert.NoError(err, "err")
	blk, err := types.DecodeBlock(raw)
	assert.NoError(err, "err")
	return blk
}

func loadFinalizations(t *testing.T) fixtureFinality {
	t.Helper()
	data, err := os.ReadFile(finalizationsF)
	assert.NoError(err, "err")
	out := fixtureFinality{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var f struct {
			Height      uint64 `json:"height"`
			Certificate string `json:"certificate"`
		}
		assert.NoError(json.Unmarshal([]byte(line), &f), "json.Unmarshal(byte(line) f)")
		cert, err := hex.DecodeString(strings.TrimPrefix(f.Certificate, "0x"))
		assert.NoError(err, "err")
		out[f.Height] = cert
	}
	return out
}

func TestCertifierAcrossEpochBoundary(t *testing.T) {
	fin := loadFinalizations(t)
	c := node.NewCertifier(consensus.NewMainnetChain(), fin, node.MainnetCertifyFrom)
	anchor := loadBlock(t, "44085599")
	last := loadBlock(t, "44107199")
	first := loadBlock(t, "44107200")
	boundary, ok := c.NeedsAnchor(boundary2041)
	assert.Ok(ok, "ok")
	assert.Equal(boundary, uint64(boundary2040), "uint64(boundary2040) boundary")
	assert.NoError(c.Anchor(boundary, &anchor), "c.Anchor(boundary anchor)")
	_, ok = c.NeedsAnchor(boundary2041)
	assert.Ok(!(ok), "ok")

	n, err := c.Admit([]types.Block{last, first}, node.NearTip)
	assert.NoError(err, "err")
	assert.Equal(n, 1, "1 n")
	n, err = c.Admit([]types.Block{first}, node.NearTip)
	assert.NoError(err, "err")
	assert.Equal(n, 1, "1 n")
	s := c.Stats()
	assert.Equal(s.Certified, uint64(2), "uint64(2) s.Certified")
	assert.Equal(s.Transitions, uint64(1), "uint64(1) s.Transitions")
	assert.Equal(s.Anchor, uint64(boundary2040), "uint64(boundary2040) s.Anchor")
}

func TestCertifierRejectsForgedBlock(t *testing.T) {
	fin := loadFinalizations(t)
	c := node.NewCertifier(consensus.NewMainnetChain(), fin, node.MainnetCertifyFrom)
	anchor := loadBlock(t, "44085599")
	last := loadBlock(t, "44107199")
	first := loadBlock(t, "44107200")
	assert.NoError(c.Anchor(boundary2040, &anchor), "c.Anchor(boundary2040 anchor)")
	_, err := c.Admit([]types.Block{last}, node.NearTip)
	assert.NoError(err, "err")
	first.Header.Inner.GasUsed++
	_, err = c.Admit([]types.Block{first}, node.NearTip)
	assert.Ok(err != nil, "err")
}

func TestCertifierWaitsForCertificate(t *testing.T) {
	c := node.NewCertifier(consensus.NewMainnetChain(), fixtureFinality{}, node.MainnetCertifyFrom)
	anchor := loadBlock(t, "44085599")
	first := loadBlock(t, "44107200")
	assert.NoError(c.Anchor(boundary2040, &anchor), "c.Anchor(boundary2040 anchor)")
	n, err := c.Admit([]types.Block{first}, node.NearTip)
	assert.NoError(err, "err")
	assert.Equal(n, 0, "0 n")
	n, err = c.Admit([]types.Block{first}, node.Buried)
	assert.NoError(err, "err")
	assert.Equal(n, 1, "1 n")
	assert.Equal(c.Stats().Unchecked, uint64(1), "uint64(1) c.Stats().Unchecked")
}

func TestRecentEvicts(t *testing.T) {
	archive := testArchive(t)
	r := node.NewRecent(4, nil)
	for i := range uint64(10) {
		raw, err := archive.Raw(i)
		assert.NoError(err, "err")
		blk, err := types.DecodeBlock(raw)
		assert.NoError(err, "err")
		b, err := rpc.NewBlockData(&blk, raw, nil)
		assert.NoError(err, "err")
		r.Add(b)
	}
	assert.Equal(r.Head(), uint64(9), "uint64(9) r.Head()")
	_, ok := r.Block(9)
	assert.Ok(ok, "ok")
	_, ok = r.Block(0)
	assert.Ok(ok, "ok")
	_, ok = r.Block(3)
	assert.Ok(!(ok), "ok")
	_, ok = r.Block(10)
	assert.Ok(!(ok), "ok")
	_, ok = r.BlockNumber(primitives.Hash{})
	assert.Ok(!(ok), "ok")
}
