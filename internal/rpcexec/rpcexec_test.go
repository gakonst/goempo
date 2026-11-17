package rpcexec_test

import (
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/rpcexec"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	genesisGasLimit = 500_000_000
	gasCap          = 50_000_000
)

const _ = uint(genesisGasLimit - gasCap)

type genesisState struct{ st *state.State }

func (g genesisState) ExecState(uint64) (rpcexec.State, error) { return g.st, nil }

func genesisExecutor() *rpcexec.Executor {
	g, err := genesis.Mainnet()
	assert.NoError(err, "mainnet genesis")
	db, err := state.Open(goio.NewMemFS(), state.Options{})
	assert.NoError(err, "open state")
	_, err = db.LoadGenesis(g)
	assert.NoError(err, "load genesis")
	var blk types.Block
	blk.Header.Inner.GasLimit = genesisGasLimit
	data, err := rpc.NewBlockData(&blk, []byte{0xc0}, nil)
	assert.NoError(err, "block data")
	chain := rpc.NewMemChain()
	chain.Add(data)
	return &rpcexec.Executor{ChainID: hardfork.MainnetChainID, Chain: chain, States: genesisState{st: db.State()}}
}

func TestCallTIP20Name(t *testing.T) {
	x := genesisExecutor()
	to := primitives.Address{0x20, 0xc0}
	res, err := x.Call(0, &rpc.CallRequest{To: &to, Input: []byte{0x06, 0xfd, 0xde, 0x03}}, gasCap)
	assert.NoError(err, "call")
	assert.Ok(!res.Reverted, "name() does not revert")
	assert.Ok(strings.Contains(string(res.Return), "pathUSD"), "pathUSD name")
	gas, err := x.EstimateGas(0, &rpc.CallRequest{To: &to, Input: []byte{0x06, 0xfd, 0xde, 0x03}}, gasCap)
	assert.NoError(err, "estimate")
	assert.Ok(gas > res.GasUsed-1, "estimate covers the gas the call used")
	t.Log(gas, res.GasUsed)
}
