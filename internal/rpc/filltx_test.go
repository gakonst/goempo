package rpc_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/rpc"
	"github.com/kapetan-io/tiger/assert"
)

func TestFillTransactionEncodesLikeReference(t *testing.T) {
	srv := rpc.NewServer(rpc.Config{ChainID: rpc.MainnetChainID}, loadChain(), nil, nil)
	got := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"eth_fillTransaction","params":[{"from":"0x894f2ca2a05f03e3f6731ceff5bf589ec019559c","to":"0x20c00000000000000000000016c6514b53947fdc","data":"0x18160ddd","nonce":"0x5","gas":"0x5c4f","maxFeePerGas":"0x47868c00","maxPriorityFeePerGas":"0x0"}]}`)
	res, ok := got["result"].(map[string]any)
	assert.Ok(ok, "fill returns an object")
	assert.Equal(res["raw"], any("0x02ec82107905808447868c00825c4f9420c00000000000000000000016c6514b53947fdc808418160dddc0808080"), "raw matches mainnet node")
	tx := res["tx"].(map[string]any)
	assert.Equal(tx["hash"], any("0xc0249ee8207fa9b7d81c5a34477a69e5dace78148d3fdc3e318902e7f0d5c89b"), "hash matches mainnet node")
	missing := call(t, srv, `{"jsonrpc":"2.0","id":2,"method":"eth_fillTransaction","params":[{}]}`)
	assert.Equal(missing["error"].(map[string]any)["message"], any("unknown account"), "from is required")
}
