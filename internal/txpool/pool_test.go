package txpool_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

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

type env struct {
	db   *state.DB
	x    *executor.Executor
	head types.Header
}

func newEnv(t *testing.T) *env {
	db, err := state.Open(goio.NewMemFS(), state.Options{})
	assert.NoError(err, "open state")
	loadPrestate(db, "testdata/mainnet-49400084-tx0-prestate.json")
	var h types.Header
	h.Inner.Beneficiary = beneficiary
	h.Inner.Number = 100
	h.Inner.Timestamp = headTime
	h.Inner.GasLimit = 500_000_000
	h.Inner.BaseFeePerGas = baseFee
	return &env{db: db, x: &executor.Executor{ChainID: hardfork.MainnetChainID, Configs: chain.NewCache()}, head: h}
}

func (e *env) pool(cfg txpool.Config) *txpool.Pool {
	cfg.Executor = e.x
	cfg.Now = func() uint64 { return headTime }
	return txpool.New(cfg)
}

type signer struct {
	key  secp256k1.PrivateKey
	addr primitives.Address
}

type funding bool

const (
	funded funding = true
	broke  funding = false
)

func newSigner(t *testing.T, e *env, seed byte, fund funding) signer {
	var s signer
	s.key[31] = seed
	s.key[0] = 0x11
	addr, err := s.key.Address()
	assert.NoError(err, "address")
	s.addr = addr
	if fund {
		e.db.State().SetStorage(feeToken, precompiles.TIP20BalanceSlot(addr), u256.FromUint64(1_000_000_000_000))
	}
	return s
}

type txSpec struct {
	nonce  uint64
	maxFee uint64
	tip    uint64
	gas    uint64
}

func (s signer) sign(t *testing.T, spec txSpec) []byte {
	gas := spec.gas
	if gas == 0 {
		gas = transfer
	}
	tx := types.Transaction{Data: &types.DynamicFeeTx{
		ChainID:              uint64(hardfork.MainnetChainID),
		Nonce:                spec.nonce,
		MaxPriorityFeePerGas: u256.FromUint64(spec.tip),
		MaxFeePerGas:         u256.FromUint64(spec.maxFee),
		Gas:                  gas,
		To:                   &feeToken,
		Data:                 transferData(),
	}}
	sig, err := secp256k1.Sign(tx.SigningHash(), s.key)
	assert.NoError(err, "sign")
	d := tx.Data.(*types.DynamicFeeTx)
	d.Sig = types.Signature{R: sig.R, S: sig.S, YParity: sig.RecoveryID == 1}
	return tx.Encode()
}

func transferData() []byte {
	data := slices.Clone(transferSel)
	var to, amount [32]byte
	copy(to[12:], recipient[:])
	amount[31] = 1
	data = append(data, to[:]...)
	return append(data, amount[:]...)
}

func drain(b *txpool.Best) []*txpool.Tx {
	var out []*txpool.Tx
	for range 1000 {
		t, ok := b.Next()
		if !ok {
			break
		}
		out = append(out, t)
	}
	return out
}

func TestBestOrdersByTipThenArrival(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a, b, c := newSigner(t, e, 1, funded), newSigner(t, e, 2, funded), newSigner(t, e, 3, funded)
	ha, err := p.Add(a.sign(t, txSpec{maxFee: 2 * gwei, tip: 1}), e.db.State(), &e.head)
	assert.NoError(err, "add a")
	hb, err := p.Add(b.sign(t, txSpec{maxFee: 2 * gwei, tip: gwei}), e.db.State(), &e.head)
	assert.NoError(err, "add b")
	hc, err := p.Add(c.sign(t, txSpec{maxFee: 2 * gwei, tip: 1}), e.db.State(), &e.head)
	assert.NoError(err, "add c")
	got := drain(p.Best(txpool.BlockEnv{BaseFee: baseFee, Timestamp: headTime + 1}))
	assert.Equal(len(got), 3, "all three are ready")
	assert.Equal(got[0].Hash, hb, "highest tip first")
	assert.Equal(got[1].Hash, ha, "equal tips keep arrival order")
	assert.Equal(got[2].Hash, hc, "later arrival last")
}

