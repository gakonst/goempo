package node_test

import (
	"os"
	"slices"
	"testing"

	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/kapetan-io/tiger/assert"
)

const (
	mainnetGenesisHash = "0x49d7ec7085e77bf5a403d0fcb4cfc42a4084a89dfff60477579c5e09c9e03c54"
	devnetGenesisHash  = "0x1b259d3700d345c7010ac503d054dc90930e2ffa5caa3aa32d72ed1f34472264"
)

func TestGenesisBlockMainnet(_ *testing.T) {
	g, err := genesis.Mainnet()
	assert.NoError(err, "mainnet genesis")
	b := node.GenesisBlock(g)
	assert.Equal(b.Hash().String(), mainnetGenesisHash, "genesis block hash matches mainnet block 0")
}

func devnetGenesis() *genesis.Genesis {
	raw, err := os.ReadFile("testdata/devnet_genesis.json")
	assert.NoError(err, "read devnet genesis")
	g, err := genesis.Parse(raw)
	assert.NoError(err, "parse devnet genesis")
	return g
}

func dirFS(dir string) goio.FS {
	fs, err := goio.NewDirFS(dir)
	assert.NoError(err, "dir fs")
	return fs
}

func TestLocalDevnetGenesis(t *testing.T) {
	g := devnetGenesis()
	gb := node.GenesisBlock(g)
	assert.Equal(gb.Hash().String(), devnetGenesisHash, "genesis hash matches the Rust devnet")
	stateDir, blockDir := t.TempDir(), t.TempDir()
	l, err := node.OpenLocal(node.LocalConfig{State: dirFS(stateDir), Blocks: dirFS(blockDir), Genesis: g})
	assert.NoError(err, "open local")
	assert.Equal(l.Head().Hash().String(), devnetGenesisHash, "head is genesis")
	assert.Equal(l.Executor().EpochLength, uint64(60), "epoch length from genesis config")
	cfg, err := l.DkgConfig()
	assert.NoError(err, "dkg config")
	assert.Ok(cfg.Found, "validator config v2 is initialized at genesis")
	outcome, err := dkg.DecodeOutcome(g.ExtraData)
	assert.NoError(err, "genesis outcome")
	keys := make([]dkg.PublicKey, 0, len(cfg.ActiveValidators))
	for _, v := range cfg.ActiveValidators {
		assert.Ok(v.IsDkgPlayer(), "devnet validators are DKG players")
		keys = append(keys, dkg.PublicKey(v.PublicKey))
	}
	slices.SortFunc(keys, func(a, b dkg.PublicKey) int { return slices.Compare(a[:], b[:]) })
	assert.Equal(len(keys), len(outcome.NextPlayers), "active validators are the genesis next players")
	for i, k := range keys {
		assert.Equal(k, outcome.NextPlayers[i], "next player")
	}
	assert.NoError(l.Close(), "close")
	again, err := node.OpenLocal(node.LocalConfig{State: dirFS(stateDir), Blocks: dirFS(blockDir), Genesis: g})
	assert.NoError(err, "reopen")
	b, ok := again.BlockByNumber(0)
	assert.Ok(ok, "genesis block stored")
	assert.Equal(b.Hash().String(), devnetGenesisHash, "stored genesis")
	assert.NoError(again.Close(), "close again")
}
