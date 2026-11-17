package rpc

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

const (
	defaultProofWindow = 5000
	defaultGasCap      = 50_000_000
	protocolVersion    = 5
	blobBaseFee        = 1
	senderScanBlocks   = 1 << 14
	storageValuesMax   = 1024
)

const _ = uint(senderScanBlocks - storageValuesMax)

var balancePlaceholder = []byte("\"0x9612084f0316e0ebd5182f398e5195a51b5ca47667d4c9b26c9b26c9b26c9b2\"")

func methodTable() map[string]handler {
	return map[string]handler{
		"eth_chainId":                                ethChainID,
		"net_version":                                netVersion,
		"eth_protocolVersion":                        constant(quantityResult(protocolVersion)),
		"net_listening":                              constant(resultTrue),
		"net_peerCount":                              constant(quantityResult(0)),
		"web3_clientVersion":                         web3ClientVersion,
		"web3_sha3":                                  web3Sha3,
		"eth_accounts":                               constant(resultEmpty),
		"eth_coinbase":                               unimplemented,
		"eth_mining":                                 unimplemented,
		"eth_getWork":                                unimplemented,
		"eth_hashrate":                               constant(quantityResult(0)),
		"eth_submitHashrate":                         ethSubmitHashrate,
		"eth_submitWork":                             ethSubmitWork,
		"eth_syncing":                                constant(resultFalse),
		"eth_blockNumber":                            ethBlockNumber,
		"eth_gasPrice":                               ethGasPrice,
		"eth_baseFee":                                ethGasPrice,
		"eth_maxPriorityFeePerGas":                   constant(quantityResult(0)),
		"eth_blobBaseFee":                            constant(quantityResult(blobBaseFee)),
		"eth_feeHistory":                             ethFeeHistory,
		"eth_pendingTransactions":                    constant(resultEmpty),
		"eth_capabilities":                           ethCapabilities,
		"eth_getBlockByNumber":                       ethGetBlockByNumber,
		"eth_getBlockByHash":                         ethGetBlockByHash,
		"eth_getHeaderByNumber":                      ethGetHeaderByNumber,
		"eth_getHeaderByHash":                        ethGetHeaderByHash,
		"eth_getBlockTransactionCountByNumber":       ethTxCountByNumber,
		"eth_getBlockTransactionCountByHash":         ethTxCountByHash,
		"eth_getUncleCountByBlockNumber":             ethUncleCountByNumber,
		"eth_getUncleCountByBlockHash":               ethUncleCountByHash,
		"eth_getUncleByBlockNumberAndIndex":          ethUncleByNumber,
		"eth_getUncleByBlockHashAndIndex":            ethUncleByHash,
		"eth_getBlockReceipts":                       ethGetBlockReceipts,
		"eth_getTransactionByHash":                   ethGetTransactionByHash,
		"eth_getRawTransactionByHash":                ethGetRawTransactionByHash,
		"eth_getTransactionByBlockNumberAndIndex":    ethTxByNumberAndIndex,
		"eth_getTransactionByBlockHashAndIndex":      ethTxByHashAndIndex,
		"eth_getRawTransactionByBlockNumberAndIndex": ethRawTxByNumberAndIndex,
		"eth_getRawTransactionByBlockHashAndIndex":   ethRawTxByHashAndIndex,
		"eth_getTransactionReceipt":                  ethGetTransactionReceipt,
		"eth_getTransactionBySenderAndNonce":         ethTxBySenderAndNonce,
		"eth_getBalance":                             ethGetBalance,
		"eth_getTransactionCount":                    ethGetTransactionCount,
		"eth_getCode":                                ethGetCode,
		"eth_getStorageAt":                           ethGetStorageAt,
		"eth_getStorageValues":                       ethGetStorageValues,
		"eth_getAccountInfo":                         ethGetAccountInfo,
		"eth_getAccount":                             ethGetAccount,
		"eth_getProof":                               ethGetProof,
		"eth_getLogs":                                ethGetLogs,
		"eth_newFilter":                              ethNewFilter,
		"eth_newBlockFilter":                         ethNewBlockFilter,
		"eth_newPendingTransactionFilter":            ethNewPendingFilter,
		"eth_getFilterChanges":                       ethGetFilterChanges,
		"eth_getFilterLogs":                          ethGetFilterLogs,
		"eth_uninstallFilter":                        ethUninstallFilter,
		"eth_sendRawTransaction":                     ethSendRawTransaction,
		"eth_sendRawTransactionSync":                 ethSendRawTransaction,
		"eth_sendTransaction":                        unknownAccount1,
		"eth_signTransaction":                        unknownAccount1,
		"eth_sign":                                   unknownAccount2,
		"eth_signTypedData":                          unknownAccount2,
		"eth_subscribe":                              subscribeOverHTTP,
		"eth_unsubscribe":                            subscribeOverHTTP,
		"eth_call":                                   ethCall,
		"eth_estimateGas":                            ethEstimateGas,
		"eth_createAccessList":                       ethCreateAccessList,
		"debug_getRawHeader":                         debugGetRawHeader,
		"debug_getRawBlock":                          debugGetRawBlock,
		"debug_getRawTransaction":                    ethGetRawTransactionByHash,
		"debug_getRawReceipts":                       debugGetRawReceipts,
		"debug_traceTransaction":                     debugTraceTransaction,
		"debug_traceBlockByNumber":                   debugTraceBlockByNumber,
		"debug_traceBlockByHash":                     debugTraceBlockByHash,
		"debug_traceCall":                            debugTraceCall,
		"tempo_forkSchedule":                         tempoForkSchedule,
		"eth_getTransactions":                        ethGetTransactions,
		"txpool_status":                              constant([]byte(`{"pending":"0x0","queued":"0x0"}`)),
		"txpool_content":                             constant([]byte(`{"pending":{},"queued":{}}`)),
		"txpool_inspect":                             constant([]byte(`{"pending":{},"queued":{}}`)),
		"txpool_contentFrom":                         txpoolContentFrom,
		"eth_simulateV1":                             ethSimulateV1,
		"eth_callMany":                               ethCallMany,
		"consensus_getLatest":                        consensusGetLatest,
		"consensus_getFinalization":                  notServed("consensus_getFinalization"),
		"trace_block":                                traceBlockParity,
		"trace_transaction":                          traceTransaction,
		"trace_filter":                               traceFilter,
		"trace_replayBlockTransactions":              traceReplayBlockTransactions,
		"trace_replayTransaction":                    traceReplayTransaction,
		"trace_get":                                  traceGet,
		"trace_call":                                 traceCallParity,
		"trace_callMany":                             traceCallMany,
	}
}

