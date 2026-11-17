package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
)

const (
	failuresShown = 20
	percentScale  = 100
)

type txJSON struct {
	Hash      string `json:"hash"`
	From      string `json:"from"`
	Type      string `json:"type"`
	Signature struct {
		KeyID string `json:"keyId"`
	} `json:"signature"`
}

type blockJSON struct {
	Number           string        `json:"number"`
	Hash             string        `json:"hash"`
	Timestamp        string        `json:"timestamp"`
	TransactionsRoot string        `json:"transactionsRoot"`
	Transactions     []txJSON      `json:"transactions"`
	Receipts         []receiptJSON `json:"-"`
}

type forkScore struct {
	Blocks int `json:"blocks"`
	Pass   int `json:"pass"`
	Txs    int `json:"txs"`
}

type r1Result struct {
	Rung      string                `json:"rung"`
	Score     string                `json:"score"`
	Blocks    int                   `json:"blocks"`
	Pass      int                   `json:"pass"`
	Txs       int                   `json:"txs"`
	Senders   int                   `json:"senders_ok"`
	Receipts  int                   `json:"receipts_ok"`
	Sponsored int                   `json:"fee_payers_recovered"`
	KeyIDs    int                   `json:"keychain_key_ids_ok"`
	KeyAuths  int                   `json:"key_authorizations_ok"`
	AAAuths   int                   `json:"aa_authorizations_recovered"`
	Forks     map[string]*forkScore `json:"forks"`
	TxTypes   map[string]int        `json:"tx_types"`
	SigTypes  map[string]int        `json:"sig_types"`
	Failures  []string              `json:"failures,omitempty"`
	FailCount int                   `json:"fail_count"`
}

func runScore(args []string) int {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	dir := fs.String("dir", defaultBlockDir(), "cache directory with fetched blocks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	res, err := scoreR1(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return 1
	}
	line, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		return 1
	}
	fmt.Println(string(line))
	if res.Pass != res.Blocks {
		return 1
	}
	return 0
}

func scoreR1(dir string) (*r1Result, error) {
	blocks, err := loadCache(dir)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, errors.New("no cached blocks; run goempo-dev fetch first")
	}
	res := &r1Result{Rung: "R1", Forks: map[string]*forkScore{}, TxTypes: map[string]int{}, SigTypes: map[string]int{}}
	for i := range blocks {
		res.add(&blocks[i])
	}
	res.Score = fmt.Sprintf("%.2f%%", float64(res.Pass)*percentScale/float64(res.Blocks))
	return res, nil
}

func (res *r1Result) add(b *cachedBlock) {
	res.Blocks++
	out, err := res.checkBlock(b)
	fs := res.Forks[out.fork]
	if fs == nil {
		fs = &forkScore{}
		res.Forks[out.fork] = fs
	}
	fs.Blocks++
	fs.Txs += out.txs
	res.Txs += out.txs
	if err != nil {
		res.FailCount++
		if len(res.Failures) < failuresShown {
			res.Failures = append(res.Failures, fmt.Sprintf("block %d: %v", b.Number, err))
		}
		return
	}
	fs.Pass++
	res.Pass++
}

type blockOutcome struct {
	fork string
	txs  int
}

func (res *r1Result) checkBlock(b *cachedBlock) (blockOutcome, error) {
	out := blockOutcome{fork: "unknown"}
	var meta blockJSON
	if err := json.Unmarshal(b.Block, &meta); err != nil {
		return out, err
	}
	if err := json.Unmarshal(b.Receipts, &meta.Receipts); err != nil {
		return out, err
	}
	ts, err := parseQuantity(meta.Timestamp)
	if err != nil {
		return out, err
	}
	out = blockOutcome{fork: forkAt(ts), txs: len(meta.Transactions)}
	raw, err := decodeHex(b.Raw)
	if err != nil {
		return out, err
	}
	blk, err := types.DecodeBlock(raw)
	if err != nil {
		return out, fmt.Errorf("decode: %w", err)
	}
	return out, res.verifyBlock(&blk, raw, &meta)
}

func (res *r1Result) verifyBlock(blk *types.Block, raw []byte, meta *blockJSON) error {
	if string(blk.Encode()) != string(raw) {
		return errors.New("re-encoding differs from the raw block")
	}
	if got := blk.Hash().String(); !strings.EqualFold(got, meta.Hash) {
		return fmt.Errorf("block hash %s, rpc %s", got, meta.Hash)
	}
	root := trie.OrderedRoot(blk.TransactionEnvelopes())
	if root != blk.Header.Inner.TransactionsRoot {
		return fmt.Errorf("transactions root %s, header %s", root, blk.Header.Inner.TransactionsRoot)
	}
	if !strings.EqualFold(root.String(), meta.TransactionsRoot) {
		return fmt.Errorf("transactions root %s, rpc %s", root, meta.TransactionsRoot)
	}
	if len(blk.Transactions) != len(meta.Transactions) {
		return fmt.Errorf("%d transactions, rpc %d", len(blk.Transactions), len(meta.Transactions))
	}
	if err := res.verifyReceipts(blk, meta.Receipts); err != nil {
		return err
	}
	for i := range blk.Transactions {
		if err := res.verifyTx(blk.Transactions[i], &meta.Transactions[i], &meta.Receipts[i]); err != nil {
			return fmt.Errorf("tx %d: %w", i, err)
		}
	}
	return nil
}

func (res *r1Result) verifyTx(tx types.Transaction, meta *txJSON, receipt *receiptJSON) error {
	res.TxTypes[fmt.Sprintf("0x%02x", uint8(tx.Type()))]++
	res.SigTypes[signatureLabel(tx)]++
	if got := tx.Hash().String(); !strings.EqualFold(got, meta.Hash) {
		return fmt.Errorf("hash %s, rpc %s", got, meta.Hash)
	}
	sender, err := tx.Sender()
	if err != nil {
		return fmt.Errorf("sender %s: %w", meta.Hash, err)
	}
	want, err := primitives.HexToAddress(meta.From)
	if err != nil {
		return err
	}
	if sender != want {
		return fmt.Errorf("sender %s, rpc %s (%s)", sender, meta.From, meta.Hash)
	}
	res.Senders++
	if err := res.verifyFeePayer(tx, sender, receipt); err != nil {
		return fmt.Errorf("%s: %w", meta.Hash, err)
	}
	if err := res.verifyTempoExtras(tx, sender, meta); err != nil {
		return fmt.Errorf("%s: %w", meta.Hash, err)
	}
	return nil
}

func signatureLabel(tx types.Transaction) string {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		if d.IsSystem() {
			return "system"
		}
		return "secp256k1"
	case *types.TempoTx:
		return tempoSignatureLabel(d)
	default:
		return "secp256k1"
	}
}

var primitiveLabels = map[types.SignatureKind]string{
	types.SignatureSecp256k1: "secp256k1",
	types.SignatureP256:      "p256",
	types.SignatureWebAuthn:  "webauthn",
}

var keychainLabels = map[types.KeychainVersion]string{
	types.KeychainNone: "",
	types.KeychainV1:   "/keychain-v1",
	types.KeychainV2:   "/keychain-v2",
}

func tempoSignatureLabel(tx *types.TempoTx) string {
	label := "tempo/" + primitiveLabels[tx.Signature.Inner.Kind] + keychainLabels[tx.Signature.Keychain]
	if tx.FeePayerSignature != nil {
		label += "/sponsored"
	}
	if tx.KeyAuthorization != nil {
		label += "/key-auth"
	}
	if len(tx.AuthorizationList) > 0 {
		label += "/aa-auth"
	}
	return label
}
