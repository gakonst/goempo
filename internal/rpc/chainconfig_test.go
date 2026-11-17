package rpc_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/rpc"
	"github.com/kapetan-io/tiger/assert"
)

func TestChainConfigMatchesReference(t *testing.T) {
	raw, err := os.ReadFile("testdata/chainconfig.json")
	assert.NoError(err, "read fixture")
	var ref struct {
		ChainConfig map[string]any `json:"debug_chainConfig"`
		EthConfig   map[string]any `json:"eth_config"`
	}
	assert.NoError(json.Unmarshal(raw, &ref), "decode fixture")
	srv := rpc.NewServer(rpc.Config{ChainID: rpc.MainnetChainID}, loadChain(), nil, nil)
	got := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"debug_chainConfig","params":[]}`)["result"].(map[string]any)
	delete(got, "t14Time")
	a, err := json.Marshal(got)
	assert.NoError(err, "encode ours")
	b, err := json.Marshal(ref.ChainConfig)
	assert.NoError(err, "encode reference")
	assert.Equal(string(a), string(b), "chain config equals v1.18.4's apart from the newer T14 entry")
	cfg := call(t, srv, `{"jsonrpc":"2.0","id":2,"method":"eth_config","params":[]}`)["result"].(map[string]any)
	assert.Ok(cfg["current"] != nil, "current fork present")
}