func constant(result []byte) handler {
	return func(_ *Server, _ *params) ([]byte, error) { return result, nil }
}

func unimplemented(_ *Server, _ *params) ([]byte, error) {
	return nil, internalError("unimplemented")
}

func ethChainID(s *Server, _ *params) ([]byte, error) { return quantityResult(s.cfg.ChainID), nil }

func netVersion(s *Server, _ *params) ([]byte, error) {
	return stringResult(uintString(s.cfg.ChainID)), nil
}

func web3ClientVersion(s *Server, _ *params) ([]byte, error) {
	return stringResult(s.cfg.ClientVersion), nil
}

func web3Sha3(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("input")
	if err != nil {
		return nil, err
	}
	b, err := bytesParam(raw)
	if err != nil {
		return nil, err
	}
	return hashResult(keccak.Sum256(b)), nil
}

func requireAll(p *params, names ...string) error {
	for _, name := range names {
		if _, err := p.required(name); err != nil {
			return err
		}
	}
	return nil
}

func ethSubmitHashrate(_ *Server, p *params) ([]byte, error) {
	if err := requireAll(p, "hashrate", "id"); err != nil {
		return nil, err
	}
	return resultFalse, nil
}

func ethSubmitWork(_ *Server, p *params) ([]byte, error) {
	if err := requireAll(p, "nonce", "pow_hash", "mix_digest"); err != nil {
		return nil, err
	}
	return resultFalse, nil
}

func ethBlockNumber(s *Server, _ *params) ([]byte, error) {
	return quantityResult(s.chain.Head()), nil
}

