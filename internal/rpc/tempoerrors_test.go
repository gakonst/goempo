package rpc_test

import (
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/kapetan-io/tiger/assert"
)

func selector(sig string) []byte {
	sum := keccak.Sum256([]byte(sig))
	return sum[:4]
}

func word(last ...byte) []byte {
	w := make([]byte, 32)
	copy(w[32-len(last):], last)
	return w
}

func TestRevertErrorRendersTIP20Error(t *testing.T) {
	data, err := hex.DecodeString("832f98b50000000000000000000000000000000000000000000000000000b5e620f47e3cffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff00000000000000000000000020c00000000000000000000016c6514b53947fdc")
	assert.NoError(err, "fixture hex")
	e := rpc.RevertError(data)
	want := "execution reverted: TIP20 token error: InsufficientBalance(InsufficientBalance { available: 199999999999548, required: 115792089237316195423570985008687907853269984665640564039457584007913129639935, token: 0x20c00000000000000000000016c6514b53947fdc })"
	assert.Equal(e.Message, want, "message as the reference renders it (mainnet eth_call at 0xe8d)")
}

func TestRevertErrorRendersUnitAndSignedErrors(t *testing.T) {
	e := rpc.RevertError(selector("ContractPaused()"))
	assert.Equal(e.Message, "execution reverted: TIP20 token error: ContractPaused(ContractPaused)", "unit error")
	neg := word(0xff, 0xff)
	for i := range 30 {
		neg[i] = 0xff
	}
	neg[30], neg[31] = 0xff, 0x9c
	e = rpc.RevertError(append(selector("TickOutOfBounds(int16)"), neg...))
	assert.Equal(e.Message, "execution reverted: Stablecoin DEX error: TickOutOfBounds(TickOutOfBounds { tick: -100 })", "negative tick")
	dirty := append(selector("TickOutOfBounds(int16)"), word(0x01, 0x00, 0x00)...)
	e = rpc.RevertError(dirty)
	assert.Equal(e.Message, "execution reverted", "an out of range value is not decoded")
}

func TestRevertErrorRendersStrings(t *testing.T) {
	body := append(word(32), word(5)...)
	body = append(body, append([]byte("a\"b\nc"), make([]byte, 27)...)...)
	e := rpc.RevertError(append(selector("IngressAlreadyExists(string)"), body...))
	assert.Equal(e.Message, "execution reverted: Validator config v2 error: IngressAlreadyExists(IngressAlreadyExists { ingress: \"a\\\"b\\nc\" })", "debug-quoted string")
}
