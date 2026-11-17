package rpc

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	optBaseFee = iota + 1
	optWithdrawals
	optBlobGas
	optExcessBlobGas
	optBeaconRoot
	optRequests
)

const _ = uint(optRequests - optBaseFee)

func writeHeaderFields(w *writer, h *types.Header, hash primitives.Hash) {
	e := &h.Inner
	w.kHash("hash", hash)
	w.kQuantity("mainBlockGeneralGasLimit", h.GeneralGasLimit)
	w.kQuantity("sharedGasLimit", h.SharedGasLimit)
	w.kQuantity("timestampMillisPart", h.TimestampMillisPart)
	w.kHash("parentHash", e.ParentHash)
	w.kHash("sha3Uncles", e.OmmersHash)
	w.kAddress("miner", e.Beneficiary)
	w.kHash("stateRoot", e.StateRoot)
	w.kHash("transactionsRoot", e.TransactionsRoot)
	w.kHash("receiptsRoot", e.ReceiptsRoot)
	w.kData("logsBloom", e.LogsBloom[:])
	w.kQuantity256("difficulty", e.Difficulty)
	w.kQuantity("number", e.Number)
	w.kQuantity("gasLimit", e.GasLimit)
	w.kQuantity("gasUsed", e.GasUsed)
	w.kQuantity("timestamp", e.Timestamp)
	w.kData("extraData", e.ExtraData)
	w.kHash("mixHash", e.MixHash)
	w.kData("nonce", e.Nonce[:])
	writeOptionalHeader(w, e)
	if h.Consensus != nil {
		c := h.Consensus
		w.key("consensusContext")
		w.objOpen()
		w.key("epoch")
		w.integer(int64(c.Epoch))
		w.key("view")
		w.integer(int64(c.View))
		w.key("parentView")
		w.integer(int64(c.ParentView))
		w.kData("proposer", c.Proposer[:])
		w.objClose()
	}
}

func writeOptionalHeader(w *writer, e *types.EthHeader) {
	if e.OptionalFields >= optBaseFee {
		w.kQuantity("baseFeePerGas", e.BaseFeePerGas)
	}
	if e.OptionalFields >= optWithdrawals {
		w.kHash("withdrawalsRoot", e.WithdrawalsRoot)
	}
	if e.OptionalFields >= optBlobGas {
		w.kQuantity("blobGasUsed", e.BlobGasUsed)
	}
	if e.OptionalFields >= optExcessBlobGas {
		w.kQuantity("excessBlobGas", e.ExcessBlobGas)
	}
	if e.OptionalFields >= optBeaconRoot {
		w.kHash("parentBeaconBlockRoot", e.ParentBeaconBlockRoot)
	}
	if e.OptionalFields >= optRequests {
		w.kHash("requestsHash", e.RequestsHash)
	}
}

func writeHeader(w *writer, b *BlockData) {
	w.objOpen()
	writeHeaderFields(w, &b.Block.Header, b.Hash)
	w.kQuantity("timestampMillis", b.Block.Header.TimestampMillis())
	w.objClose()
}

type txDetail bool

const (
	txHashes txDetail = false
	txFull   txDetail = true
)

func writeBlock(w *writer, b *BlockData, full txDetail) {
	w.objOpen()
	writeBlockFields(w, b, full)
	w.objClose()
}

func writeBlockFields(w *writer, b *BlockData, full txDetail) {
	writeHeaderFields(w, &b.Block.Header, b.Hash)
	w.kQuantity("size", uint64(len(b.Raw)))
	w.kQuantity("timestampMillis", b.Block.Header.TimestampMillis())
	w.key("uncles")
	w.arrOpen()
	for i := range b.Block.Ommers {
		w.hash(b.Block.Ommers[i].Hash())
	}
	w.arrClose()
	w.key("transactions")
	w.arrOpen()
	for i := range b.Block.Transactions {
		if full {
			writeTx(w, b, i)
		} else {
			w.hash(b.TxHash(i))
		}
	}
	w.arrClose()
	if b.Block.HasWithdrawals {
		w.key("withdrawals")
		w.arrOpen()
		for i := range b.Block.Withdrawals {
			wd := &b.Block.Withdrawals[i]
			w.objOpen()
			w.kQuantity("index", wd.Index)
			w.kQuantity("validatorIndex", wd.ValidatorIndex)
			w.kAddress("address", wd.Address)
			w.kQuantity("amount", wd.Amount)
			w.objClose()
		}
		w.arrClose()
	}
}

