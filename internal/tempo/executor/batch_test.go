package executor_test

import (
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type prestateAccount struct {
	Balance string            `json:"balance"`
	Nonce   uint64            `json:"nonce"`
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

func hexBytes(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	assert.NoError(err, "hex")
	return b
}

func hexWord(t *testing.T, s string) u256.Int {
	if s == "" {
		return u256.Int{}
	}
	v, err := u256.FromHex(s)
	assert.NoError(err, "hex word")
	return v
}

func loadPrestate(t *testing.T, path string) *memstate.State {
	data, err := os.ReadFile(path)
	assert.NoError(err, "read prestate")
	var accounts map[string]prestateAccount
	assert.NoError(json.Unmarshal(data, &accounts), "decode prestate")
	st := memstate.New()
	for _, addr := range slices.Sorted(maps.Keys(accounts)) {
		a := accounts[addr]
		storage := make(map[primitives.Hash]u256.Int, len(a.Storage))
		for _, k := range slices.Sorted(maps.Keys(a.Storage)) {
			storage[primitives.Hash(hexBytes(t, k))] = hexWord(t, a.Storage[k])
		}
		st.Put(primitives.Address(hexBytes(t, addr)), memstate.Account{
			Balance: hexWord(t, a.Balance),
			Nonce:   a.Nonce,
			Code:    hexBytes(t, a.Code),
			Storage: storage,
		})
	}
	return st
}

func TestBatchWarmsLaterTargetsOnlyWhenTheirCallStarts(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-12964402.blk")
	assert.Equal(len(blocks), 1, "one block")
	tx := &blocks[0].Transactions[0]
	sender, err := tx.Sender()
	assert.NoError(err, "sender")
	st := loadPrestate(t, "testdata/mainnet-12964402-tx0-prestate.json")
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	out, err := x.ApplyCall(st, &blocks[0], tx, executor.Simulation{Sender: sender})
	assert.NoError(err, "apply")
	assert.Ok(out.Success, "succeeds")
	assert.Equal(out.GasUsed, uint64(2_087_728), "pathUSD is cold when the first call touches it")
}

func TestKeychainTxChecksCallScopesFromT3(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-16986086.blk")
	assert.Equal(len(blocks), 1, "one block")
	tx := &blocks[0].Transactions[0]
	sender, err := tx.Sender()
	assert.NoError(err, "sender")
	st := loadPrestate(t, "testdata/mainnet-16986086-tx0-prestate.json")
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	out, err := x.ApplyCall(st, &blocks[0], tx, executor.Simulation{Sender: sender})
	assert.NoError(err, "apply")
	assert.Ok(out.Success, "succeeds")
	assert.Equal(out.GasUsed, uint64(59_274), "the call scope check reads the key's scoped flag")
}

func TestTwoDimensionalNonceCreateFromFreshSenderPaysNewAccount(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-18337286.blk")
	assert.Equal(len(blocks), 1, "one block")
	tx := &blocks[0].Transactions[0]
	sender, err := tx.Sender()
	assert.NoError(err, "sender")
	st := loadPrestate(t, "testdata/mainnet-18337286-tx0-prestate.json")
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	out, err := x.ApplyCall(st, &blocks[0], tx, executor.Simulation{Sender: sender})
	assert.NoError(err, "apply")
	assert.Ok(out.Success, "succeeds")
	assert.Equal(out.GasUsed, uint64(959_243), "a CREATE under a 2D nonce from a sender with nonce 0 pays new-account gas")
}