func ethGasPrice(s *Server, _ *params) ([]byte, error) {
	b, ok := s.chain.Block(s.chain.Head())
	if !ok {
		return quantityResult(0), nil
	}
	return quantityResult(b.Block.Header.Inner.BaseFeePerGas), nil
}

func ethCapabilities(s *Server, _ *params) ([]byte, error) {
	head := s.chain.Head()
	b, ok := s.chain.Block(head)
	oldest := uint64(0)
	if head > s.cfg.ProofWindow {
		oldest = head - s.cfg.ProofWindow
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.key("head")
		w.objOpen()
		w.kQuantity("number", head)
		w.key("hash")
		if ok {
			w.hash(b.Hash)
		} else {
			w.null()
		}
		w.objClose()
		for _, name := range [...]field{"state", "tx", "logs", "receipts", "blocks"} {
			w.key(name)
			w.objOpen()
			w.kBool("disabled", false)
			w.kQuantity("oldestBlock", 0)
			w.objClose()
		}
		w.key("stateproofs")
		w.objOpen()
		w.kBool("disabled", false)
		w.kQuantity("oldestBlock", oldest)
		w.key("deleteStrategy")
		w.objOpen()
		w.kStr("type", "window")
		w.kQuantity("retentionBlocks", s.cfg.ProofWindow)
		w.objClose()
		w.objClose()
		w.objClose()
	}), nil
}

func blockByNumberArg(s *Server, p *params) (*BlockData, error) {
	raw, err := p.required("number")
	if err != nil {
		return nil, err
	}
	ref, err := blockNumberParam(raw)
	if err != nil {
		return nil, err
	}
	return s.blockFor(ref), nil
}

func blockByHashArg(s *Server, p *params) (*BlockData, error) {
	raw, err := p.required("hash")
	if err != nil {
		return nil, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return nil, err
	}
	return s.blockFor(blockRef{kind: tagHash, hash: h}), nil
}

func renderBlock(b *BlockData, p *params) ([]byte, error) {
	raw, err := p.required("full")
	if err != nil {
		return nil, err
	}
	full, err := boolParam(raw)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return resultNull, nil
	}
	return encode(func(w *writer) { writeBlock(w, b, txDetail(full)) }), nil
}

func ethGetBlockByNumber(s *Server, p *params) ([]byte, error) {
	b, err := blockByNumberArg(s, p)
	if err != nil {
		return nil, err
	}
	return renderBlock(b, p)
}

func ethGetBlockByHash(s *Server, p *params) ([]byte, error) {
	b, err := blockByHashArg(s, p)
	if err != nil {
		return nil, err
	}
	return renderBlock(b, p)
}

func renderHeader(b *BlockData, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if b == nil {
		return resultNull, nil
	}
	return encode(func(w *writer) { writeHeader(w, b) }), nil
}

func ethGetHeaderByNumber(s *Server, p *params) ([]byte, error) {
	return renderHeader(blockByNumberArg(s, p))
}

func ethGetHeaderByHash(s *Server, p *params) ([]byte, error) {
	return renderHeader(blockByHashArg(s, p))
}

func txCount(b *BlockData, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if b == nil {
		return resultNull, nil
	}
	return quantityResult(uint64(len(b.Block.Transactions))), nil
}

func ethTxCountByNumber(s *Server, p *params) ([]byte, error) { return txCount(blockByNumberArg(s, p)) }

func ethTxCountByHash(s *Server, p *params) ([]byte, error) { return txCount(blockByHashArg(s, p)) }

func uncleCount(b *BlockData, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if b == nil {
		return resultNull, nil
	}
	return quantityResult(uint64(len(b.Block.Ommers))), nil
}

func ethUncleCountByNumber(s *Server, p *params) ([]byte, error) {
	return uncleCount(blockByNumberArg(s, p))
}

func ethUncleCountByHash(s *Server, p *params) ([]byte, error) {
	return uncleCount(blockByHashArg(s, p))
}

func uncleAt(p *params, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	raw, err := p.required("index")
	if err != nil {
		return nil, err
	}
	if _, err := indexParam(raw); err != nil {
		return nil, err
	}
	return resultNull, nil
}

