package main_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dev "github.com/gakonst/goempo/cmd/goempo-dev"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
	"go.uber.org/goleak"
)

const streamModelBlocks = 130

// Synthetic serial-model differential, not independent-client or performance
// evidence. Consecutive nonces and BLOCKHASH logs cross three decode windows.
func TestStreamingSignedModelEquivalence(t *testing.T) {
	defer goleak.VerifyNone(t)
	raws := streamModel(t)
	last, err := types.DecodeBlock(raws[len(raws)-1])
	if err != nil {
		t.Fatal(err)
	}
	dir, data := t.TempDir(), t.TempDir()
	fs, err := goio.NewDirFS(data)
	if err != nil {
		t.Fatal(err)
	}
	parent := hashModelParent(t, fs)
	if err := parent.db.Close(); err != nil {
		t.Fatal(err)
	}
	hashModelChunk(t, dir, raws)
	result := filepath.Join(t.TempDir(), "result.json")
	code := dev.Run([]string{"replay", "-dir", dir, "-datadir", data, "-to", fmt.Sprint(last.Header.Inner.Number), "-heal", "0", "-diagnose", "off", "-checkpoint", "0", "-result", result})
	if code != 0 {
		t.Fatalf("serial-model replay exit %d", code)
	}
	streamModelCheck(t, result, fs, last.Header.Inner.StateRoot, streamModelBlocks)
}

func TestStreamingSignedModelResume(t *testing.T) {
	defer goleak.VerifyNone(t)
	raws := streamModel(t)
	last, err := types.DecodeBlock(raws[len(raws)-1])
	if err != nil {
		t.Fatal(err)
	}
	dir, data := t.TempDir(), t.TempDir()
	fs, err := goio.NewDirFS(data)
	if err != nil {
		t.Fatal(err)
	}
	parent := hashModelParent(t, fs)
	if err := parent.db.Close(); err != nil {
		t.Fatal(err)
	}
	hashModelChunk(t, dir, raws)
	result := filepath.Join(t.TempDir(), "result.json")
	for _, to := range []uint64{65, streamModelBlocks + 1} {
		code := dev.Run([]string{"replay", "-dir", dir, "-datadir", data, "-to", fmt.Sprint(to), "-heal", "0", "-diagnose", "off", "-checkpoint", "0", "-result", result})
		if code != 0 {
			t.Fatalf("resumed serial-model replay to %d exit %d", to, code)
		}
	}
	streamModelCheck(t, result, fs, last.Header.Inner.StateRoot, streamModelBlocks+1-65)
}

