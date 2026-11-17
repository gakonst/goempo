package precompiles_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/kapetan-io/tiger/assert"
)

type statelessFrame struct {
	Source    string `json:"source"`
	Timestamp uint64 `json:"timestamp"`
	From      string `json:"from"`
	To        string `json:"to"`
	Input     string `json:"input"`
	Gas       uint64 `json:"gas"`
	GasUsed   uint64 `json:"gasUsed"`
	Output    string `json:"output"`
	Reverted  bool   `json:"reverted"`
}

func TestStatelessFrames(t *testing.T) {
	paths, err := filepath.Glob("testdata/frames/*.json")
	assert.NoError(err, "glob frames")
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) { replayFrames(p) })
	}
}

func replayFrames(path string) {
	raw, err := os.ReadFile(path)
	assert.NoError(err, "read frames")
	var frames []statelessFrame
	assert.NoError(json.Unmarshal(raw, &frames), "parse frames")
	{
		for _, f := range frames {
			fork := hardfork.At(hardfork.MainnetChainID, f.Timestamp)
			c := precompiles.NewContext(memstate.New(), fork, f.Gas)
			c.Block.Time = f.Timestamp
			c.Caller = mustAddr(f.From)
			c.Static = true
			out, err := precompiles.Call(c, mustAddr(f.To), mustBytes(f.Input))
			assert.Equal(err != nil, f.Reverted, "revert status "+f.Source)
			assert.Equal(c.GasUsed(), f.GasUsed, "gas used "+f.Source)
			if err == nil {
				assert.Equal(hex.EncodeToString(out), strings.TrimPrefix(f.Output, "0x"), "output "+f.Source)
			}
		}
	}
}