func effectiveGasPrice(tx types.Transaction, baseFee uint64) u256.Int {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		return d.GasPrice
	case *types.AccessListTx:
		return d.GasPrice
	case *types.DynamicFeeTx:
		return dynamicPrice(feeCaps{maxFee: d.MaxFeePerGas, tip: d.MaxPriorityFeePerGas}, baseFee)
	case *types.SetCodeTx:
		return dynamicPrice(feeCaps{maxFee: d.MaxFeePerGas, tip: d.MaxPriorityFeePerGas}, baseFee)
	case *types.TempoTx:
		return dynamicPrice(feeCaps{maxFee: d.MaxFeePerGas, tip: d.MaxPriorityFeePerGas}, baseFee)
	}
	return u256.Zero()
}

type feeCaps struct {
	maxFee u256.Int
	tip    u256.Int
}

func dynamicPrice(c feeCaps, baseFee uint64) u256.Int {
	price, overflow := u256.FromUint64(baseFee).AddOverflow(c.tip)
	if overflow || c.maxFee.Lt(price) {
		return c.maxFee
	}
	return price
}

func writeTxContext(w *writer, b *BlockData, i int) {
	w.kHash("hash", b.TxHash(i))
	w.kHash("blockHash", b.Hash)
	w.kQuantity("blockNumber", b.Number())
	w.kQuantity("transactionIndex", uint64(i))
	w.kAddress("from", b.Sender(i))
}

func writeTx(w *writer, b *BlockData, i int) {
	tx := b.Block.Transactions[i]
	w.objOpen()
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		writeLegacy(w, d)
		writeTxContext(w, b, i)
	case *types.AccessListTx:
		writeAccessListTx(w, d)
		writeTxContext(w, b, i)
	case *types.DynamicFeeTx:
		writeDynamicFields(w, types.TxTypeDynamicFee, d, nil)
		writeTxContext(w, b, i)
		w.kQuantity256("gasPrice", effectiveGasPrice(tx, b.Block.Header.Inner.BaseFeePerGas))
	case *types.SetCodeTx:
		writeDynamicFields(w, types.TxTypeSetCode, &d.DynamicFeeTx, d.AuthorizationList)
		writeTxContext(w, b, i)
		w.kQuantity256("gasPrice", effectiveGasPrice(tx, b.Block.Header.Inner.BaseFeePerGas))
	case *types.TempoTx:
		writeTempoFields(w, d, tx.SigningHash())
		writeTxContext(w, b, i)
		w.kQuantity256("gasPrice", effectiveGasPrice(tx, b.Block.Header.Inner.BaseFeePerGas))
	}
	w.kQuantity("blockTimestamp", b.Block.Header.Inner.Timestamp)
	w.objClose()
}

func writeTo(w *writer, to *primitives.Address) {
	w.key("to")
	w.addressPtr(to)
}

func writeLegacy(w *writer, d *types.LegacyTx) {
	w.kQuantity("type", uint64(types.TxTypeLegacy))
	if id, ok := d.ChainID(); ok {
		w.kQuantity("chainId", id)
	}
	w.kQuantity("nonce", d.Nonce)
	w.kQuantity256("gasPrice", d.GasPrice)
	w.kQuantity("gas", d.Gas)
	writeTo(w, d.To)
	w.kQuantity256("value", d.Value)
	w.kData("input", d.Data)
	w.key("r")
	w.quantityBytes(d.R[:])
	w.key("s")
	w.quantityBytes(d.S[:])
	w.kQuantity("v", d.V)
}

func writeSig(w *writer, sig *types.Signature) {
	w.key("r")
	w.quantityBytes(sig.R[:])
	w.key("s")
	w.quantityBytes(sig.S[:])
	parity := uint64(0)
	if sig.YParity {
		parity = 1
	}
	w.kQuantity("yParity", parity)
	w.kQuantity("v", parity)
}

func writeAccessList(w *writer, al types.AccessList) {
	w.key("accessList")
	w.arrOpen()
	for i := range al {
		w.objOpen()
		w.kAddress("address", al[i].Address)
		w.key("storageKeys")
		w.arrOpen()
		for j := range al[i].StorageKeys {
			w.hash(al[i].StorageKeys[j])
		}
		w.arrClose()
		w.objClose()
	}
	w.arrClose()
}

