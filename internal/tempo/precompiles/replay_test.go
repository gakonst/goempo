package precompiles_test

import (
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type replayCall struct {
	To       string `json:"to"`
	Input    string `json:"input"`
	Gas      uint64 `json:"gas"`
	GasUsed  uint64 `json:"gasUsed"`
	TopLevel bool   `json:"topLevel"`
	Reverted bool   `json:"reverted"`
}

type replayAccount struct {
	Code    bool              `json:"code"`
	Storage map[string]string `json:"storage"`
}

type replayLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type replayLimit struct {
	Token  string `json:"token"`
	Amount string `json:"amount"`
	Period uint64 `json:"period"`
}

type replayRule struct {
	Selector   string   `json:"selector"`
	Recipients []string `json:"recipients"`
}

type replayScope struct {
	Target string       `json:"target"`
	Rules  []replayRule `json:"rules"`
}

type replayKeyAuth struct {
	KeyType      uint8         `json:"keyType"`
	KeyID        string        `json:"keyId"`
	Expiry       *uint64       `json:"expiry"`
	Limits       []replayLimit `json:"limits"`
	AllowedCalls []replayScope `json:"allowedCalls"`
	Witness      *string       `json:"witness"`
	IsAdmin      bool          `json:"isAdmin"`
}

type replayFixture struct {
	KeyAuth      *replayKeyAuth               `json:"keyAuth"`
	KeyID        string                       `json:"keyId"`
	SigType      uint8                        `json:"sigType"`
	Source       string                       `json:"source"`
	ChainID      uint64                       `json:"chainId"`
	Number       uint64                       `json:"number"`
	Timestamp    uint64                       `json:"timestamp"`
	Coinbase     string                       `json:"coinbase"`
	Sender       string                       `json:"sender"`
	FeePayer     string                       `json:"feePayer"`
	FeeToken     string                       `json:"feeToken"`
	MaxFee       string                       `json:"maxFee"`
	ActualFee    string                       `json:"actualFee"`
	ExpiringHash string                       `json:"expiringHash"`
	ValidBefore  uint64                       `json:"validBefore"`
	Calls        []replayCall                 `json:"calls"`
	Pre          map[string]replayAccount     `json:"pre"`
	Post         map[string]map[string]string `json:"post"`
	Logs         []replayLog                  `json:"logs"`
}

func mustAddr(s string) primitives.Address {
	a, err := primitives.HexToAddress(s)
	assert.NoError(err, "address hex")
	return a
}

func mustHash(s string) primitives.Hash {
	h, err := primitives.HexToHash(s)
	assert.NoError(err, "hash hex")
	return h
}

func mustWord(s string) u256.Int {
	w, err := u256.FromHex(s)
	assert.NoError(err, "word hex")
	return w
}

func mustBytes(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	assert.NoError(err, "bytes hex")
	return b
}

func loadState(fx replayFixture) *memstate.State {
	st := memstate.New()
	for _, a := range slices.Sorted(maps.Keys(fx.Pre)) {
		acct := fx.Pre[a]
		addr := mustAddr(a)
		if acct.Code {
			st.SetCode(addr, []byte{0xef})
		}
		for _, k := range slices.Sorted(maps.Keys(acct.Storage)) {
			st.SetStorage(addr, mustHash(k), mustWord(acct.Storage[k]))
		}
	}
	st.FinalizeTx()
	return st
}

type replayRun struct {
	fx   replayFixture
	st   *memstate.State
	fork hardfork.Fork
}

func (r replayRun) context(gas uint64) *precompiles.Context {
	c := precompiles.NewContext(r.st, r.fork, gas)
	c.Block.Number = r.fx.Number
	c.Block.Time = r.fx.Timestamp
	c.Block.Coinbase = mustAddr(r.fx.Coinbase)
	c.ChainID = u256.FromUint64(r.fx.ChainID)
	c.Caller = mustAddr(r.fx.Sender)
	return c
}

const handlerGas = uint64(1) << 62

func replay(t *testing.T, path string) {
	raw, err := os.ReadFile(path)
	assert.NoError(err, "read fixture")
	var fx replayFixture
	assert.NoError(json.Unmarshal(raw, &fx), "parse fixture")
	run := replayRun{fx: fx, st: loadState(fx), fork: hardfork.At(hardfork.ChainID(fx.ChainID), fx.Timestamp)}
	st := run.st
	st.WarmAddress(mustAddr(fx.Sender))
	st.WarmAddress(mustAddr(fx.Coinbase))
	h := run.context(handlerGas)
	sameTx := fx.KeyAuth != nil && fx.KeyAuth.KeyID == fx.KeyID
	if fx.KeyID != "" && !sameTx {
		precompiles.SetTxOrigin(h, mustAddr(fx.Sender))
		use := precompiles.KeyUse{Account: mustAddr(fx.Sender), KeyID: mustAddr(fx.KeyID), SigType: fx.SigType, Check: run.fork >= hardfork.T1}
		_, err := precompiles.ValidateKeychainAuthorization(h, use)
		assert.NoError(err, "keychain authorization")
		precompiles.SetTransactionKey(h, mustAddr(fx.KeyID))
	}
	maxFee, actual := mustWord(fx.MaxFee), mustWord(fx.ActualFee)
	collection := precompiles.FeeCollection{FeePayer: mustAddr(fx.FeePayer), Token: mustAddr(fx.FeeToken), MaxAmount: maxFee, Beneficiary: mustAddr(fx.Coinbase)}
	_, err = precompiles.CollectFeePreTx(h, collection)
	assert.NoError(err, "collect fee pre tx")
	if fx.KeyAuth != nil {
		applyKeyAuth(run, h, fx)
	}
	if run.fork >= hardfork.T7 {
		precompiles.MarkFeeSlotsNonCreditable(h, precompiles.FeeSlots{FeePayer: mustAddr(fx.FeePayer), FeeToken: mustAddr(fx.FeeToken)})
	}
	if fx.ExpiringHash != "" {
		assert.NoError(precompiles.CheckAndMarkExpiringNonce(h, mustHash(fx.ExpiringHash), fx.ValidBefore), "expiring nonce")
	}
	for _, call := range fx.Calls {
		gas := call.Gas
		if call.TopLevel {
			gas = handlerGas
		}
		c := run.context(gas)
		c.Env = h.Env
		to := mustAddr(call.To)
		st.WarmAddress(to)
		if !precompiles.IsPrecompile(to, run.fork) {
			continue
		}
		snap := st.Snapshot()
		_, err := precompiles.Call(c, to, mustBytes(call.Input))
		assert.Equal(err != nil, call.Reverted, "call revert status "+call.To)
		if err != nil {
			st.RevertToSnapshot(snap)
		}
		if !call.TopLevel {
			assert.Equal(c.GasUsed(), call.GasUsed, "call gas used "+call.To)
		}
	}
	settlement := precompiles.FeeSettlement{FeePayer: mustAddr(fx.FeePayer), Actual: actual, Refund: maxFee.Sub(actual), FeeToken: mustAddr(fx.FeeToken), Beneficiary: mustAddr(fx.Coinbase)}
	_, err = precompiles.CollectFeePostTx(h, settlement)
	assert.NoError(err, "collect fee post tx")
	_, err = precompiles.SettleStorageCreditRefunds(h)
	assert.NoError(err, "settle storage credit refunds")
	checkStorage(fx, st)
	checkLogs(fx, st)
}

func checkStorage(fx replayFixture, st *memstate.State) {
	for _, a := range slices.Sorted(maps.Keys(fx.Pre)) {
		acct := fx.Pre[a]
		addr := mustAddr(a)
		for _, k := range slices.Sorted(maps.Keys(acct.Storage)) {
			want := acct.Storage[k]
			if post, ok := fx.Post[a][k]; ok {
				want = post
			}
			assert.Equal(st.Storage(addr, mustHash(k)).Hex(), mustWord(want).Hex(), "slot "+a+" "+k)
		}
	}
	for _, a := range slices.Sorted(maps.Keys(fx.Post)) {
		slots := fx.Post[a]
		addr := mustAddr(a)
		for _, k := range slices.Sorted(maps.Keys(slots)) {
			assert.Equal(st.Storage(addr, mustHash(k)).Hex(), mustWord(slots[k]).Hex(), "post slot "+a+" "+k)
		}
	}
}

func checkLogs(fx replayFixture, st *memstate.State) {
	logs := st.Logs()
	assert.Equal(len(logs), len(fx.Logs), "log count")
	for i, want := range fx.Logs {
		got := logs[i]
		assert.Equal(got.Address, mustAddr(want.Address), "log address")
		assert.Equal(len(got.Topics), len(want.Topics), "topic count")
		for j, topic := range want.Topics {
			assert.Equal(got.Topics[j], mustHash(topic), "topic")
		}
		assert.Equal(hex.EncodeToString(got.Data), strings.TrimPrefix(want.Data, "0x"), "log data")
	}
}

func TestReplayMainnet(t *testing.T) {
	paths, err := filepath.Glob("testdata/mainnet/*.json")
	assert.NoError(err, "glob fixtures")
	assert.Ok(len(paths) > 0, "fixtures present")
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) { replay(t, p) })
	}
}

