package types_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
)

type txFixture struct {
	Net      string `json:"net"`
	Block    uint64 `json:"block"`
	Label    string `json:"label"`
	Envelope string `json:"envelope"`
	Hash     string `json:"hash"`
	From     string `json:"from"`
	FeePayer string `json:"feePayer"`
	KeyID    string `json:"keyId"`
}

type logFixture struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type receiptFixture struct {
	Type              string       `json:"type"`
	Status            string       `json:"status"`
	CumulativeGasUsed string       `json:"cumulativeGasUsed"`
	Logs              []logFixture `json:"logs"`
}

type blockFixture struct {
	Net              string           `json:"net"`
	Number           uint64           `json:"number"`
	Raw              string           `json:"raw"`
	Hash             string           `json:"hash"`
	TransactionsRoot string           `json:"transactionsRoot"`
	ReceiptsRoot     string           `json:"receiptsRoot"`
	Txs              []txFixture      `json:"txs"`
	Receipts         []receiptFixture `json:"receipts"`
}

func readLines[T any](t *testing.T, path string) []T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := primitives.DecodeHex(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustAddress(t *testing.T, s string) primitives.Address {
	t.Helper()
	a, err := primitives.HexToAddress(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustHash(t *testing.T, s string) primitives.Hash {
	t.Helper()
	h, err := primitives.HexToHash(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func checkTx(t *testing.T, tx types.Transaction, fx *txFixture) {
	t.Helper()
	if got := tx.Hash(); got != mustHash(t, fx.Hash) {
		t.Fatalf("%s: hash %s, want %s", fx.Hash, got, fx.Hash)
	}
	sender, err := tx.Sender()
	if err != nil {
		t.Fatalf("%s: sender: %v", fx.Hash, err)
	}
	if sender != mustAddress(t, fx.From) {
		t.Fatalf("%s: sender %s, want %s", fx.Hash, sender, fx.From)
	}
	tempo, ok := tx.Data.(*types.TempoTx)
	if !ok {
		return
	}
	payer, err := tempo.FeePayer(sender)
	if err != nil {
		t.Fatalf("%s: fee payer: %v", fx.Hash, err)
	}
	if payer != mustAddress(t, fx.FeePayer) {
		t.Fatalf("%s: fee payer %s, want %s", fx.Hash, payer, fx.FeePayer)
	}
	checkTempoExtras(t, tx, tempo, fx)
}

func checkTempoExtras(t *testing.T, tx types.Transaction, tempo *types.TempoTx, fx *txFixture) {
	t.Helper()
	if fx.KeyID != "" {
		key, err := tempo.Signature.KeyID(tx.SigningHash())
		if err != nil {
			t.Fatal(err)
		}
		if key != mustAddress(t, fx.KeyID) {
			t.Fatalf("%s: key id %s, want %s", fx.Hash, key, fx.KeyID)
		}
	}
	if tempo.KeyAuthorization != nil {
		signer, err := tempo.KeyAuthorization.Signer()
		if err != nil {
			t.Fatal(err)
		}
		if signer != mustAddress(t, fx.From) {
			t.Fatalf("%s: key authorization signer %s", fx.Hash, signer)
		}
	}
	for i := range tempo.AuthorizationList {
		if _, err := tempo.AuthorizationList[i].Authority(); err != nil {
			t.Fatalf("%s: authorization %d: %v", fx.Hash, i, err)
		}
	}
}

func TestRealTransactions(t *testing.T) {
	fixtures := readLines[txFixture](t, "testdata/txs.jsonl")
	if len(fixtures) < 15 {
		t.Fatalf("only %d tx fixtures", len(fixtures))
	}
	for i := range fixtures {
		fx := &fixtures[i]
		t.Run(fx.Label, func(t *testing.T) {
			envelope := mustHex(t, fx.Envelope)
			tx, err := types.DecodeTransaction(envelope)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if string(tx.Encode()) != string(envelope) {
				t.Fatal("re-encoding differs")
			}
			checkTx(t, tx, fx)
		})
	}
}

func TestRealBlocks(t *testing.T) {
	fixtures := readLines[blockFixture](t, "testdata/blocks.jsonl")
	for i := range fixtures {
		fx := &fixtures[i]
		raw := mustHex(t, fx.Raw)
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			t.Fatalf("%s %d: decode: %v", fx.Net, fx.Number, err)
		}
		if string(blk.Encode()) != string(raw) {
			t.Fatalf("%s %d: re-encoding differs", fx.Net, fx.Number)
		}
		if blk.Hash() != mustHash(t, fx.Hash) {
			t.Fatalf("%s %d: hash %s", fx.Net, fx.Number, blk.Hash())
		}
		if blk.Header.Inner.Number != fx.Number {
			t.Fatalf("%s %d: number %d", fx.Net, fx.Number, blk.Header.Inner.Number)
		}
		root := trie.OrderedRoot(blk.TransactionEnvelopes())
		if root != mustHash(t, fx.TransactionsRoot) {
			t.Fatalf("%s %d: transactions root %s", fx.Net, fx.Number, root)
		}
		if len(blk.Transactions) != len(fx.Txs) {
			t.Fatalf("%s %d: %d txs", fx.Net, fx.Number, len(blk.Transactions))
		}
		for k := range blk.Transactions {
			checkTx(t, blk.Transactions[k], &fx.Txs[k])
		}
		checkReceipts(t, &blk, fx)
	}
}

func receiptFrom(t *testing.T, fx *receiptFixture) types.Receipt {
	t.Helper()
	kind := mustHex(t, padQuantity(fx.Type))
	r := types.Receipt{Type: types.TxType(kind[len(kind)-1]), Success: fx.Status == "0x1"}
	for _, c := range mustHex(t, padQuantity(fx.CumulativeGasUsed)) {
		r.CumulativeGasUsed = r.CumulativeGasUsed<<8 | uint64(c)
	}
	for _, l := range fx.Logs {
		log := types.Log{Address: mustAddress(t, l.Address), Data: mustHex(t, l.Data)}
		for _, topic := range l.Topics {
			log.Topics = append(log.Topics, mustHash(t, topic))
		}
		r.Logs = append(r.Logs, log)
	}
	r.Bloom = types.LogsBloom(r.Logs)
	return r
}

func padQuantity(q string) string {
	digits := strings.TrimPrefix(q, "0x")
	if len(digits)%2 == 1 {
		digits = "0" + digits
	}
	return "0x" + digits
}

func checkReceipts(t *testing.T, blk *types.Block, fx *blockFixture) {
	t.Helper()
	encoded := make([][]byte, len(fx.Receipts))
	var bloom types.Bloom
	for i := range fx.Receipts {
		r := receiptFrom(t, &fx.Receipts[i])
		encoded[i] = r.Encode()
		back, err := types.DecodeReceipt(encoded[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(back.Encode()) != string(encoded[i]) {
			t.Fatal("receipt does not round-trip")
		}
		bloom.Or(&r.Bloom)
	}
	if bloom != blk.Header.Inner.LogsBloom {
		t.Fatalf("%s %d: logs bloom differs", fx.Net, fx.Number)
	}
	if root := trie.OrderedRoot(encoded); root != mustHash(t, fx.ReceiptsRoot) {
		t.Fatalf("%s %d: receipts root %s", fx.Net, fx.Number, root)
	}
}
