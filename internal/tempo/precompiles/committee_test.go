package precompiles_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type committeeFixture struct {
	Timestamp uint64   `json:"timestamp"`
	Epoch     uint64   `json:"epoch"`
	Keys      []string `json:"keys"`
}

func TestCommitteeMatchesMainnetStorage(t *testing.T) {
	raw, err := os.ReadFile("testdata/committee/epoch1502.json")
	assert.NoError(err, "read fixture")
	var fx committeeFixture
	assert.NoError(json.Unmarshal(raw, &fx), "parse fixture")
	st := memstate.New()
	c := precompiles.NewContext(st, hardfork.At(hardfork.MainnetChainID, fx.Timestamp), 30_000_000)
	input := calldata("setCommitteeMembers(uint64,bytes32[])", amount(fx.Epoch), amount(64), amount(uint64(len(fx.Keys))))
	for _, k := range fx.Keys {
		input = append(input, mustBytes(k)...)
	}
	_, err = precompiles.Call(c, precompiles.CurrentCommitteeAddress, input)
	assert.NoError(err, "set committee")
	assert.Equal(st.Storage(precompiles.CurrentCommitteeAddress, primitives.Hash{}), u256.FromUint64(fx.Epoch), "epoch slot")
	lenSlot := primitives.Hash{31: 1}
	assert.Equal(st.Storage(precompiles.CurrentCommitteeAddress, lenSlot), u256.FromUint64(uint64(len(fx.Keys))), "length slot")
	base := u256.FromBytes32(keccak.Sum256(lenSlot[:]))
	for i, k := range fx.Keys {
		s := primitives.Hash(base.Add(u256.FromUint64(uint64(i))).Bytes32())
		assert.Equal(st.Storage(precompiles.CurrentCommitteeAddress, s), mustWord(k), "member slot")
	}
	c.Caller = alice
	_, err = precompiles.Call(c, precompiles.CurrentCommitteeAddress, input)
	var rv *precompiles.Revert
	assert.Ok(errors.As(err, &rv), "non-system caller reverts")
}

func TestKeyAuthGasChargesOnlySstoreSetAndWarmRead(t *testing.T) {
	st := memstate.New()
	c := precompiles.NewContext(st, hardfork.T1, 1_000_000)
	c.KeyAuthGas = true
	c.SkipStorageCredits = true
	c.Block.Time = 1_770_908_400
	precompiles.SetTxOrigin(c, alice)
	grant := precompiles.KeyGrant{Account: alice, KeyID: bob, SigType: precompiles.SigP256, Restrictions: precompiles.KeyRestrictions{Expiry: ^uint64(0), AllowAnyCalls: true}}
	assert.NoError(precompiles.AuthorizeKey(c, grant), "authorize key")
	assert.Equal(c.GasUsed(), uint64(100+100+100+250_000+375), "tload, sload, pre-T4 struct load, new slot and log base")
	small := precompiles.NewContext(memstate.New(), hardfork.T1, 1_000)
	small.KeyAuthGas = true
	small.Block.Time = 1_770_908_400
	precompiles.SetTxOrigin(small, alice)
	err := precompiles.AuthorizeKey(small, grant)
	assert.Ok(errors.Is(err, precompiles.ErrOutOfGas), "out of gas is distinguishable")
}
