package rpc_test

import (
	"fmt"
	"testing"

	"github.com/gakonst/goempo/internal/rpc"
	"github.com/kapetan-io/tiger/assert"
)

func TestTraceTypesAndFilterValidation(t *testing.T) {
	chain := loadChain()
	srv := rpc.NewServer(rpc.Config{}, chain, nil, nil)
	bad := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"trace_replayBlockTransactions","params":["0x1",["bogus"]]}`)
	e := bad["error"].(map[string]any)
	assert.Equal(e["code"], any(float64(-32602)), "unknown trace type rejected")
	head := chain.Head()
	wide := call(t, srv, fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"trace_filter","params":[{"fromBlock":"0x0","toBlock":"0x%x"}]}`, head))
	if head > 100 {
		assert.Equal(wide["error"].(map[string]any)["message"], any("Block range too large; currently limited to 100 blocks"), "range limit")
	}
	above := call(t, srv, fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"trace_filter","params":[{"fromBlock":"0x%x","toBlock":"0x%x"}]}`, head, head+5))
	assert.Ok(above["error"] != nil, "a bound above the head is an error")
	inverted := call(t, srv, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"trace_filter","params":[{"fromBlock":"0x%x","toBlock":"0x%x"}]}`, head, head-1))
	assert.Equal(inverted["error"].(map[string]any)["message"], any("invalid parameters: fromBlock cannot be greater than toBlock"), "inverted range")
}