func writeAccessListTx(w *writer, d *types.AccessListTx) {
	w.kQuantity("type", uint64(types.TxTypeAccessList))
	w.kQuantity("chainId", d.ChainID)
	w.kQuantity("nonce", d.Nonce)
	w.kQuantity256("gasPrice", d.GasPrice)
	w.kQuantity("gas", d.Gas)
	writeTo(w, d.To)
	w.kQuantity256("value", d.Value)
	writeAccessList(w, d.AccessList)
	w.kData("input", d.Data)
	writeSig(w, &d.Sig)
}

func writeDynamicFields(w *writer, typ types.TxType, d *types.DynamicFeeTx, auths []types.SetCodeAuthorization) {
	w.kQuantity("type", uint64(typ))
	w.kQuantity("chainId", d.ChainID)
	w.kQuantity("nonce", d.Nonce)
	w.kQuantity("gas", d.Gas)
	w.kQuantity256("maxFeePerGas", d.MaxFeePerGas)
	w.kQuantity256("maxPriorityFeePerGas", d.MaxPriorityFeePerGas)
	writeTo(w, d.To)
	w.kQuantity256("value", d.Value)
	writeAccessList(w, d.AccessList)
	if typ == types.TxTypeSetCode {
		w.key("authorizationList")
		w.arrOpen()
		for i := range auths {
			a := &auths[i]
			w.objOpen()
			w.kQuantity256("chainId", a.ChainID)
			w.kAddress("address", a.Address)
			w.kQuantity("nonce", a.Nonce)
			w.kQuantity("yParity", uint64(a.YParity))
			w.key("r")
			w.quantityBytes(a.R[:])
			w.key("s")
			w.quantityBytes(a.S[:])
			w.objClose()
		}
		w.arrClose()
	}
	w.kData("input", d.Data)
	writeSig(w, &d.Sig)
}

func writeOptQuantity(w *writer, k field, v uint64) {
	w.key(k)
	if v == 0 {
		w.null()
		return
	}
	w.quantity(v)
}

func writeTempoFields(w *writer, d *types.TempoTx, signingHash primitives.Hash) {
	w.kQuantity("type", uint64(types.TxTypeTempo))
	w.kQuantity("chainId", d.ChainID)
	w.key("feeToken")
	w.addressPtr(d.FeeToken)
	w.kQuantity256("maxPriorityFeePerGas", d.MaxPriorityFeePerGas)
	w.kQuantity256("maxFeePerGas", d.MaxFeePerGas)
	w.kQuantity("gas", d.GasLimit)
	w.key("calls")
	w.arrOpen()
	for i := range d.Calls {
		c := &d.Calls[i]
		w.objOpen()
		writeTo(w, c.To)
		w.kQuantity256("value", c.Value)
		w.kData("input", c.Input)
		w.kNull("data")
		w.objClose()
	}
	w.arrClose()
	writeAccessList(w, d.AccessList)
	w.kQuantity256("nonceKey", d.NonceKey)
	w.kQuantity("nonce", d.Nonce)
	w.key("feePayerSignature")
	if d.FeePayerSignature == nil {
		w.null()
	} else {
		w.objOpen()
		writeSig(w, d.FeePayerSignature)
		w.objClose()
	}
	writeOptQuantity(w, "validBefore", d.ValidBefore)
	writeOptQuantity(w, "validAfter", d.ValidAfter)
	w.key("keyAuthorization")
	if d.KeyAuthorization == nil {
		w.null()
	} else {
		writeKeyAuthorization(w, d.KeyAuthorization)
	}
	w.key("aaAuthorizationList")
	w.arrOpen()
	for i := range d.AuthorizationList {
		a := &d.AuthorizationList[i]
		w.objOpen()
		w.kQuantity256("chainId", a.ChainID)
		w.kAddress("address", a.Address)
		w.kQuantity("nonce", a.Nonce)
		w.key("signature")
		writeTempoSignature(w, &a.Signature, a.SigningHash())
		w.objClose()
	}
	w.arrClose()
	w.key("signature")
	writeTempoSignature(w, &d.Signature, signingHash)
}

