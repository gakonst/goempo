package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
)

type logJSON struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type receiptJSON struct {
	Type              string    `json:"type"`
	Status            string    `json:"status"`
	CumulativeGasUsed string    `json:"cumulativeGasUsed"`
	LogsBloom         string    `json:"logsBloom"`
	Logs              []logJSON `json:"logs"`
	TransactionHash   string    `json:"transactionHash"`
	FeePayer          string    `json:"feePayer"`
}

func decodeHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

func (r *receiptJSON) toReceipt() (types.Receipt, error) {
	var out types.Receipt
	kind, err := parseQuantity(r.Type)
	if err != nil {
		return out, err
	}
	status, err := parseQuantity(r.Status)
	if err != nil {
		return out, err
	}
	if out.CumulativeGasUsed, err = parseQuantity(r.CumulativeGasUsed); err != nil {
		return out, err
	}
	bloom, err := decodeHex(r.LogsBloom)
	if err != nil {
		return out, err
	}
	if len(bloom) != types.BloomLength {
		return out, fmt.Errorf("bloom has %d bytes", len(bloom))
	}
	copy(out.Bloom[:], bloom)
	out.Type = types.TxType(kind)
	out.Success = status == 1
	out.Logs, err = convertLogs(r.Logs)
	return out, err
}

func convertLogs(logs []logJSON) ([]types.Log, error) {
	out := make([]types.Log, len(logs))
	for i := range logs {
		addr, err := primitives.HexToAddress(logs[i].Address)
		if err != nil {
			return nil, err
		}
		data, err := decodeHex(logs[i].Data)
		if err != nil {
			return nil, err
		}
		out[i] = types.Log{Address: addr, Data: data}
		for _, t := range logs[i].Topics {
			h, err := primitives.HexToHash(t)
			if err != nil {
				return nil, err
			}
			out[i].Topics = append(out[i].Topics, h)
		}
	}
	return out, nil
}

func (res *r1Result) verifyReceipts(blk *types.Block, receipts []receiptJSON) error {
	if len(receipts) != len(blk.Transactions) {
		return fmt.Errorf("%d receipts for %d transactions", len(receipts), len(blk.Transactions))
	}
	encoded := make([][]byte, len(receipts))
	var bloom types.Bloom
	for i := range receipts {
		r, err := receipts[i].toReceipt()
		if err != nil {
			return fmt.Errorf("receipt %d: %w", i, err)
		}
		if types.LogsBloom(r.Logs) != r.Bloom {
			return fmt.Errorf("receipt %d: computed logs bloom differs from rpc", i)
		}
		round, err := types.DecodeReceipt(r.Encode())
		if err != nil {
			return fmt.Errorf("receipt %d: decode of own encoding: %w", i, err)
		}
		if string(round.Encode()) != string(r.Encode()) {
			return fmt.Errorf("receipt %d: encoding does not round-trip", i)
		}
		bloom.Or(&r.Bloom)
		encoded[i] = r.Encode()
	}
	if bloom != blk.Header.Inner.LogsBloom {
		return fmt.Errorf("header logs bloom differs from the receipts' union")
	}
	if root := trie.OrderedRoot(encoded); root != blk.Header.Inner.ReceiptsRoot {
		return fmt.Errorf("receipts root %s, header %s", root, blk.Header.Inner.ReceiptsRoot)
	}
	res.Receipts += len(receipts)
	return nil
}

func (res *r1Result) verifyFeePayer(tx types.Transaction, sender primitives.Address, r *receiptJSON) error {
	if r.FeePayer == "" {
		return nil
	}
	want, err := primitives.HexToAddress(r.FeePayer)
	if err != nil {
		return err
	}
	got := sender
	if d, ok := tx.Data.(*types.TempoTx); ok {
		if got, err = d.FeePayer(sender); err != nil {
			return fmt.Errorf("fee payer: %w", err)
		}
		if d.FeePayerSignature != nil {
			res.Sponsored++
		}
	}
	if got != want {
		return fmt.Errorf("fee payer %s, rpc %s", got, want)
	}
	return nil
}

func (res *r1Result) verifyTempoExtras(tx types.Transaction, sender primitives.Address, meta *txJSON) error {
	d, ok := tx.Data.(*types.TempoTx)
	if !ok {
		return nil
	}
	if meta.Signature.KeyID != "" {
		want, err := primitives.HexToAddress(meta.Signature.KeyID)
		if err != nil {
			return err
		}
		got, err := d.Signature.KeyID(tx.SigningHash())
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("key id %s, rpc %s", got, want)
		}
		res.KeyIDs++
	}
	if d.KeyAuthorization != nil {
		signer, err := d.KeyAuthorization.Signer()
		if err != nil {
			return fmt.Errorf("key authorization: %w", err)
		}
		if signer != sender {
			return fmt.Errorf("key authorization signer %s, sender %s", signer, sender)
		}
		res.KeyAuths++
	}
	return res.verifyAuthorities(d)
}

func (res *r1Result) verifyAuthorities(d *types.TempoTx) error {
	for i := range d.AuthorizationList {
		if _, err := d.AuthorizationList[i].Authority(); err != nil {
			return fmt.Errorf("aa authorization %d: %w", i, err)
		}
		res.AAAuths++
	}
	return nil
}