func ethUncleByNumber(s *Server, p *params) ([]byte, error) {
	_, err := blockByNumberArg(s, p)
	return uncleAt(p, err)
}

func ethUncleByHash(s *Server, p *params) ([]byte, error) {
	_, err := blockByHashArg(s, p)
	return uncleAt(p, err)
}

type blockAt struct {
	b   *BlockData
	ref blockRef
}

func blockIDArg(s *Server, p *params) (blockAt, error) {
	raw, err := p.required("block_id")
	if err != nil {
		return blockAt{}, err
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return blockAt{}, err
	}
	return blockAt{b: s.blockFor(ref), ref: ref}, nil
}

func ethGetBlockReceipts(s *Server, p *params) ([]byte, error) {
	at, err := blockIDArg(s, p)
	if err != nil {
		return nil, err
	}
	b := at.b
	if b == nil {
		return resultNull, nil
	}
	if b.Receipts == nil {
		return resultNull, nil
	}
	return encode(func(w *writer) {
		w.arrOpen()
		logIndex := uint64(0)
		for i := range b.Receipts {
			writeReceipt(w, b, i, logIndex)
			logIndex += uint64(len(b.Receipts[i].Logs))
		}
		w.arrClose()
	}), nil
}

type txRef struct {
	b *BlockData
	i int
}

func (s *Server) txByHash(h primitives.Hash) txRef {
	loc, ok := s.chain.TxLocation(h)
	if !ok {
		return txRef{}
	}
	if loc.Block > s.chain.Head() {
		return txRef{}
	}
	b, ok := s.chain.Block(loc.Block)
	if !ok {
		return txRef{}
	}
	if loc.Index >= len(b.Block.Transactions) {
		return txRef{}
	}
	return txRef{b: b, i: loc.Index}
}

func txByHashArg(s *Server, p *params) (txRef, error) {
	raw, err := p.required("hash")
	if err != nil {
		return txRef{}, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return txRef{}, err
	}
	return s.txByHash(h), nil
}

func renderTx(t txRef, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if t.b == nil {
		return resultNull, nil
	}
	return encode(func(w *writer) { writeTx(w, t.b, t.i) }), nil
}

func renderRawTx(t txRef, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if t.b == nil {
		return resultNull, nil
	}
	return dataResult(t.b.Block.Transactions[t.i].Encode()), nil
}

func ethGetTransactionByHash(s *Server, p *params) ([]byte, error) {
	return renderTx(txByHashArg(s, p))
}

func ethGetRawTransactionByHash(s *Server, p *params) ([]byte, error) {
	return renderRawTx(txByHashArg(s, p))
}

func txAt(p *params, b *BlockData, err error) (txRef, error) {
	if err != nil {
		return txRef{}, err
	}
	raw, err := p.required("index")
	if err != nil {
		return txRef{}, err
	}
	i, err := indexParam(raw)
	if err != nil {
		return txRef{}, err
	}
	if b == nil {
		return txRef{}, nil
	}
	if i >= uint64(len(b.Block.Transactions)) {
		return txRef{}, nil
	}
	return txRef{b: b, i: int(i)}, nil
}

func ethTxByNumberAndIndex(s *Server, p *params) ([]byte, error) {
	b, err := blockByNumberArg(s, p)
	return renderTx(txAt(p, b, err))
}

func ethTxByHashAndIndex(s *Server, p *params) ([]byte, error) {
	b, err := blockByHashArg(s, p)
	return renderTx(txAt(p, b, err))
}

func ethRawTxByNumberAndIndex(s *Server, p *params) ([]byte, error) {
	b, err := blockByNumberArg(s, p)
	return renderRawTx(txAt(p, b, err))
}

func ethRawTxByHashAndIndex(s *Server, p *params) ([]byte, error) {
	b, err := blockByHashArg(s, p)
	return renderRawTx(txAt(p, b, err))
}

func ethGetTransactionReceipt(s *Server, p *params) ([]byte, error) {
	t, err := txByHashArg(s, p)
	if err != nil {
		return nil, err
	}
	if t.b == nil {
		return resultNull, nil
	}
	if t.b.Receipts == nil {
		return resultNull, nil
	}
	return encode(func(w *writer) { writeReceipt(w, t.b, t.i, firstLogIndex(t.b, t.i)) }), nil
}

