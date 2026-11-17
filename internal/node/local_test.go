package node_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/gakonst/goempo/internal/builder"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/txpool"
	"github.com/gakonst/goempo/internal/types"
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

func buildOn(l *node.Local, pending []*types.Block, view uint64) *types.Block {
	var out *types.Block
	pool := txpool.New(txpool.Config{Executor: l.Executor(), Now: func() uint64 { return 0 }})
	err := l.WithBranch(pending, func(st *state.State, parent *types.Block, getHash func(uint64) primitives.Hash) error {
		ctx := types.ConsensusContext{View: view, ParentView: view - 1}
		built, err := builder.New(pool).Build(st, l.Executor(), parent, builder.Attributes{
			TimestampMillis: (parent.Header.Inner.Timestamp + 1) * 1000,
			Consensus:       &ctx, GasLimit: parent.Header.Inner.GasLimit, GetHash: getHash,
		})
		if err != nil {
			return err
		}
		out = &built.Block
		return nil
	})
	assert.NoError(err, "build on branch")
	return out
}

func TestLocalExecutesOnPendingBranch(t *testing.T) {
	l, err := node.OpenLocal(node.LocalConfig{State: dirFS(t.TempDir()), Blocks: dirFS(t.TempDir()), Genesis: devnetGenesis()})
	assert.NoError(err, "open local")
	genesis := l.Head()
	b1 := buildOn(l, nil, 1)
	b2 := buildOn(l, []*types.Block{b1}, 2)
	assert.Equal(b2.Header.Inner.ParentHash, b1.Hash(), "built on the uncommitted block")
	assert.Equal(l.Head().Hash(), genesis.Hash(), "building a branch commits nothing")
	assert.NoError(l.ExecuteOn([]*types.Block{b1}, b2), "execute on the pending branch")
	assert.Ok(errors.Is(l.Execute(b2), node.ErrLocalParent), "the block does not extend the head")
	assert.Ok(errors.Is(l.ExecuteOn([]*types.Block{b2}, b1), node.ErrLocalParent), "a branch must link to the head")
	cfg, err := l.DkgConfigOn([]*types.Block{b1})
	assert.NoError(err, "dkg config on the branch")
	assert.Ok(cfg.Found, "validator config read through the branch")
	assert.NoError(l.Commit(b1), "commit first")
	assert.NoError(l.Commit(b2), "commit second")
	assert.Equal(l.Head().Hash(), b2.Hash(), "head")
	assert.NoError(l.Close(), "close")
}
