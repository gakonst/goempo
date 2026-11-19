package executor_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

func TestBatchSenderScratchMainnetOracle(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-user-3211-3857.blk")
	db := openGenesis(t)
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	zero := func(uint64) primitives.Hash { return primitives.Hash{} }
	for i := range blocks {
		blk := &blocks[i]
		batch := executor.RecoverSenders(blk.Transactions)
		for j := range blk.Transactions {
			single := executor.RecoverSender(&blk.Transactions[j])
			if batch[j].Address != single.Address || !errors.Is(batch[j].Err, single.Err) {
				t.Fatal("batch recovery differs from independent recovery")
			}
		}
		result, err := x.Execute(db.State(), executor.Input{Block: blk, GetHash: zero, Senders: batch})
		if err != nil {
			t.Fatal(err)
		}
		if result.ReceiptsRoot != blk.Header.Inner.ReceiptsRoot || result.Bloom != blk.Header.Inner.LogsBloom || result.GasUsed != blk.Header.Inner.GasUsed {
			t.Fatal("batch recovery mainnet execution oracle")
		}
		root, err := db.State().Commit(blk.Header.Inner.Number)
		if err != nil || root != blk.Header.Inner.StateRoot {
			t.Fatal("batch recovery mainnet state oracle", err)
		}
	}
}

func TestBatchSenderScratchRejectsChangedTransaction(t *testing.T) {
	blocks := readBlocks(t, "testdata/mainnet-user-3211-3857.blk")
	for i := range blocks {
		txs := blocks[i].Transactions
		before := executor.RecoverSenders(txs)
		for j := range txs {
			if executor.IsSystemTx(&txs[j]) {
				continue
			}
			raw := txs[j].Encode()
			raw[len(raw)-1] ^= 0x7f
			changed, err := types.DecodeTransaction(raw)
			if err != nil {
				continue
			}
			pair := []types.Transaction{changed, txs[j]}
			batch := executor.RecoverSenders(pair)
			single := executor.RecoverSender(&changed)
			if batch[0].Address != single.Address || !errors.Is(batch[0].Err, single.Err) {
				t.Fatal("changed transaction reused original sender")
			}
			if batch[1].Address != before[j].Address || !errors.Is(batch[1].Err, before[j].Err) {
				t.Fatal("recovery scratch polluted later transaction")
			}
			return
		}
	}
	t.Fatal("fixture contains no mutable transaction")
}

func TestBatchSenderScratchMixedVariantsAndInvalid(t *testing.T) {
	raw, err := os.ReadFile("../../types/testdata/txs.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var txs []types.Transaction
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var fx struct {
			Envelope string `json:"envelope"`
		}
		if err := json.Unmarshal(line, &fx); err != nil {
			t.Fatal(err)
		}
		encoded, err := primitives.DecodeHex(fx.Envelope)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := types.DecodeTransaction(encoded)
		if err != nil {
			t.Fatal(err)
		}
		txs = append(txs, tx)
	}
	txs = append(txs, types.Transaction{Data: &types.TempoTx{}})
	txs = append(txs, types.Transaction{Data: &types.DynamicFeeTx{}})
	for worker := 0; worker < 4; worker++ {
		t.Run(string(rune('A'+worker)), func(t *testing.T) {
			t.Parallel()
			got := executor.RecoverSenders(txs)
			for i := range txs {
				want := executor.RecoverSender(&txs[i])
				if got[i].Address != want.Address || !errors.Is(got[i].Err, want.Err) {
					t.Fatalf("mixed recovery differs at %d", i)
				}
			}
			if got[len(got)-1].Err == nil || got[len(got)-2].Err == nil {
				t.Fatal("invalid signature was accepted")
			}
		})
	}
}
