package precompiles_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

func creditsCtx(fork hardfork.Fork) *precompiles.Context {
	st := memstate.New()
	st.SetCode(precompiles.StorageCreditsAddress, []byte{0xef})
	st.SetStorage(precompiles.StorageCreditsAddress, wordHash(alice), u256.FromUint64(42))
	st.FinalizeTx()
	c := precompiles.NewContext(st, fork, 1_000_000)
	c.Caller = alice
	return c
}

func wordHash(a [20]byte) [32]byte {
	var h [32]byte
	copy(h[12:], a[:])
	return h
}

func creditsCall(c *precompiles.Context, sig string, args ...[]byte) ([]byte, error) {
	return precompiles.Call(c, precompiles.StorageCreditsAddress, calldata(sig, args...))
}

func creditsView(c *precompiles.Context, sig string) []byte {
	out, err := creditsCall(c, sig, word(alice))
	assert.NoError(err, sig)
	return out
}

func revertData(err error) ([]byte, bool) {
	var rv *precompiles.Revert
	if !errors.As(err, &rv) {
		return nil, false
	}
	return rv.Data, true
}

func TestStorageCreditsViews(t *testing.T) {
	c := creditsCtx(hardfork.T7)
	out, err := creditsCall(c, "balanceOf(address)", word(alice))
	assert.NoError(err, "balanceOf")
	assert.Ok(bytes.Equal(out, amount(42)), "balance")
	assert.Equal(c.GasUsed(), uint64(2*6+2100), "input words plus cold sload")
	out, err = creditsCall(c, "modeOf(address)", word(alice))
	assert.NoError(err, "modeOf")
	assert.Ok(bytes.Equal(out, amount(0)), "default refund mode")
	out, err = creditsCall(c, "budgetOf(address)", word(bob))
	assert.NoError(err, "budgetOf")
	assert.Ok(bytes.Equal(out, amount(0)), "default budget")
}

func TestStorageCreditsSetModeAndBudget(t *testing.T) {
	c := creditsCtx(hardfork.T7)
	_, err := creditsCall(c, "setBudget(uint64)", amount(7))
	assert.NoError(err, "setBudget")
	out := creditsView(c, "modeOf(address)")
	assert.Ok(bytes.Equal(out, amount(2)), "setBudget selects direct")
	out = creditsView(c, "budgetOf(address)")
	assert.Ok(bytes.Equal(out, amount(7)), "budget 7")
	_, err = creditsCall(c, "setBudget(uint64)", amount(0))
	assert.NoError(err, "setBudget zero")
	out = creditsView(c, "modeOf(address)")
	assert.Ok(bytes.Equal(out, amount(2)), "zero budget stays direct")
	_, err = creditsCall(c, "setMode(uint8)", amount(2))
	assert.NoError(err, "setMode direct")
	out = creditsView(c, "budgetOf(address)")
	maxBudget := u256.FromUint64(^uint64(0)).Bytes32()
	assert.Ok(bytes.Equal(out, maxBudget[:]), "direct mode budget is unlimited")
	_, err = creditsCall(c, "setMode(uint8)", amount(1))
	assert.NoError(err, "setMode preserve")
	out = creditsView(c, "budgetOf(address)")
	assert.Ok(bytes.Equal(out, amount(0)), "preserve clears budget")
	out = creditsView(c, "balanceOf(address)")
	assert.Ok(bytes.Equal(out, amount(42)), "persistent balance untouched")
}

func TestStorageCreditsInvalidMode(t *testing.T) {
	pre, err := creditsCall(creditsCtx(hardfork.T10), "setMode(uint8)", amount(3))
	assert.Ok(pre == nil, "no output")
	data, ok := revertData(err)
	assert.Ok(ok, "pre-T11 reverts")
	assert.Ok(bytes.Equal(data, selectorOf("InvalidMode()")), "pre-T11 InvalidMode")
	_, err = creditsCall(creditsCtx(hardfork.T11), "setMode(uint8)", amount(3))
	data, ok = revertData(err)
	assert.Ok(ok, "T11 reverts")
	assert.Equal(len(data), 0, "T11 strict decoder reverts empty")
	_, err = creditsCall(creditsCtx(hardfork.T10), "setMode(uint8)", amount(0x101))
	assert.NoError(err, "pre-T11 lenient decode keeps the low byte")
}

func TestStorageCreditsStatic(t *testing.T) {
	c := creditsCtx(hardfork.T7)
	c.Static = true
	_, err := creditsCall(c, "setBudget(uint64)", amount(1))
	data, ok := revertData(err)
	assert.Ok(ok, "static mutate reverts")
	assert.Ok(bytes.Equal(data, selectorOf("StaticCallNotAllowed()")), "static error")
	_, err = creditsCall(c, "balanceOf(address)", word(alice))
	assert.NoError(err, "static view")
}

func TestStorageCreditsMainnetBalances(t *testing.T) {
	st := memstate.New()
	st.SetCode(precompiles.StorageCreditsAddress, []byte{0xef})
	st.SetStorage(precompiles.StorageCreditsAddress, wordHash(precompiles.ChannelReserveAddress), u256.FromUint64(0x97))
	st.SetStorage(precompiles.StorageCreditsAddress, wordHash(precompiles.StablecoinDEXAddress), u256.FromUint64(0xb76))
	st.FinalizeTx()
	c := precompiles.NewContext(st, hardfork.T13, 1_000_000)
	out, err := creditsCall(c, "balanceOf(address)", word(precompiles.ChannelReserveAddress))
	assert.NoError(err, "channel reserve credits")
	assert.Ok(bytes.Equal(out, amount(0x97)), "eth_call at block 48400138 returned 0x97")
	out, err = creditsCall(c, "balanceOf(address)", word(precompiles.StablecoinDEXAddress))
	assert.NoError(err, "dex credits")
	assert.Ok(bytes.Equal(out, amount(0xb76)), "eth_call at block 48400138 returned 0xb76")
	out, err = creditsCall(c, "modeOf(address)", word(precompiles.StablecoinDEXAddress))
	assert.NoError(err, "dex mode")
	assert.Ok(bytes.Equal(out, amount(0)), "eth_call returned mode 0")
}