func TestEffectiveTipIsCappedByFeeCap(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a, b := newSigner(t, e, 1, funded), newSigner(t, e, 2, funded)
	ha, err := p.Add(a.sign(t, txSpec{maxFee: baseFee + 10, tip: baseFee + 10}), e.db.State(), &e.head)
	assert.NoError(err, "add a")
	hb, err := p.Add(b.sign(t, txSpec{maxFee: 2 * gwei, tip: 100}), e.db.State(), &e.head)
	assert.NoError(err, "add b")
	got := drain(p.Best(txpool.BlockEnv{BaseFee: baseFee, Timestamp: headTime + 1}))
	assert.Equal(got[0].Hash, hb, "a tip of 100 beats a tip capped at 10")
	assert.Equal(got[1].Hash, ha, "capped tip second")
	none := drain(p.Best(txpool.BlockEnv{BaseFee: baseFee + 11, Timestamp: headTime + 1}))
	assert.Equal(len(none), 1, "a fee cap below the base fee is not ready")
}

func TestNonceLaneYieldsInNonceOrder(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a := newSigner(t, e, 1, funded)
	h2, err := p.Add(a.sign(t, txSpec{nonce: 2, maxFee: 2 * gwei, tip: gwei}), e.db.State(), &e.head)
	assert.NoError(err, "future nonce is pooled")
	h0, err := p.Add(a.sign(t, txSpec{nonce: 0, maxFee: 2 * gwei, tip: 1}), e.db.State(), &e.head)
	assert.NoError(err, "add nonce 0")
	h1, err := p.Add(a.sign(t, txSpec{nonce: 1, maxFee: 2 * gwei, tip: 2}), e.db.State(), &e.head)
	assert.NoError(err, "add nonce 1")
	got := drain(p.Best(txpool.BlockEnv{BaseFee: baseFee, Timestamp: headTime + 1}))
	assert.Equal(len(got), 3, "whole lane")
	assert.Equal(got[0].Hash, h0, "nonce 0 first")
	assert.Equal(got[1].Hash, h1, "nonce 1 second")
	assert.Equal(got[2].Hash, h2, "nonce 2 last despite the highest tip")
}

func TestGapBlocksTheRestOfTheLane(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a := newSigner(t, e, 1, funded)
	_, err := p.Add(a.sign(t, txSpec{nonce: 1, maxFee: 2 * gwei, tip: 1}), e.db.State(), &e.head)
	assert.NoError(err, "add nonce 1")
	assert.Equal(len(drain(p.Best(txpool.BlockEnv{BaseFee: baseFee, Timestamp: headTime + 1}))), 0, "nonce 1 waits for nonce 0")
}

func TestMarkInvalidSkipsTheLane(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a, b := newSigner(t, e, 1, funded), newSigner(t, e, 2, funded)
	for n := uint64(0); n < 3; n++ {
		_, err := p.Add(a.sign(t, txSpec{nonce: n, maxFee: 2 * gwei, tip: gwei}), e.db.State(), &e.head)
		assert.NoError(err, "add lane a")
	}
	hb, err := p.Add(b.sign(t, txSpec{maxFee: 2 * gwei, tip: 1}), e.db.State(), &e.head)
	assert.NoError(err, "add b")
	best := p.Best(txpool.BlockEnv{BaseFee: baseFee, Timestamp: headTime + 1})
	first, ok := best.Next()
	assert.Ok(ok, "a tx is ready")
	assert.Equal(first.Info.Sender, a.addr, "lane a leads")
	best.MarkInvalid(first)
	rest := drain(best)
	assert.Equal(len(rest), 1, "only the other lane remains")
	assert.Equal(rest[0].Hash, hb, "lane b")
}

func TestReplacementNeedsThePriceBump(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a := newSigner(t, e, 1, funded)
	_, err := p.Add(a.sign(t, txSpec{maxFee: 2 * gwei, tip: gwei}), e.db.State(), &e.head)
	assert.NoError(err, "add")
	_, err = p.Add(a.sign(t, txSpec{maxFee: 2 * gwei * 105 / 100, tip: gwei * 105 / 100}), e.db.State(), &e.head)
	assert.Ok(errors.Is(err, txpool.ErrReplacementUnderpriced), "5% bump is underpriced")
	_, err = p.Add(a.sign(t, txSpec{maxFee: 2 * gwei * 110 / 100, tip: gwei}), e.db.State(), &e.head)
	assert.Ok(errors.Is(err, txpool.ErrReplacementUnderpriced), "the tip must rise too")
	h, err := p.Add(a.sign(t, txSpec{maxFee: 2 * gwei * 110 / 100, tip: gwei * 110 / 100}), e.db.State(), &e.head)
	assert.NoError(err, "10% bump replaces")
	assert.Equal(p.Len(), 1, "replaced in place")
	_, ok := p.Get(h)
	assert.Ok(ok, "replacement pooled")
}

