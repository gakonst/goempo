package precompiles_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

var (
	alice = primitives.Address{19: 0xa1}
	bob   = primitives.Address{19: 0xb0}
)

func selectorOf(sig string) []byte {
	h := keccak.Sum256([]byte(sig))
	return h[:4]
}

func word(a primitives.Address) []byte {
	var w [32]byte
	copy(w[12:], a[:])
	return w[:]
}

func amount(v uint64) []byte {
	b := u256.FromUint64(v).Bytes32()
	return b[:]
}

func calldata(sig string, args ...[]byte) []byte {
	out := selectorOf(sig)
	for _, a := range args {
		out = append(out, a...)
	}
	return out
}

type fixture struct {
	st *memstate.State
	c  *precompiles.Context
}

func setupToken(fork hardfork.Fork) fixture {
	st := memstate.New()
	st.SetCode(precompiles.PathUSDAddress, []byte{0xef})
	st.SetStorage(precompiles.PathUSDAddress, precompiles.TIP20BalanceSlot(alice), u256.FromUint64(1_000))
	policySlot := primitives.Hash{31: 7}
	st.SetStorage(precompiles.PathUSDAddress, policySlot, u256.One().Lsh(160))
	st.FinalizeTx()
	c := precompiles.NewContext(st, fork, 1_000_000)
	c.Caller = alice
	return fixture{st: st, c: c}
}

func TestTIP20Transfer(t *testing.T) {
	f := setupToken(hardfork.T13)
	st, c := f.st, f.c
	out, err := precompiles.Call(c, precompiles.PathUSDAddress, calldata("transfer(address,uint256)", word(bob), amount(250)))
	assert.NoError(err, "transfer succeeds")
	assert.Equal(u256.FromBytes32([32]byte(out)), u256.One(), "returns true")
	got := st.Storage(precompiles.PathUSDAddress, precompiles.TIP20BalanceSlot(alice))
	assert.Equal(got, u256.FromUint64(750), "alice debited")
	got = st.Storage(precompiles.PathUSDAddress, precompiles.TIP20BalanceSlot(bob))
	assert.Equal(got, u256.FromUint64(250), "bob credited")
	logs := st.Logs()
	assert.Equal(len(logs), 1, "one Transfer log")
	assert.Equal(logs[0].Topics[0], keccak.Sum256([]byte("Transfer(address,address,uint256)")), "Transfer topic")
	assert.Ok(c.GasUsed() > 5_000, "charged storage gas")
}

func TestTIP20TransferInsufficientBalance(t *testing.T) {
	f := setupToken(hardfork.T13)
	st, c := f.st, f.c
	_, err := precompiles.Call(c, precompiles.PathUSDAddress, calldata("transfer(address,uint256)", word(bob), amount(5_000)))
	rv, ok := err.(*precompiles.Revert)
	assert.Ok(ok, "reverts")
	want := calldata("InsufficientBalance(uint256,uint256,address)", amount(1_000), amount(5_000), word(precompiles.PathUSDAddress))
	assert.Equal(string(rv.Data), string(want), "InsufficientBalance revert data")
	assert.Equal(len(st.Logs()), 0, "no logs")
}

func TestUnknownSelectorAndShortInput(t *testing.T) {
	c := setupToken(hardfork.T13).c
	_, err := precompiles.Call(c, precompiles.PathUSDAddress, []byte{1, 2, 3, 4})
	rv, ok := err.(*precompiles.Revert)
	assert.Ok(ok, "unknown selector reverts")
	assert.Equal(string(rv.Data[:4]), string(selectorOf("UnknownFunctionSelector(bytes4)")), "UnknownFunctionSelector")
	_, err = precompiles.Call(c, precompiles.PathUSDAddress, []byte{1})
	rv, ok = err.(*precompiles.Revert)
	assert.Ok(ok, "short input reverts after T1")
	assert.Equal(len(rv.Data), 0, "empty revert")
}

func TestUninitializedToken(t *testing.T) {
	st := memstate.New()
	c := precompiles.NewContext(st, hardfork.T13, 100_000)
	_, err := precompiles.Call(c, precompiles.TIP20Address(77), calldata("totalSupply()"))
	rv, ok := err.(*precompiles.Revert)
	assert.Ok(ok, "uninitialized reverts")
	assert.Equal(string(rv.Data), string(selectorOf("Uninitialized()")), "Uninitialized()")
}