func signatureKindName(k types.SignatureKind) string {
	switch k {
	case types.SignatureSecp256k1:
		return "secp256k1"
	case types.SignatureP256:
		return "p256"
	case types.SignatureWebAuthn:
		return "webAuthn"
	case types.SignatureAccount:
		return "account"
	default:
		assert.Unreachable("SignatureKind: unhandled value")
	}
	return ""
}

func writePrimitiveSignature(w *writer, sig *types.PrimitiveSignature) {
	w.objOpen()
	w.kStr("type", signatureKindName(sig.Kind))
	switch sig.Kind {
	case types.SignatureP256:
		w.kData("r", sig.R[:])
		w.kData("s", sig.S[:])
		w.kData("pubKeyX", sig.PubKeyX[:])
		w.kData("pubKeyY", sig.PubKeyY[:])
		w.kBool("preHash", jbool(sig.PreHash))
	case types.SignatureWebAuthn:
		w.kData("r", sig.R[:])
		w.kData("s", sig.S[:])
		w.kData("pubKeyX", sig.PubKeyX[:])
		w.kData("pubKeyY", sig.PubKeyY[:])
		w.kData("webauthnData", sig.WebAuthnData)
	case types.SignatureSecp256k1, types.SignatureAccount:
		parity := uint64(0)
		if sig.YParity {
			parity = 1
		}
		w.key("r")
		w.quantityBytes(sig.R[:])
		w.key("s")
		w.quantityBytes(sig.S[:])
		w.kQuantity("yParity", parity)
		w.kQuantity("v", parity)
	default:
		assert.Unreachable("SignatureKind: unhandled value")
	}
	w.objClose()
}

func writeTempoSignature(w *writer, sig *types.TempoSignature, hash primitives.Hash) {
	if sig.Keychain == types.KeychainNone {
		writePrimitiveSignature(w, &sig.Inner)
		return
	}
	w.objOpen()
	w.kAddress("userAddress", sig.UserAddress)
	w.key("signature")
	writePrimitiveSignature(w, &sig.Inner)
	version := "v1"
	if sig.Keychain == types.KeychainV2 {
		version = "v2"
	}
	w.kStr("version", version)
	w.key("keyId")
	keyID, err := sig.KeyID(hash)
	if err != nil {
		w.null()
	} else {
		w.address(keyID)
	}
	w.objClose()
}

func writeKeyAuthorization(w *writer, sk *types.SignedKeyAuthorization) {
	k := &sk.Authorization
	w.objOpen()
	w.kQuantity("chainId", k.ChainID)
	w.kStr("keyType", signatureKindName(k.KeyType))
	w.kAddress("keyId", k.KeyID)
	writeOptQuantity(w, "expiry", k.Expiry)
	w.key("limits")
	if !k.HasLimits {
		w.null()
	} else {
		w.arrOpen()
		for i := range k.Limits {
			l := &k.Limits[i]
			w.objOpen()
			w.kAddress("token", l.Token)
			w.kQuantity256("limit", l.Limit)
			w.kQuantity("period", l.Period)
			w.objClose()
		}
		w.arrClose()
	}
	w.key("allowedCalls")
	if !k.HasAllowedCalls {
		w.null()
	} else {
		writeCallScopes(w, k.AllowedCalls)
	}
	w.key("witness")
	if k.HasWitness {
		w.hash(k.Witness)
	} else {
		w.null()
	}
	w.kBool("isAdmin", jbool(k.IsAdmin))
	w.key("account")
	if k.HasAccount {
		w.address(k.Account)
	} else {
		w.null()
	}
	w.key("signature")
	writePrimitiveSignature(w, &sk.Signature)
	w.objClose()
}

func writeCallScopes(w *writer, scopes []types.CallScope) {
	w.arrOpen()
	for i := range scopes {
		sc := &scopes[i]
		w.objOpen()
		w.kAddress("target", sc.Target)
		if len(sc.SelectorRules) > 0 {
			w.key("selectorRules")
			w.arrOpen()
			for j := range sc.SelectorRules {
				rule := &sc.SelectorRules[j]
				w.objOpen()
				w.kData("selector", rule.Selector[:])
				if len(rule.Recipients) > 0 {
					w.key("recipients")
					w.arrOpen()
					for r := range rule.Recipients {
						w.address(rule.Recipients[r])
					}
					w.arrClose()
				}
				w.objClose()
			}
			w.arrClose()
		}
		w.objClose()
	}
	w.arrClose()
}