func keyGrant(fx replayFixture) precompiles.KeyGrant {
	ka := fx.KeyAuth
	r := precompiles.KeyRestrictions{Expiry: ^uint64(0), EnforceLimits: ka.Limits != nil, AllowAnyCalls: ka.AllowedCalls == nil}
	if ka.Expiry != nil {
		r.Expiry = *ka.Expiry
	}
	for _, l := range ka.Limits {
		r.Limits = append(r.Limits, precompiles.TokenLimit{Token: mustAddr(l.Token), Amount: mustWord(l.Amount), Period: l.Period})
	}
	for _, sc := range ka.AllowedCalls {
		scope := precompiles.CallScope{Target: mustAddr(sc.Target)}
		for _, rule := range sc.Rules {
			sr := precompiles.SelectorRule{Selector: [4]byte(mustBytes(rule.Selector))}
			for _, rc := range rule.Recipients {
				sr.Recipients = append(sr.Recipients, mustAddr(rc))
			}
			scope.SelectorRules = append(scope.SelectorRules, sr)
		}
		r.AllowedCalls = append(r.AllowedCalls, scope)
	}
	g := precompiles.KeyGrant{Account: mustAddr(fx.Sender), KeyID: mustAddr(ka.KeyID), SigType: ka.KeyType, Admin: ka.IsAdmin, Restrictions: r}
	if ka.Witness != nil {
		w := mustHash(*ka.Witness)
		g.Witness = &w
	}
	return g
}

func applyKeyAuth(run replayRun, h *precompiles.Context, fx replayFixture) {
	precompiles.SetTxOrigin(h, mustAddr(fx.Sender))
	k := run.context(handlerGas)
	k.Env = h.Env
	k.SkipStorageCredits = true
	assert.NoError(precompiles.AuthorizeKey(k, keyGrant(fx)), "authorize key")
	if fx.KeyAuth.KeyID != fx.KeyID {
		return
	}
	precompiles.SetTransactionKey(h, mustAddr(fx.KeyID))
	maxFee := mustWord(fx.MaxFee)
	if maxFee.IsZero() {
		return
	}
	flow := precompiles.TokenFlow{Account: mustAddr(fx.FeePayer), Token: mustAddr(fx.FeeToken), Amount: maxFee}
	assert.NoError(precompiles.AuthorizeTransfer(h, flow), "authorize fee transfer")
}
