package executor_test

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	intrinsicVectorsMax = 1 << 10
	intrinsicLineMax    = 1 << 20
)

const _ = uint(intrinsicLineMax - intrinsicVectorsMax)

type intrinsicVector struct {
	Block     uint64 `json:"block"`
	Time      uint64 `json:"time"`
	Cat       string `json:"cat"`
	Raw       string `json:"raw"`
	Intrinsic uint64 `json:"intrinsic"`
}

func TestIntrinsicGasMatchesMainnet(t *testing.T) {
	data, err := os.ReadFile("testdata/intrinsic-mainnet.jsonl")
	assert.NoError(err, "read vectors")
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 0, intrinsicLineMax), intrinsicLineMax)
	count := 0
	for count = 0; count < intrinsicVectorsMax; count++ {
		if !scan.Scan() {
			break
		}
		var v intrinsicVector
		assert.NoError(json.Unmarshal(scan.Bytes(), &v), "parse vector")
		raw, err := hex.DecodeString(v.Raw)
		assert.NoError(err, "decode raw")
		tx, err := types.DecodeTransaction(raw)
		assert.NoError(err, "decode tx")
		rules := chain.RulesAt(hardfork.MainnetChainID, v.Time)
		got, err := executor.IntrinsicGas(&tx, rules)
		assert.NoError(err, "intrinsic gas")
		assert.Equal(got.Gas, v.Intrinsic, v.Cat)
	}
	assert.Ok(count >= 30, "vectors cover the transaction kinds")
}