func streamModel(t *testing.T) [][]byte {
	t.Helper()
	checkpoint := hashModelParent(t, goio.NewMemFS())
	db, parent := checkpoint.db, checkpoint.block
	g, err := genesis.Mainnet()
	if err != nil {
		t.Fatal(err)
	}
	gen := node.GenesisBlock(g)
	raws := [][]byte{gen.Encode(), parent.Encode()}
	x, hash := hashModelExecutor(t, g), parent.Hash()
	var senders [hashModelSigners]primitives.Address
	for n := range streamModelBlocks {
		block := types.Block{Header: parent.Header, HasWithdrawals: true}
		block.Header.Inner.Number = uint64(n + 2)
		block.Header.Inner.Timestamp += uint64(n + 1)
		block.Header.Inner.ParentHash = parent.Hash()
		for i := range hashModelSigners {
			var key secp256k1.PrivateKey
			key[len(key)-1] = byte(i + 1)
			legacy := &types.LegacyTx{Nonce: uint64(n), Gas: 1_000_000, To: &hashModelContract, V: 35 + 2*uint64(hardfork.MainnetChainID)}
			tx := types.Transaction{Data: legacy}
			sig, err := secp256k1.Sign(tx.SigningHash(), key)
			if err != nil {
				t.Fatal(err)
			}
			if sig.RecoveryID > 1 {
				t.Fatal("legacy model requires a one-bit recovery id")
			}
			legacy.R, legacy.S = sig.R, sig.S
			legacy.V += uint64(sig.RecoveryID)
			sender, err := tx.Sender()
			if err != nil {
				t.Fatal(err)
			}
			senders[i] = sender
			block.Transactions = append(block.Transactions, tx)
		}
		block.Header.Inner.TransactionsRoot = trie.OrderedRoot(block.TransactionEnvelopes())
		res, err := x.Execute(db.State(), executor.Input{Block: &block, GetHash: func(uint64) primitives.Hash { return hash }})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Receipts) != hashModelSigners {
			t.Fatal("model lost receipts")
		}
		for i, receipt := range res.Receipts {
			if !receipt.Success || len(receipt.Logs) != 1 {
				t.Fatalf("model block %d tx %d must succeed with one log", n+2, i)
			}
			log := receipt.Logs[0]
			if log.Address != hashModelContract || len(log.Topics) != 0 || !bytes.Equal(log.Data, hash[:]) {
				t.Fatalf("model block %d tx %d logged the wrong BLOCKHASH", n+2, i)
			}
			if got := db.State().Nonce(senders[i]); got != uint64(n+1) {
				t.Fatalf("model nonce %d want %d", got, n+1)
			}
		}
		block.Header.Inner.GasUsed, block.Header.Inner.ReceiptsRoot, block.Header.Inner.LogsBloom = res.GasUsed, res.ReceiptsRoot, res.Bloom
		block.Header.Inner.StateRoot, err = db.State().Commit(block.Header.Inner.Number)
		if err != nil {
			t.Fatal(err)
		}
		if block.Header.Inner.StateRoot == parent.Header.Inner.StateRoot {
			t.Fatal("signed model must change state")
		}
		raws = append(raws, block.Encode())
		parent = block
	}
	return raws
}

func streamModelCheck(t *testing.T, path string, fs goio.FS, root primitives.Hash, blocks uint64) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Height   uint64
		Target   uint64
		Mismatch string
		Bench    struct{ Blocks, Txs uint64 }
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Target != streamModelBlocks+1 || result.Mismatch != "" || result.Height != streamModelBlocks+1 || result.Bench.Blocks != blocks || result.Bench.Txs != blocks*hashModelSigners {
		t.Fatalf("lost or repeated streamed transactions: %+v", result)
	}
	db, err := state.Open(fs, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Head().Number != streamModelBlocks+1 || db.Head().Root != root {
		t.Fatalf("wrong persisted serial-model head: %+v", db.Head())
	}
	got, err := db.RebuildRoot()
	if err != nil || got != root {
		t.Fatalf("flat state differs from serial model: root=%s err=%v", got, err)
	}
}

const hashModelSigners = 6

var hashModelContract = primitives.Address{0x42, 0x01}

type hashModelCheckpoint struct {
	db    *state.DB
	block types.Block
}