func ethTxBySenderAndNonce(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("sender")
	if err != nil {
		return nil, err
	}
	sender, err := addressParam(raw)
	if err != nil {
		return nil, err
	}
	raw, err = p.required("nonce")
	if err != nil {
		return nil, err
	}
	nonce, err := quantityParam(raw)
	if err != nil {
		return nil, err
	}
	head := s.chain.Head()
	scan := min(head+1, senderScanBlocks)
	for k := range scan {
		b, ok := s.chain.Block(head - k)
		if !ok {
			continue
		}
		if t := findSenderNonce(b, sender, nonce); t.b != nil {
			return encode(func(w *writer) { writeTx(w, t.b, t.i) }), nil
		}
	}
	return resultNull, nil
}

type senderNonce struct {
	sender primitives.Address
	nonce  uint64
}

func findSenderNonce(b *BlockData, sender primitives.Address, nonce uint64) txRef {
	want := senderNonce{sender: sender, nonce: nonce}
	for i, tx := range b.Block.Transactions {
		if twoDimensional(tx) {
			continue
		}
		if (senderNonce{sender: b.Sender(i), nonce: txNonce(tx)}) == want {
			return txRef{b: b, i: i}
		}
	}
	return txRef{}
}

func twoDimensional(tx types.Transaction) bool {
	t, ok := tx.Data.(*types.TempoTx)
	return ok && !t.NonceKey.IsZero()
}

type accountAt struct {
	addr primitives.Address
	st   StateReader
}

func addressAndBlock(s *Server, p *params) (accountAt, error) {
	raw, err := p.required("address")
	if err != nil {
		return accountAt{}, err
	}
	addr, err := addressParam(raw)
	if err != nil {
		return accountAt{}, err
	}
	ref, err := blockIDParam(p.optional("block_number"))
	if err != nil {
		return accountAt{}, err
	}
	st, err := s.stateAt(ref)
	if err != nil {
		return accountAt{}, err
	}
	return accountAt{addr: addr, st: st}, nil
}

func ethGetBalance(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("address")
	if err != nil {
		return nil, err
	}
	if _, err := addressParam(raw); err != nil {
		return nil, err
	}
	if _, err := blockIDParam(p.optional("block_number")); err != nil {
		return nil, err
	}
	return balancePlaceholder, nil
}

func ethGetTransactionCount(s *Server, p *params) ([]byte, error) {
	a, err := addressAndBlock(s, p)
	if err != nil {
		return nil, err
	}
	return quantityResult(a.st.Nonce(a.addr)), nil
}

func ethGetCode(s *Server, p *params) ([]byte, error) {
	a, err := addressAndBlock(s, p)
	if err != nil {
		return nil, err
	}
	return dataResult(a.st.Code(a.addr)), nil
}

func ethGetStorageAt(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("address")
	if err != nil {
		return nil, err
	}
	addr, err := addressParam(raw)
	if err != nil {
		return nil, err
	}
	raw, err = p.required("index")
	if err != nil {
		return nil, err
	}
	key, err := storageKeyParam(raw)
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(p.optional("block_number"))
	if err != nil {
		return nil, err
	}
	st, err := s.stateAt(ref)
	if err != nil {
		return nil, err
	}
	v := st.Storage(addr, key).Bytes32()
	return dataResult(v[:]), nil
}

