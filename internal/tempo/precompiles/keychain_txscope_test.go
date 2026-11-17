package precompiles_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/kapetan-io/tiger/assert"
)

var scopedKey = primitives.Address{19: 0x5c}

const grantGas = 30_000_000

func grantScoped(fork hardfork.Fork, scopes []precompiles.CallScope) *precompiles.Context {
	f := setupToken(fork)
	f.c = precompiles.NewContext(f.st, fork, grantGas)
	f.c.Caller = alice
	r := precompiles.KeyRestrictions{Expiry: 1 << 40, AllowAnyCalls: scopes == nil, AllowedCalls: scopes}
	if fork >= hardfork.T3 {
		precompiles.SetTxOrigin(f.c, alice)
		err := precompiles.AuthorizeKey(f.c, precompiles.KeyGrant{Account: alice, KeyID: scopedKey, Restrictions: r})
		assert.NoError(err, "authorize key")
	}
	return precompiles.NewContext(f.st, fork, 1_000_000)
}

func assertNotAllowed(err error, what string) {
	rv, ok := err.(*precompiles.Revert)
	assert.Ok(ok, what+": reverts")
	assert.Equal(string(rv.Data), string(selectorOf("CallNotAllowed()")), what+": CallNotAllowed")
}

func TestTxCallScope(t *testing.T) {
	var transfer [4]byte
	copy(transfer[:], selectorOf("transfer(address,uint256)"))
	scopes := []precompiles.CallScope{{Target: precompiles.PathUSDAddress, SelectorRules: []precompiles.SelectorRule{{Selector: transfer, Recipients: []primitives.Address{bob}}}}}
	c := grantScoped(hardfork.T3, scopes)
	token := precompiles.PathUSDAddress
	other := primitives.Address{19: 0x77}
	check := func(to *primitives.Address, input []byte) error {
		return precompiles.ValidateTxCallScope(c, precompiles.TxCall{Account: alice, KeyID: scopedKey, To: to, Input: input})
	}
	assert.NoError(check(&token, calldata("transfer(address,uint256)", word(bob), amount(1))), "allowed recipient")
	assertNotAllowed(check(&token, calldata("transfer(address,uint256)", word(alice), amount(1))), "other recipient")
	assertNotAllowed(check(&token, calldata("approve(address,uint256)", word(bob), amount(1))), "other selector")
	assertNotAllowed(check(&other, calldata("transfer(address,uint256)", word(bob), amount(1))), "other target")
	assertNotAllowed(check(nil, nil), "create")
	assertNotAllowed(check(&token, selectorOf("transfer(address,uint256)")), "recipient word missing")
	dirty := calldata("transfer(address,uint256)", word(bob), amount(1))
	dirty[4] = 1
	assertNotAllowed(check(&token, dirty), "recipient word with high bytes")
	assert.NoError(precompiles.ValidateTxCallScope(c, precompiles.TxCall{Account: alice, To: &other}), "root key is unrestricted")
	assert.Ok(c.GasUsed() > 0, "scope reads are metered")
}

func TestTxCallScopeUnscopedAndPreT3(t *testing.T) {
	other := primitives.Address{19: 0x77}
	for _, fork := range []hardfork.Fork{hardfork.T2, hardfork.T3} {
		c := grantScoped(fork, nil)
		err := precompiles.ValidateTxCallScope(c, precompiles.TxCall{Account: alice, KeyID: scopedKey, To: &other, Input: []byte{1, 2, 3, 4}})
		assert.NoError(err, "unscoped key or pre-T3 allows any call")
	}
	c := grantScoped(hardfork.T2, nil)
	assert.NoError(precompiles.ValidateTxCallScope(c, precompiles.TxCall{Account: alice, KeyID: scopedKey}), "pre-T3 create allowed")
	assert.Equal(c.GasUsed(), uint64(0), "pre-T3 reads nothing")
}