func TestSenderLimit(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{MaxPerSender: 2})
	a := newSigner(t, e, 1, funded)
	for n := uint64(0); n < 2; n++ {
		_, err := p.Add(a.sign(t, txSpec{nonce: n, maxFee: 2 * gwei}), e.db.State(), &e.head)
		assert.NoError(err, "within the limit")
	}
	_, err := p.Add(a.sign(t, txSpec{nonce: 2, maxFee: 2 * gwei}), e.db.State(), &e.head)
	assert.Ok(errors.Is(err, txpool.ErrSenderLimit), "third tx is over the limit")
}

func TestRejections(t *testing.T) {
	e := newEnv(t)
	rich := newSigner(t, e, 1, funded)
	poor := newSigner(t, e, 2, broke)
	ok := rich.sign(t, txSpec{maxFee: 2 * gwei})
	cases := []struct {
		name string
		cfg  txpool.Config
		raw  []byte
		want error
		text string
	}{
		{name: "oversized", cfg: txpool.Config{MaxTxBytes: 16}, raw: ok, want: txpool.ErrOversized},
		{name: "garbage", raw: []byte{0x02, 0x01}, want: txpool.ErrDecode},
		{name: "below the protocol base fee", raw: rich.sign(t, txSpec{maxFee: baseFee - 1}), want: txpool.ErrUnderpriced},
		{name: "below the configured fee cap", cfg: txpool.Config{MinFeeCap: 3 * gwei}, raw: ok, want: txpool.ErrUnderpriced},
		{name: "no fee token balance", raw: poor.sign(t, txSpec{maxFee: 2 * gwei}), want: txpool.ErrRejected, text: "fee"},
		{name: "gas below intrinsic", raw: rich.sign(t, txSpec{maxFee: 2 * gwei, gas: 20_000}), want: txpool.ErrRejected},
		{name: "tip above fee cap", raw: rich.sign(t, txSpec{maxFee: gwei, tip: 2 * gwei}), want: txpool.ErrTipAboveFeeCap},
	}
	for i := range cases {
		c := &cases[i]
		p := e.pool(c.cfg)
		_, err := p.Add(c.raw, e.db.State(), &e.head)
		assert.Ok(errors.Is(err, c.want), c.name+": "+errString(err))
		if c.text != "" {
			assert.Ok(strings.Contains(err.Error(), c.text), c.name+" names the reason: "+err.Error())
		}
		assert.Equal(p.Len(), 0, c.name+" is not pooled")
	}
}

func TestAlreadyKnownAndStaleNonce(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a := newSigner(t, e, 1, funded)
	raw := a.sign(t, txSpec{nonce: 3, maxFee: 2 * gwei})
	_, err := p.Add(raw, e.db.State(), &e.head)
	assert.NoError(err, "add")
	_, err = p.Add(raw, e.db.State(), &e.head)
	assert.Ok(errors.Is(err, txpool.ErrAlreadyKnown), "duplicate")
	e.db.State().SetNonce(a.addr, 5)
	_, err = p.Add(a.sign(t, txSpec{nonce: 4, maxFee: 2 * gwei}), e.db.State(), &e.head)
	assert.Ok(errors.Is(err, txpool.ErrRejected), "rejected: "+errString(err))
	assert.Ok(errors.Is(err, executor.ErrNonce), "nonce below the account's: "+errString(err))
}

func TestOnBlockDropsMinedAndStale(t *testing.T) {
	e := newEnv(t)
	p := e.pool(txpool.Config{})
	a := newSigner(t, e, 1, funded)
	raws := [][]byte{a.sign(t, txSpec{nonce: 0, maxFee: 2 * gwei}), a.sign(t, txSpec{nonce: 1, maxFee: 2 * gwei}), a.sign(t, txSpec{nonce: 2, maxFee: 2 * gwei})}
	for i := range raws {
		_, err := p.Add(raws[i], e.db.State(), &e.head)
		assert.NoError(err, "add")
	}
	mined, err := types.DecodeTransaction(raws[0])
	assert.NoError(err, "decode")
	e.db.State().SetNonce(a.addr, 2)
	blk := types.Block{Header: e.head, Transactions: []types.Transaction{mined}}
	blk.Header.Inner.Number++
	assert.NoError(p.OnBlock(&blk, e.db.State()), "on block")
	assert.Equal(p.Len(), 1, "mined and stale txs dropped")
	got := drain(p.Best(txpool.BlockEnv{BaseFee: baseFee, Timestamp: headTime + 2}))
	assert.Equal(len(got), 1, "nonce 2 is ready")
	assert.Equal(got[0].Info.Nonce, uint64(2), "the lane moved to the account nonce")
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