func txTo(tx types.Transaction) *primitives.Address {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		return d.To
	case *types.AccessListTx:
		return d.To
	case *types.DynamicFeeTx:
		return d.To
	case *types.SetCodeTx:
		return d.To
	case *types.TempoTx:
		if len(d.Calls) > 0 {
			return d.Calls[0].To
		}
	}
	return nil
}

func txNonce(tx types.Transaction) uint64 {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		return d.Nonce
	case *types.AccessListTx:
		return d.Nonce
	case *types.DynamicFeeTx:
		return d.Nonce
	case *types.SetCodeTx:
		return d.Nonce
	case *types.TempoTx:
		return d.Nonce
	}
	return 0
}

func createAddress(sender primitives.Address, nonce uint64) primitives.Address {
	enc := rlp.NewEncoder(64)
	enc.ListStart()
	enc.WriteBytes(sender[:])
	enc.WriteUint64(nonce)
	enc.ListEnd()
	h := keccak.Sum256(enc.Encoded())
	return primitives.BytesToAddress(h[12:])
}

func firstLogIndex(b *BlockData, i int) uint64 {
	n := uint64(0)
	for j := range min(i, len(b.Receipts)) {
		n += uint64(len(b.Receipts[j].Logs))
	}
	return n
}

type logPos struct {
	b        *BlockData
	txIndex  int
	logIndex uint64
}

func writeLog(w *writer, pos logPos, l *types.Log) {
	b, txIndex, logIndex := pos.b, pos.txIndex, pos.logIndex
	w.objOpen()
	w.kAddress("address", l.Address)
	w.key("topics")
	w.arrOpen()
	for i := range l.Topics {
		w.hash(l.Topics[i])
	}
	w.arrClose()
	w.kData("data", l.Data)
	w.kHash("blockHash", b.Hash)
	w.kQuantity("blockNumber", b.Number())
	w.kQuantity("blockTimestamp", b.Block.Header.Inner.Timestamp)
	w.kHash("transactionHash", b.TxHash(txIndex))
	w.kQuantity("transactionIndex", uint64(txIndex))
	w.kQuantity("logIndex", logIndex)
	w.kBool("removed", false)
	w.objClose()
}

func writeReceipt(w *writer, b *BlockData, i int, logIndex uint64) {
	r := &b.Receipts[i]
	tx := b.Block.Transactions[i]
	sender := b.Sender(i)
	gasUsed := r.CumulativeGasUsed
	if i > 0 {
		gasUsed -= b.Receipts[i-1].CumulativeGasUsed
	}
	price := effectiveGasPrice(tx, b.Block.Header.Inner.BaseFeePerGas)
	w.objOpen()
	w.kQuantity("type", uint64(r.Type))
	status := uint64(0)
	if r.Success {
		status = 1
	}
	w.kQuantity("status", status)
	w.kQuantity("cumulativeGasUsed", r.CumulativeGasUsed)
	w.key("logs")
	w.arrOpen()
	for j := range r.Logs {
		writeLog(w, logPos{b: b, txIndex: i, logIndex: logIndex + uint64(j)}, &r.Logs[j])
	}
	w.arrClose()
	w.kData("logsBloom", r.Bloom[:])
	w.kHash("transactionHash", b.TxHash(i))
	w.kQuantity("transactionIndex", uint64(i))
	w.kHash("blockHash", b.Hash)
	w.kQuantity("blockNumber", b.Number())
	w.kQuantity("gasUsed", gasUsed)
	w.kQuantity256("effectiveGasPrice", price)
	w.kAddress("from", sender)
	to := txTo(tx)
	writeTo(w, to)
	w.key("contractAddress")
	if to == nil {
		w.address(createAddress(sender, txNonce(tx)))
	} else {
		w.null()
	}
	if !price.IsZero() && gasUsed != 0 && len(r.Logs) > 0 {
		w.kAddress("feeToken", r.Logs[len(r.Logs)-1].Address)
	}
	w.kAddress("feePayer", feePayer(tx, sender))
	w.objClose()
}

func feePayer(tx types.Transaction, sender primitives.Address) primitives.Address {
	t, ok := tx.Data.(*types.TempoTx)
	if !ok {
		return sender
	}
	payer, err := t.FeePayer(sender)
	if err != nil {
		return sender
	}
	return payer
}
