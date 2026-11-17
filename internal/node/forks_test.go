package node_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/genesis"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

const (
	t13DevnetChain = 991337
	t13Time        = 0
	afterT13       = 1_900_000_000
)

func TestLocalRegistersGenesisForks(t *testing.T) {
	g, err := genesis.Parse([]byte(`{"config":{"chainId":991337,"epochLength":60,"t0Time":0,"t1aTime":0,"t13Time":0,"t14Time":null},"alloc":{}}`))
	assert.NoError(err, "parse")
	l, err := node.OpenLocal(node.LocalConfig{State: dirFS(t.TempDir()), Blocks: dirFS(t.TempDir()), Genesis: g})
	assert.NoError(err, "open local")
	assert.Equal(hardfork.At(t13DevnetChain, afterT13), hardfork.T13, "forks absent from the genesis config stay unscheduled")
	at, ok := hardfork.Activation(t13DevnetChain, hardfork.T1A)
	assert.Ok(ok, "t1aTime is read")
	assert.Equal(at, uint64(t13Time), "t1a activation")
	_, ok = hardfork.Activation(t13DevnetChain, hardfork.T14)
	assert.Ok(!ok, "null t14Time is unscheduled")
	assert.NoError(l.Close(), "close")
	assert.Ok(hardfork.Register(hardfork.MainnetChainID, hardfork.NewSchedule()) == hardfork.ErrBuiltinChain, "mainnet schedule is fixed")
}