func ethGetStorageValues(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("requests")
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, typeError(raw, "a map")
	}
	var requests map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &requests); err != nil {
		return nil, invalidParams("invalid storage request map")
	}
	total := 0
	for _, keys := range requests {
		total += len(keys)
	}
	if total > storageValuesMax {
		return nil, newError(CodeInvalidParams, "too many storage keys")
	}
	type entry struct {
		addr primitives.Address
		keys []primitives.Hash
	}
	entries := make([]entry, 0, len(requests))
	for _, name := range slices.Sorted(maps.Keys(requests)) {
		addr, err := addressParam(json.RawMessage(strconv.Quote(name)))
		if err != nil {
			return nil, err
		}
		e := entry{addr: addr, keys: make([]primitives.Hash, 0, len(requests[name]))}
		for _, item := range requests[name] {
			k, err := storageKeyParam(item)
			if err != nil {
				return nil, err
			}
			e.keys = append(e.keys, k)
		}
		entries = append(entries, e)
	}
	ref, err := blockIDParam(p.optional("block_number"))
	if err != nil {
		return nil, err
	}
	st, err := s.stateAt(ref)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.objOpen()
		for _, e := range entries {
			w.key(field(e.addr.Hex()))
			w.arrOpen()
			for _, k := range e.keys {
				v := st.Storage(e.addr, k).Bytes32()
				w.data(v[:])
			}
			w.arrClose()
		}
		w.objClose()
	}), nil
}

func ethGetAccountInfo(s *Server, p *params) ([]byte, error) {
	a, err := addressAndBlock(s, p)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.kQuantity256("balance", a.st.Balance(a.addr))
		w.kQuantity("nonce", a.st.Nonce(a.addr))
		w.kData("code", a.st.Code(a.addr))
		w.objClose()
	}), nil
}

func (s *Server) proofWindow(p *params, name string) (uint64, error) {
	ref, err := blockIDParam(p.optional(name))
	if err != nil {
		return 0, err
	}
	n, err := s.stateBlock(ref)
	if err != nil {
		return 0, err
	}
	if s.chain.Head()-n > s.cfg.ProofWindow {
		return 0, newError(CodeProofWindow, "distance to target block exceeds maximum proof window")
	}
	return n, nil
}

func ethGetAccount(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("address")
	if err != nil {
		return nil, err
	}
	addr, err := addressParam(raw)
	if err != nil {
		return nil, err
	}
	at, err := s.proofWindow(p, "block")
	if err != nil {
		return nil, err
	}
	proof, err := s.accountProof(at, addr, nil)
	if err != nil {
		return nil, err
	}
	if !proof.Exists {
		return resultNull, nil
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.kQuantity("nonce", proof.Nonce)
		w.kQuantity256("balance", proof.Balance)
		w.kHash("storageRoot", proof.StorageRoot)
		w.kHash("codeHash", proof.CodeHash)
		w.objClose()
	}), nil
}

func (s *Server) accountProof(at uint64, addr primitives.Address, keys []storageKeyArg) (AccountProof, error) {
	src, ok := s.state.(ProofSource)
	if !ok {
		return AccountProof{}, internalError("account proofs are not available")
	}
	hashes := make([]primitives.Hash, len(keys))
	for i, k := range keys {
		hashes[i] = k.hash
	}
	proof, err := src.ProofAt(at, addr, hashes)
	if err != nil {
		return AccountProof{}, internalError(err.Error())
	}
	for i := range proof.Storage {
		proof.Storage[i].Key = keys[i].text
	}
	return proof, nil
}

type storageKeyArg struct {
	hash primitives.Hash
	text string
}

func storageKeysParam(raw json.RawMessage) ([]storageKeyArg, error) {
	var strs []json.RawMessage
	if err := json.Unmarshal(raw, &strs); err != nil {
		return nil, invalidParams("invalid storage keys")
	}
	out := make([]storageKeyArg, len(strs))
	for i, k := range strs {
		v, err := quantity256Param(k)
		if err != nil {
			return nil, err
		}
		b := v.Bytes32()
		out[i] = storageKeyArg{hash: b, text: v.Hex()}
		if len(k) == 4+2*primitives.HashLength {
			out[i].text = primitives.Hash(b).Hex()
		}
	}
	return out, nil
}

func ethGetProof(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("address")
	if err != nil {
		return nil, err
	}
	addr, err := addressParam(raw)
	if err != nil {
		return nil, err
	}
	rawKeys, err := p.required("keys")
	if err != nil {
		return nil, err
	}
	keys, err := storageKeysParam(rawKeys)
	if err != nil {
		return nil, err
	}
	at, err := s.proofWindow(p, "block_number")
	if err != nil {
		return nil, err
	}
	proof, err := s.accountProof(at, addr, keys)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) { writeAccountProof(w, addr, &proof) }), nil
}

