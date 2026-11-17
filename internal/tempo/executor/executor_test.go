package executor_test

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	fixtureBlocksMax = 1 << 10
	blockBytesMax    = 1 << 22
)

const _ = uint(blockBytesMax - fixtureBlocksMax)

func readBlocks(t *testing.T, path string) []types.Block {
	data, err := os.ReadFile(path)
	assert.NoError(err, "read fixture")
	blocks := make([]types.Block, 0, fixtureBlocksMax)
	count := 0
	for count = 0; count < fixtureBlocksMax; count++ {
		if len(data) == 0 {
			break
		}
		n := int(binary.BigEndian.Uint32(data))
		assert.Ok(n <= blockBytesMax, "block size is bounded")
		b, err := types.DecodeBlock(data[4 : 4+n])
		assert.NoError(err, "decode block")
		blocks = append(blocks, b)
		data = data[4+n:]
	}
	assert.Equal(len(data), 0, "fixture read to its end")
	return blocks
}

func openGenesis(t *testing.T) *state.DB {
	db, err := state.Open(goio.NewMemFS(), state.Options{})
	assert.NoError(err, "open state")
	g, err := genesis.Mainnet()
	assert.NoError(err, "mainnet genesis")
	_, err = db.LoadGenesis(g)
	assert.NoError(err, "load genesis")
	return db
}

func TestMainnetBlocksOverGenesis(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-0-40.blk")
	assert.Equal(len(blocks), 41, "fixture holds blocks 0..40")
	db := openGenesis(t)
	assert.Equal(db.Head().Root, blocks[0].Header.Inner.StateRoot, "genesis root")
	hashes := make([]primitives.Hash, len(blocks))
	for i := range blocks {
		hashes[i] = blocks[i].Header.Hash()
	}
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	for i := 1; i < len(blocks); i++ {
		h := &blocks[i].Header.Inner
		assert.Equal(h.ParentHash, hashes[i-1], "parent hash links")
		res, err := x.Execute(db.State(), executor.Input{
			Block:   &blocks[i],
			GetHash: func(n uint64) primitives.Hash { return hashes[n] },
		})
		assert.NoError(err, "execute block")
		assert.Equal(res.GasUsed, h.GasUsed, "gas used")
		assert.Equal(res.ReceiptsRoot, h.ReceiptsRoot, "receipts root")
		assert.Equal(res.Bloom, h.LogsBloom, "logs bloom")
		root, err := db.State().Commit(h.Number)
		assert.NoError(err, "commit")
		assert.Equal(root, h.StateRoot, "state root")
	}
}

func TestSystemTxMustBeLast(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-0-40.blk")
	b := blocks[1]
	assert.Ok(executor.IsSystemTx(&b.Transactions[len(b.Transactions)-1]), "block 1 ends with the system tx")
	b.Transactions = append(b.Transactions, b.Transactions[len(b.Transactions)-1])
	db := openGenesis(t)
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	_, err := x.Execute(db.State(), executor.Input{Block: &b, GetHash: func(uint64) primitives.Hash { return primitives.Hash{} }})
	assert.Ok(errors.Is(err, executor.ErrSystemTxPlace), "a second system tx is not last")
}
