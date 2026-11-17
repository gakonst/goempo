package rpc

import (
	"bytes"
	"encoding/json"
	"math/big"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

type BundleRequest struct {
	Txs         []types.Transaction
	Senders     []primitives.Address
	BlockNumber uint64
	StateBlock  uint64
	Timestamp   *uint64
	Coinbase    *primitives.Address
	GasLimit    uint64
	BaseFee     *uint64
}

type BundleTxResult struct {
	GasUsed      uint64
	GasPrice     u256.Int
	Success      bool
	Output       []byte
	CoinbaseDiff u256.Int
}

type BundleResult struct {
	Txs          []BundleTxResult
	GasUsed      uint64
	CoinbaseDiff u256.Int
}

type BundleExecutor interface {
	CallBundle(req *BundleRequest) (BundleResult, error)
}

type rawCallBundle struct {
	Txs              json.RawMessage `json:"txs"`
	BlockNumber      json.RawMessage `json:"blockNumber"`
	StateBlockNumber json.RawMessage `json:"stateBlockNumber"`
	Coinbase         json.RawMessage `json:"coinbase"`
	Timestamp        *uint64         `json:"timestamp"`
	GasLimit         *uint64         `json:"gasLimit"`
	BaseFee          json.RawMessage `json:"baseFee"`
}

var bundleFields = []string{"txs", "blockNumber", "stateBlockNumber"}

func EffectiveTip(tx types.Transaction, baseFee uint64) u256.Int {
	return effectiveTip(tx, baseFee)
}

func EffectiveGasPrice(tx types.Transaction, baseFee uint64) u256.Int {
	return effectiveGasPrice(tx, baseFee)
}

func decimalU256(v u256.Int) string {
	b := v.Bytes32()
	return new(big.Int).SetBytes(b[:]).String()
}

func (s *Server) bundleArgs(p *params) (*BundleRequest, error) {
	raw, err := p.required("bundle")
	if err != nil {
		return nil, err
	}
	if err := structParam(raw, "struct EthCallBundle", bundleFields...); err != nil {
		return nil, err
	}
	var rb rawCallBundle
	if err := json.Unmarshal(raw, &rb); err != nil {
		return nil, invalidParams(err.Error())
	}
	txs, ok := arrayElems(bytes.TrimSpace(rb.Txs))
	if !ok {
		return nil, shiftColumn(typeError(bytes.TrimSpace(rb.Txs), "a sequence"), bytes.Index(raw, rb.Txs))
	}
	if len(txs) == 0 {
		return nil, newError(CodeInvalidParams, "bundle missing txs")
	}
	number, err := quantityParam(rb.BlockNumber)
	if err != nil {
		return nil, err
	}
	if number == 0 {
		return nil, newError(CodeInvalidParams, "bundle missing blockNumber")
	}
	req := &BundleRequest{BlockNumber: number, Timestamp: rb.Timestamp, GasLimit: s.cfg.GasCap}
	if rb.GasLimit != nil {
		if *rb.GasLimit > s.cfg.GasCap {
			return nil, newError(CodeServer, "intrinsic gas too high")
		}
		req.GasLimit = *rb.GasLimit
	}
	for _, e := range txs {
		enc, err := bytesParam(e.raw)
		if err != nil {
			return nil, err
		}
		tx, err := types.DecodeTransaction(enc)
		if err != nil {
			return nil, newError(CodeInvalidParams, "failed to decode signed transaction")
		}
		from, err := tx.Sender()
		if err != nil {
			return nil, newError(CodeInvalidParams, "invalid transaction signature")
		}
		req.Txs = append(req.Txs, tx)
		req.Senders = append(req.Senders, from)
	}
	ref, err := blockIDParam(rb.StateBlockNumber)
	if err != nil {
		return nil, err
	}
	if req.StateBlock, err = s.stateBlock(ref); err != nil {
		return nil, err
	}
	if present(rb.Coinbase) {
		cb, err := addressParam(rb.Coinbase)
		if err != nil {
			return nil, err
		}
		req.Coinbase = &cb
	}
	if present(rb.BaseFee) {
		fee, err := quantityParam(rb.BaseFee)
		if err != nil {
			return nil, err
		}
		req.BaseFee = &fee
	}
	return req, nil
}

func ethCallBundle(s *Server, p *params) ([]byte, error) {
	req, err := s.bundleArgs(p)
	if err != nil {
		return nil, err
	}
	ex, ok := s.exec.(BundleExecutor)
	if !ok {
		return nil, internalError("unimplemented")
	}
	res, err := ex.CallBundle(req)
	if err != nil {
		return nil, executionError(err)
	}
	var hashes []byte
	total := u256.Zero()
	for i := range res.Txs {
		h := req.Txs[i].Hash()
		hashes = append(hashes, h[:]...)
		total = total.Add(u256.FromUint64(res.Txs[i].GasUsed).Mul(res.Txs[i].GasPrice))
	}
	bundleHash := keccak.Sum256(hashes)
	price := u256.Zero()
	if res.GasUsed > 0 {
		price = res.CoinbaseDiff.Div(u256.FromUint64(res.GasUsed))
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.kHash("bundleHash", primitives.Hash(bundleHash))
		w.kStr("bundleGasPrice", decimalU256(price))
		w.kStr("coinbaseDiff", decimalU256(res.CoinbaseDiff))
		w.kStr("ethSentToCoinbase", decimalU256(Balances{Before: total, After: res.CoinbaseDiff}.Gain()))
		w.kStr("gasFees", decimalU256(total))
		w.key("results")
		w.arrOpen()
		for i := range res.Txs {
			writeBundleTx(w, &req.Txs[i], req.Senders[i], &res.Txs[i])
		}
		w.arrClose()
		w.key("stateBlockNumber")
		w.integer(int64(req.StateBlock))
		w.key("totalGasUsed")
		w.integer(int64(res.GasUsed))
		w.objClose()
	}), nil
}

func writeBundleTx(w *writer, tx *types.Transaction, from primitives.Address, r *BundleTxResult) {
	fees := u256.FromUint64(r.GasUsed).Mul(r.GasPrice)
	w.objOpen()
	w.kStr("coinbaseDiff", decimalU256(r.CoinbaseDiff))
	w.kStr("ethSentToCoinbase", decimalU256(Balances{Before: fees, After: r.CoinbaseDiff}.Gain()))
	w.kAddress("fromAddress", from)
	w.kStr("gasFees", decimalU256(fees))
	w.kStr("gasPrice", decimalU256(r.GasPrice))
	w.key("gasUsed")
	w.integer(int64(r.GasUsed))
	w.key("toAddress")
	if to := txTo(*tx); to != nil {
		w.address(*to)
	} else {
		w.raw(resultNull)
	}
	w.kHash("txHash", tx.Hash())
	if r.Success {
		w.kData("value", r.Output)
	} else {
		w.kData("revert", r.Output)
	}
	w.objClose()
}

type Balances struct {
	Before u256.Int
	After  u256.Int
}

func (b Balances) Gain() u256.Int {
	if b.After.Lt(b.Before) {
		return u256.Zero()
	}
	return b.After.Sub(b.Before)
}
