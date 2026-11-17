package evm_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

func TestSstoreClearRefund(t *testing.T) {
	st := map[primitives.Hash]u256.Int{slot(0): u256.One()}
	code := []byte{0x60, 0x00, 0x60, 0x00, 0x55, 0x00}
	r := executeWith(t, program{code: code, gas: 100_000}, st)
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	assert.Equal(r.state.Refund(), int64(4800), "EIP-3529 clear refund")
	assert.Equal(100_000-r.result.GasLeft, uint64(3+3+2100+2900), "cold reset cost")
}

func TestSstoreRestoreRefund(t *testing.T) {
	code := []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x60, 0x00, 0x60, 0x00, 0x55, 0x00}
	r := execute(t, program{code: code, gas: 100_000})
	assert.Equal(r.state.Refund(), int64(20000-100), "set then restore refunds the set cost")
}

func TestSstoreSentry(t *testing.T) {
	code := []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x00}
	r := execute(t, program{code: code, gas: 2306})
	assert.Equal(r.result.Status, evm.StatusOutOfGas, "EIP-2200 stipend sentry")
}

func TestTransientStorageRevertsWithFrame(t *testing.T) {
	callee := []byte{0x60, 0x05, 0x60, 0x00, 0x5d, 0x60, 0x00, 0x60, 0x00, 0xfd}
	caller := []byte{
		0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00,
		0x73, 0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0x61, 0xff, 0xff, 0xf1, 0x50,
		0x60, 0x00, 0x5c, 0x60, 0x00, 0x55,
		0x60, 0x07, 0x60, 0x01, 0x5d, 0x60, 0x01, 0x5c, 0x60, 0x01, 0x55, 0x00,
	}
	r := execute(t, program{code: caller, gas: 300_000, extra: []deployed{{addr: other, code: callee}}})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	assert.Ok(r.state.Storage(contract, slot(0)).IsZero(), "reverted TSTORE is gone")
	assert.Equal(r.state.Storage(contract, slot(1)), u256.FromUint64(7), "TLOAD sees own TSTORE")
}

func TestMcopyOverlap(t *testing.T) {
	code := []byte{
		0x7f, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
		0x60, 0x00, 0x52,
		0x60, 0x20, 0x60, 0x00, 0x60, 0x01, 0x5e,
		0x60, 0x00, 0x51, 0x60, 0x00, 0x55, 0x00,
	}
	r := execute(t, program{code: code, gas: 100_000})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	want := [32]byte{1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}
	assert.Equal(r.state.Storage(contract, slot(0)), u256.FromBytes32(want), "memmove semantics")
}

func TestStaticCallBlocksSstore(t *testing.T) {
	callee := []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x00}
	caller := []byte{
		0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00,
		0x73, 0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0x61, 0xff, 0xff, 0xfa,
		0x60, 0x00, 0x55, 0x00,
	}
	r := execute(t, program{code: caller, gas: 300_000, extra: []deployed{{addr: other, code: callee}}})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	assert.Ok(r.state.Storage(contract, slot(0)).IsZero(), "STATICCALL returned 0")
	assert.Ok(r.state.Storage(other, slot(0)).IsZero(), "callee wrote nothing")
}

func TestCallForwardsAllButOne64th(t *testing.T) {
	callee := []byte{0x5a, 0x60, 0x00, 0x55, 0x00}
	caller := []byte{
		0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00,
		0x73, 0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xf1, 0x00,
	}
	gas := uint64(1_000_000)
	r := execute(t, program{code: caller, gas: gas, extra: []deployed{{addr: other, code: callee}}})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	before := gas - 3*7 - 2600
	forwarded := before - before/64
	assert.Equal(r.state.Storage(other, slot(0)), u256.FromUint64(forwarded-2), "GAS in callee sees 63/64")
}

func TestCreate2Address(t *testing.T) {
	code := []byte{
		0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0xf5,
		0x60, 0x00, 0x55, 0x00,
	}
	r := execute(t, program{code: code, gas: 200_000})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	got := r.state.Storage(contract, slot(0)).Bytes32()
	want := evm.Create2Address(contract, evm.Create2Input{CodeHash: keccak.Sum256(nil)})
	assert.Equal(primitives.Address(got[12:]), want, "CREATE2 pushes the EIP-1014 address")
	assert.Equal(r.state.Nonce(want), uint64(1), "created account has nonce 1")
}

func TestCreate2Vectors(t *testing.T) {
	cases := []struct {
		name   string
		caller string
		salt   uint64
		code   []byte
		want   string
	}{
		{name: "example 0", caller: "0x0000000000000000000000000000000000000000", code: []byte{0x00}, want: "0x4d1a2e2bb4f88f0250f26ffff098b0b30b26bf38"},
		{name: "example 1", caller: "0xdeadbeef00000000000000000000000000000000", code: []byte{0x00}, want: "0xb928f69bb1d91cd65274e3c79d8986362984fda3"},
		{name: "example 4", caller: "0x00000000000000000000000000000000deadbeef", salt: 0xcafebabe, code: []byte{0xde, 0xad, 0xbe, 0xef}, want: "0x60f3f640a8508fc6a86d45df051962668e1e8ac7"},
		{name: "example 6", caller: "0x0000000000000000000000000000000000000000", code: nil, want: "0xe33c0c7f7df4809055c3eba6c09cfe4baf1bd9e0"},
	}
	for i := 0; i < len(cases); i++ {
		caller, err := primitives.HexToAddress(cases[i].caller)
		assert.NoError(err, cases[i].name)
		in := evm.Create2Input{Salt: u256.FromUint64(cases[i].salt).Bytes32(), CodeHash: keccak.Sum256(cases[i].code)}
		assert.Equal(evm.Create2Address(caller, in).Hex(), cases[i].want, cases[i].name)
	}
}