func hashModelParent(t *testing.T, fs goio.FS) hashModelCheckpoint {
	t.Helper()
	db, err := state.Open(fs, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	g, err := genesis.Mainnet()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoadGenesis(g); err != nil {
		t.Fatal(err)
	}
	gen := node.GenesisBlock(g)
	parent := types.Block{Header: gen.Header, HasWithdrawals: true}
	parent.Header.GeneralGasLimit = g.GasLimit
	parent.Header.SharedGasLimit = g.GasLimit
	parent.Header.Inner.Number = 1
	parent.Header.Inner.Timestamp = 1_779_112_801
	parent.Header.Inner.ParentHash = gen.Hash()
	parent.Header.Inner.BaseFeePerGas = 0
	parent.Header.Inner.ExtraData = nil
	parent.Header.Inner.Nonce = [types.NonceLength]byte{}
	// PUSH1 1; BLOCKHASH; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; LOG0; STOP.
	db.State().SetCode(hashModelContract, []byte{0x60, 0x01, 0x40, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xa0, 0x00})
	db.State().SetNonce(hashModelContract, 1)
	db.State().FinalizeTx()
	x := hashModelExecutor(t, g)
	res, err := x.Execute(db.State(), executor.Input{Block: &parent, GetHash: func(uint64) primitives.Hash { return primitives.Hash{} }})
	if err != nil {
		t.Fatal(err)
	}
	parent.Header.Inner.GasUsed = res.GasUsed
	parent.Header.Inner.ReceiptsRoot = res.ReceiptsRoot
	parent.Header.Inner.LogsBloom = res.Bloom
	parent.Header.Inner.StateRoot, err = db.State().Commit(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	return hashModelCheckpoint{db: db, block: parent}
}

func hashModelExecutor(t *testing.T, g *genesis.Genesis) *executor.Executor {
	t.Helper()
	var cfg struct {
		EpochLength uint64 `json:"epochLength"`
	}
	if err := json.Unmarshal(g.Config.Raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return &executor.Executor{ChainID: hardfork.MainnetChainID, EpochLength: cfg.EpochLength, Configs: chain.NewCache()}
}

func hashModelChunk(t *testing.T, dir string, raws [][]byte) {
	t.Helper()
	var chunk bytes.Buffer
	for _, raw := range raws {
		if err := binary.Write(&chunk, binary.BigEndian, uint32(len(raw))); err != nil {
			t.Fatal(err)
		}
		chunk.Write(raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "0000000000.blk"), chunk.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingSignedModelBoundaries(t *testing.T) {
	defer goleak.VerifyNone(t)
	raws := streamModel(t)
	for _, target := range []int{63, 64, 65, 127, 128, 129, 131} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			streamBoundaryCase(t, raws, target)
		})
	}
}

func streamBoundaryCase(t *testing.T, raws [][]byte, target int) {
	t.Helper()
	for _, failure := range []string{"none", "root", "decode"} {
		t.Run(failure, func(t *testing.T) {
			altered := append([][]byte(nil), raws...)
			switch failure {
			case "none":
			case "root":
				block, err := types.DecodeBlock(altered[target])
				if err != nil {
					t.Fatal(err)
				}
				block.Header.Inner.StateRoot[0] ^= 1
				altered[target] = block.Encode()
			case "decode":
				altered[target] = []byte{0xc0}
			}
			dir, data := t.TempDir(), t.TempDir()
			fs, err := goio.NewDirFS(data)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := hashModelParent(t, fs)
			if err := checkpoint.db.Close(); err != nil {
				t.Fatal(err)
			}
			hashModelChunk(t, dir, altered)
			result := filepath.Join(t.TempDir(), "result.json")
			code := dev.Run([]string{"replay", "-dir", dir, "-datadir", data, "-to", fmt.Sprint(target), "-heal", "0", "-diagnose", "off", "-checkpoint", "0", "-result", result})
			raw, err := os.ReadFile(result)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Height, Target uint64
				Mismatch       string
				Bench          struct{ Blocks, Txs uint64 }
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			wantHeight, wantCode := uint64(target), 0
			if failure != "none" {
				wantHeight--
			}
			if failure == "root" {
				wantCode = 1
			}
			if code != wantCode || got.Height != wantHeight || got.Target != uint64(target) || got.Bench.Blocks != wantHeight-1 || got.Bench.Txs != (wantHeight-1)*hashModelSigners {
				t.Fatalf("boundary %s: exit=%d result=%+v", failure, code, got)
			}
			if failure == "root" && !strings.HasPrefix(got.Mismatch, fmt.Sprintf("block %d: stateRoot:", target)) {
				t.Fatalf("wrong mismatch: %+v", got)
			}
			if failure != "root" && got.Mismatch != "" {
				t.Fatalf("unexpected mismatch: %+v", got)
			}
		})
	}
}
