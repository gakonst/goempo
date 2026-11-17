package rpc_test

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

type fixtureBlock struct {
	Number   uint64   `json:"number"`
	Raw      string   `json:"raw"`
	Receipts []string `json:"receipts"`
}

type fixtureCase struct {
	Request  string `json:"request"`
	Status   int    `json:"status"`
	Response string `json:"response"`
}

func decodeHex(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	assert.NoError(err, "fixture hex")
	return b
}

func readLines(path string) [][]byte {
	data, err := os.ReadFile(path)
	assert.NoError(err, "read fixture")
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		assert.NoError(err, "open gzip fixture")
		data, err = io.ReadAll(gz)
		assert.NoError(err, "read gzip fixture")
	}
	return bytes.Split(bytes.TrimSpace(data), []byte{10})
}

func loadChain() *rpc.MemChain {
	chain := rpc.NewMemChain()
	for _, line := range readLines("testdata/blocks.jsonl") {
		var fb fixtureBlock
		assert.NoError(json.Unmarshal(line, &fb), "block fixture")
		raw := decodeHex(fb.Raw)
		blk, err := types.DecodeBlock(raw)
		assert.NoError(err, "decode block")
		receipts := make([]types.Receipt, len(fb.Receipts))
		for i, r := range fb.Receipts {
			receipts[i], err = types.DecodeReceipt(decodeHex(r))
			assert.NoError(err, "decode receipt")
		}
		bd, err := rpc.NewBlockData(&blk, raw, receipts)
		assert.NoError(err, "block data")
		chain.Add(bd)
	}
	return chain
}

func loadCases(path string) []fixtureCase {
	lines := readLines(path)
	out := make([]fixtureCase, 0, len(lines))
	for _, line := range lines {
		var c fixtureCase
		assert.NoError(json.Unmarshal(line, &c), "case fixture")
		out = append(out, c)
	}
	return out
}

func decodeJSON(b []byte) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	assert.NoError(d.Decode(&v), "decode json")
	return v
}

func matches(got rpc.Response, want fixtureCase) bool {
	if got.Status != want.Status {
		return false
	}
	return reflect.DeepEqual(decodeJSON(got.Body), decodeJSON([]byte(want.Response)))
}

func replay(t *testing.T, srv *rpc.Server, cases []fixtureCase) {
	for _, c := range cases {
		got := srv.Handle([]byte(c.Request))
		if !matches(got, c) {
			t.Fatalf("request %s want %d %s got %d %s", c.Request, c.Status, c.Response, got.Status, got.Body)
		}
	}
}

func TestRecordedMainnetCorpus(t *testing.T) {
	cases := loadCases("testdata/corpus.jsonl.gz")
	assert.Ok(len(cases) > 100, "corpus has cases")
	replay(t, rpc.NewServer(rpc.Config{}, loadChain(), nil, nil), cases)
}

func TestEnvelopeErrors(t *testing.T) {
	replay(t, rpc.NewServer(rpc.Config{}, rpc.NewMemChain(), nil, nil), loadCases("testdata/envelope.jsonl"))
}

func TestMethodsAreSorted(t *testing.T) {
	names := rpc.NewServer(rpc.Config{}, rpc.NewMemChain(), nil, nil).Methods()
	assert.Ok(len(names) > 50, "method table is populated")
	for i := range len(names) - 1 {
		assert.Ok(names[i] < names[i+1], "methods sorted and unique")
	}
}

func TestDebugExtensionMethods(t *testing.T) {
	replay(t, rpc.NewServer(rpc.Config{}, loadChain(), nil, nil), loadCases("testdata/debugext.jsonl"))
}
