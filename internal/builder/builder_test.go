package builder_test

import (
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/builder"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/txpool"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	headTime = 1_794_883_263
	gwei     = 1_000_000_000
	baseFee  = 600_000_000
	transfer = 1_000_000
	txCap    = 30_000_000
)

var (
	recipient   = primitives.Address{0x42}
	feeToken    = mustAddress("20c000000000000000000000b9537d11c60e8b50")
	beneficiary = mustAddress("fa377708130d3b25d0fb4e70f5a0f77498407057")
	transferSel = []byte{0xa9, 0x05, 0x9c, 0xbb}
)

func mustAddress(s string) primitives.Address {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "address literal")
	assert.Equal(len(b), primitives.AddressLength, "address length")
	return primitives.Address(b)
}

type prestateAccount struct {
	Balance string            `json:"balance"`
	Nonce   uint64            `json:"nonce"`
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

func hexBytes(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	assert.NoError(err, "hex")
	return b
}

func word(s string) u256.Int {
	var b [32]byte
	v := hexBytes(s)
	copy(b[32-len(v):], v)
	return u256.FromBytes32(b)
}

func loadPrestate(db *state.DB, path string) {
	data, err := os.ReadFile(path)
	assert.NoError(err, "read prestate")
	var accounts map[string]prestateAccount
	assert.NoError(json.Unmarshal(data, &accounts), "decode prestate")
	st := db.State()
	for _, k := range slices.Sorted(maps.Keys(accounts)) {
		a := accounts[k]
		addr := primitives.Address(hexBytes(k))
		st.CreateAccount(addr)
		st.SetNonce(addr, a.Nonce)
		if code := hexBytes(a.Code); len(code) != 0 {
			st.SetCode(addr, code)
		}
		for _, sk := range slices.Sorted(maps.Keys(a.Storage)) {
			st.SetStorage(addr, primitives.Hash(hexBytes(sk)), word(a.Storage[sk]))
		}
	}
	_, err = st.Commit(1)
	assert.NoError(err, "commit prestate")
}

func transferData() []byte {
	data := slices.Clone(transferSel)
	var to, amount [32]byte
	copy(to[12:], recipient[:])
	amount[31] = 1
	data = append(data, to[:]...)
	return append(data, amount[:]...)
}

func balanceOfData() []byte {
	data := []byte{0x70, 0xa0, 0x82, 0x31}
	var who [32]byte
	copy(who[12:], recipient[:])
	return append(data, who[:]...)
}

type env struct {
	db     *state.DB
	x      *executor.Executor
	parent types.Block
	pool   *txpool.Pool
}

func newEnv(t *testing.T) *env {
	db, err := state.Open(goio.NewMemFS(), state.Options{})
	assert.NoError(err, "open state")
	loadPrestate(db, "../txpool/testdata/mainnet-49400084-tx0-prestate.json")
	e := &env{db: db, x: &executor.Executor{ChainID: hardfork.MainnetChainID, Configs: chain.NewCache()}}
	h := &e.parent.Header
	h.Inner.Number = 1
	h.Inner.Timestamp = headTime
	h.Inner.GasLimit = 500_000_000
	h.Inner.BaseFeePerGas = baseFee
	h.Inner.Beneficiary = beneficiary
	h.Inner.OptionalFields = 6
	e.pool = txpool.New(txpool.Config{Executor: e.x, Now: func() uint64 { return headTime }})
	return e
}

func (e *env) add(t *testing.T, raw []byte) primitives.Hash {
	h, err := e.pool.Add(raw, e.db.State(), &e.parent.Header)
	assert.NoError(err, "pool add")
	return h
}

type signer struct {
	key  secp256k1.PrivateKey
	addr primitives.Address
}

func newSigner(t *testing.T, e *env, seed byte) signer {
	var s signer
	s.key[31] = seed
	s.key[0] = 0x11
	addr, err := s.key.Address()
	assert.NoError(err, "address")
	s.addr = addr
	e.db.State().SetStorage(feeToken, precompiles.TIP20BalanceSlot(addr), u256.FromUint64(1_000_000_000_000))
	var pref [32]byte
	copy(pref[12:], feeToken[:])
	e.db.State().SetStorage(precompiles.FeeManagerAddress, precompiles.FeeUserTokenSlot(addr), u256.FromBytes32(pref))
	return s
}

type txSpec struct {
	nonce uint64
	tip   uint64
	gas   uint64
	data  []byte
}

func (s signer) sign(t *testing.T, spec txSpec) []byte {
	gas := spec.gas
	if gas == 0 {
		gas = transfer
	}
	data := spec.data
	if data == nil {
		data = transferData()
	}
	tx := types.Transaction{Data: &types.DynamicFeeTx{
		ChainID:              uint64(hardfork.MainnetChainID),
		Nonce:                spec.nonce,
		MaxPriorityFeePerGas: u256.FromUint64(spec.tip),
		MaxFeePerGas:         u256.FromUint64(2 * gwei),
		Gas:                  gas,
		To:                   &feeToken,
		Data:                 data,
	}}
	sig, err := secp256k1.Sign(tx.SigningHash(), s.key)
	assert.NoError(err, "sign")
	tx.Data.(*types.DynamicFeeTx).Sig = types.Signature{R: sig.R, S: sig.S, YParity: sig.RecoveryID == 1}
	return tx.Encode()
}

func (e *env) fund(t *testing.T) {
	n := e.db.Head().Number + 1
	_, err := e.db.State().Commit(n)
	assert.NoError(err, "commit funding")
	e.parent.Header.Inner.Number = n
}

func noHash(uint64) primitives.Hash { return primitives.Hash{} }

func (e *env) build(t *testing.T) builder.Built {
	attrs := builder.Attributes{TimestampMillis: (headTime+1)*1000 + 250, Beneficiary: beneficiary, ExtraData: []byte("goempo"), GetHash: noHash}
	built, err := builder.New(e.pool).Build(e.db.State(), e.x, &e.parent, attrs)
	assert.NoError(err, "build")
	e.db.State().Discard()
	return built
}

func (e *env) replay(t *testing.T, blk *types.Block) {
	res, err := e.x.Execute(e.db.State(), executor.Input{Block: blk, GetHash: noHash})
	assert.NoError(err, "the executor accepts the built block")
	assert.Equal(res.GasUsed, blk.Header.Inner.GasUsed, "gas used")
	assert.Equal(res.ReceiptsRoot, blk.Header.Inner.ReceiptsRoot, "receipts root")
	assert.Equal(res.Bloom, blk.Header.Inner.LogsBloom, "bloom")
	root, err := e.db.State().Commit(blk.Header.Inner.Number)
	assert.NoError(err, "commit")
	assert.Equal(root, blk.Header.Inner.StateRoot, "state root")
}

func TestBuildIsAcceptedByTheExecutor(t *testing.T) {
	e := newEnv(t)
	a, b := newSigner(t, e, 1), newSigner(t, e, 2)
	e.fund(t)
	ha0 := e.add(t, a.sign(t, txSpec{nonce: 0, tip: 1}))
	ha1 := e.add(t, a.sign(t, txSpec{nonce: 1, tip: 1}))
	hb := e.add(t, b.sign(t, txSpec{nonce: 0, tip: gwei}))
	built := e.build(t)
	blk := &built.Block
	h := &blk.Header
	assert.Equal(len(blk.Transactions), 3, "all pool txs included")
	assert.Equal(blk.Transactions[0].Hash(), hb, "highest tip first")
	assert.Equal(blk.Transactions[1].Hash(), ha0, "then the lane in nonce order")
	assert.Equal(blk.Transactions[2].Hash(), ha1, "nonce 1 last")
	assert.Equal(h.Inner.Number, e.parent.Header.Inner.Number+1, "number")
	assert.Equal(h.Inner.ParentHash, e.parent.Hash(), "parent hash")
	assert.Equal(h.Inner.Timestamp, uint64(headTime+1), "timestamp")
	assert.Equal(h.TimestampMillisPart, uint64(250), "millis part")
	assert.Equal(h.Inner.BaseFeePerGas, uint64(baseFee), "base fee follows the parent")
	assert.Equal(h.GeneralGasLimit, uint64(txCap), "general lane limit")
	assert.Equal(h.SharedGasLimit, uint64(0), "no shared gas from T4")
	assert.Equal(string(h.Inner.ExtraData), "goempo", "extra data")
	assert.Equal(built.Stats.PaymentTxs, 3, "transfers are payments")
	e.replay(t, blk)
	decoded, err := types.DecodeBlock(blk.Encode())
	assert.NoError(err, "the block round-trips")
	assert.Equal(decoded.Hash(), blk.Hash(), "same hash after decoding")
}

func TestGeneralLaneCapsNonPaymentGas(t *testing.T) {
	e := newEnv(t)
	a, b, c := newSigner(t, e, 1), newSigner(t, e, 2), newSigner(t, e, 3)
	e.fund(t)
	first := e.add(t, a.sign(t, txSpec{tip: 3 * gwei / 2, gas: txCap, data: balanceOfData()}))
	e.add(t, b.sign(t, txSpec{tip: gwei, gas: txCap, data: balanceOfData()}))
	pay := e.add(t, c.sign(t, txSpec{tip: 1, gas: txCap}))
	built := e.build(t)
	blk := &built.Block
	assert.Equal(len(blk.Transactions), 2, "the second non-payment tx does not fit the general lane")
	assert.Equal(blk.Transactions[0].Hash(), first, "first non-payment tx")
	assert.Equal(blk.Transactions[1].Hash(), pay, "the payment tx bypasses the general lane")
	assert.Equal(built.Stats.Skipped, 1, "one skipped")
	e.replay(t, blk)
}

func TestEmptyPoolBuildsAnEmptyBlock(t *testing.T) {
	e := newEnv(t)
	built := e.build(t)
	assert.Equal(len(built.Block.Transactions), 0, "no txs")
	assert.Equal(built.Block.Header.Inner.GasUsed, uint64(0), "no gas")
	e.replay(t, &built.Block)
}

func TestPreT4TimestampsAreRefused(t *testing.T) {
	e := newEnv(t)
	e.parent.Header.Inner.Timestamp = 1_770_000_000
	attrs := builder.Attributes{TimestampMillis: 1_770_000_001_000, GetHash: noHash}
	_, err := builder.New(e.pool).Build(e.db.State(), e.x, &e.parent, attrs)
	assert.Ok(err != nil, "pre-T4 blocks carry a system transaction the builder does not make")
}
