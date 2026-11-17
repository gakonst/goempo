package precompiles_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type vcv2Log struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type vcv2Case struct {
	Name      string                       `json:"name"`
	Block     uint64                       `json:"block"`
	Timestamp uint64                       `json:"timestamp"`
	Caller    string                       `json:"caller"`
	Static    bool                         `json:"static"`
	Input     string                       `json:"input"`
	Gas       uint64                       `json:"gas"`
	GasUsed   uint64                       `json:"gasUsed"`
	Output    string                       `json:"output"`
	Reverted  bool                         `json:"reverted"`
	Pre       map[string]map[string]string `json:"pre"`
	Post      map[string]map[string]string `json:"post"`
	Logs      []vcv2Log                    `json:"logs"`
}

func TestValidatorConfigV2MainnetCalls(t *testing.T) {
	paths, err := filepath.Glob("testdata/vcv2/*.json")
	assert.NoError(err, "glob vcv2 fixtures")
	assert.Ok(len(paths) > 0, "vcv2 fixtures present")
	for _, p := range paths {
		for _, c := range loadVCV2Cases(p) {
			t.Run(filepath.Base(p)+"/"+c.Name, func(t *testing.T) { runVCV2Case(c) })
		}
	}
}

func loadVCV2Cases(path string) []vcv2Case {
	raw, err := os.ReadFile(path)
	assert.NoError(err, "read vcv2 fixture")
	var cases []vcv2Case
	assert.NoError(json.Unmarshal(raw, &cases), "parse vcv2 fixture")
	return cases
}

func runVCV2Case(fx vcv2Case) {
	st := memstate.New()
	for _, a := range slices.Sorted(maps.Keys(fx.Pre)) {
		st.SetCode(mustAddr(a), []byte{0xef})
		for _, k := range slices.Sorted(maps.Keys(fx.Pre[a])) {
			st.SetStorage(mustAddr(a), mustHash(k), mustWord(fx.Pre[a][k]))
		}
	}
	st.FinalizeTx()
	c := precompiles.NewContext(st, hardfork.At(hardfork.MainnetChainID, fx.Timestamp), fx.Gas)
	c.Block.Number = fx.Block
	c.Block.Time = fx.Timestamp
	c.ChainID = u256.FromUint64(uint64(hardfork.MainnetChainID))
	c.Caller = mustAddr(fx.Caller)
	c.Static = fx.Static
	out, err := precompiles.Call(c, precompiles.ValidatorConfigV2Address, mustBytes(fx.Input))
	used := c.GasUsed()
	var rev *precompiles.Revert
	switch {
	case errors.As(err, &rev):
		out = rev.Data
	case err != nil:
		used = fx.Gas
	}
	assert.Equal(err != nil, fx.Reverted, "revert status "+fx.Name)
	assert.Equal(used, fx.GasUsed, "gas used "+fx.Name)
	assert.Equal(hex.EncodeToString(out), strings.TrimPrefix(fx.Output, "0x"), "output "+fx.Name)
	if fx.Reverted {
		return
	}
	_, err = precompiles.SettleStorageCreditRefunds(c)
	assert.NoError(err, "settle credits "+fx.Name)
	for _, a := range slices.Sorted(maps.Keys(fx.Post)) {
		for _, k := range slices.Sorted(maps.Keys(fx.Post[a])) {
			assert.Equal(st.Storage(mustAddr(a), mustHash(k)), mustWord(fx.Post[a][k]), "storage "+fx.Name+" "+a+" "+k)
		}
	}
	logs := st.Logs()
	assert.Equal(len(logs), len(fx.Logs), "log count "+fx.Name)
	for i, l := range fx.Logs {
		assert.Equal(logs[i].Address, mustAddr(l.Address), "log address "+fx.Name)
		assert.Equal(len(logs[i].Topics), len(l.Topics), "topic count "+fx.Name)
		for j, tp := range l.Topics {
			assert.Equal(logs[i].Topics[j], mustHash(tp), "topic "+fx.Name)
		}
		assert.Equal(hex.EncodeToString(logs[i].Data), strings.TrimPrefix(l.Data, "0x"), "log data "+fx.Name)
	}
}