func writeAccountProof(w *writer, addr primitives.Address, proof *AccountProof) {
	w.objOpen()
	w.kAddress("address", addr)
	w.kQuantity256("balance", proof.Balance)
	w.kHash("codeHash", proof.CodeHash)
	w.kQuantity("nonce", proof.Nonce)
	w.kHash("storageHash", proof.StorageRoot)
	w.key("accountProof")
	writeProofNodes(w, proof.Proof)
	w.key("storageProof")
	w.arrOpen()
	for _, sp := range proof.Storage {
		w.objOpen()
		w.kStr("key", sp.Key)
		w.kQuantity256("value", sp.Value)
		w.key("proof")
		writeProofNodes(w, sp.Proof)
		w.objClose()
	}
	w.arrClose()
	w.objClose()
}

func writeProofNodes(w *writer, nodes [][]byte) {
	w.arrOpen()
	for _, n := range nodes {
		w.data(n)
	}
	w.arrClose()
}

func rawBlockArg(s *Server, p *params) (*BlockData, error) {
	at, err := blockIDArg(s, p)
	if err != nil {
		return nil, err
	}
	if at.b == nil {
		return nil, blockNotFound(at.ref.describe())
	}
	return at.b, nil
}

func debugGetRawHeader(s *Server, p *params) ([]byte, error) {
	b, err := rawBlockArg(s, p)
	if err != nil {
		return nil, err
	}
	return dataResult(b.Block.Header.Encode()), nil
}

func debugGetRawBlock(s *Server, p *params) ([]byte, error) {
	b, err := rawBlockArg(s, p)
	if err != nil {
		return nil, err
	}
	return dataResult(b.Raw), nil
}

func debugGetRawReceipts(s *Server, p *params) ([]byte, error) {
	b, err := rawBlockArg(s, p)
	if err != nil {
		return nil, err
	}
	if b.Receipts == nil {
		return nil, blockNotFound("0x" + strconv.FormatUint(b.Number(), 16))
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for i := range b.Receipts {
			w.data(b.Receipts[i].Encode())
		}
		w.arrClose()
	}), nil
}

func ethSendRawTransaction(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("tx")
	if err != nil {
		return nil, err
	}
	b, err := bytesParam(raw)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, newError(CodeInvalidParams, "empty transaction data")
	}
	if _, err := types.DecodeTransaction(b); err != nil {
		return nil, newError(CodeInvalidParams, "failed to decode signed transaction")
	}
	return nil, newError(CodeServer, "transaction pool is not available on this follower")
}

func unknownAccount1(_ *Server, p *params) ([]byte, error) {
	if err := requireAll(p, "request"); err != nil {
		return nil, err
	}
	return nil, newError(CodeInvalidParams, "unknown account")
}

func unknownAccount2(_ *Server, p *params) ([]byte, error) {
	if err := requireAll(p, "address", "message"); err != nil {
		return nil, err
	}
	return nil, newError(CodeInvalidParams, "unknown account")
}

func subscribeOverHTTP(_ *Server, _ *params) ([]byte, error) {
	return nil, internalError("Internal error")
}

func objectParam(p *params, name field, expected string) error {
	raw, err := p.required(string(name))
	if err != nil {
		return err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return typeError(raw, expected)
	}
	return nil
}

func ethGetTransactions(_ *Server, p *params) ([]byte, error) {
	if err := objectParam(p, "params", "struct PaginationParams"); err != nil {
		return nil, err
	}
	return nil, internalError("unimplemented")
}

func notServed(method string) handler {
	return func(_ *Server, p *params) ([]byte, error) {
		if _, err := p.required("params"); err != nil {
			return nil, err
		}
		return nil, internalError(method + " is not served by goempo yet")
	}
}

func consensusGetLatest(_ *Server, _ *params) ([]byte, error) {
	return nil, internalError("consensus_getLatest is not served by goempo yet")
}

func txpoolContentFrom(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("from")
	if err != nil {
		return nil, err
	}
	if _, err := addressParam(raw); err != nil {
		return nil, err
	}
	return []byte(`{"pending":{},"queued":{}}`), nil
}
