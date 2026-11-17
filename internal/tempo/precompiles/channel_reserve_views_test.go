package precompiles_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	mainnetChannelID   = "c83cb6a7281713fa16a5aed5015cc429e545af12d9c60cec6b284342b7a89aa9"
	mainnetDomainSep   = "6465ffa0b0c1e7cfc1a894ba54d79615bc06fd7cc9cfec558f3df82189d17367"
	mainnetVoucherTH   = "4730b4b2fe8de522475e80e010877c5b58ad10eb07fd0436615ee123f743bbe8"
	mainnetDigest1000  = "959b92b3995a03938c931e542339458938145b968d141f270f484f7d109aacdb"
	mainnetChannelDesc = "000000000000000000000000ca03b1c5ab76ff652c9d9a045593334513124b12" +
		"00000000000000000000000045a4a9612d4d4e78703a63dc3ef36267a6c17d96" +
		"0000000000000000000000000000000000000000000000000000000000000000" +
		"00000000000000000000000020c000000000000000000000b9537d11c60e8b50" +
		"c8026774239aa3b7ea3f210cfabf186856d4b04539d1cdc7450d51d27f2150aa" +
		"000000000000000000000000ca03b1c5ab76ff652c9d9a045593334513124b12" +
		"880797be4cd8dee33b000f57942e694fc56115c826635ab1d45c2eed0b99be6d"
)

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "hex")
	return b
}

func reserveCtx(fork hardfork.Fork) *precompiles.Context {
	st := memstate.New()
	st.SetCode(precompiles.ChannelReserveAddress, []byte{0xef})
	var key [64]byte
	copy(key[:32], unhex(mainnetChannelID))
	st.SetStorage(precompiles.ChannelReserveAddress, keccak.Sum256(key[:]), u256.FromUint64(10000).Lsh(96))
	st.FinalizeTx()
	c := precompiles.NewContext(st, fork, 1_000_000)
	c.ChainID = u256.FromUint64(4217)
	c.Static = true
	return c
}

func reserveView(t *testing.T, c *precompiles.Context, data []byte) []byte {
	out, err := precompiles.Call(c, precompiles.ChannelReserveAddress, data)
	assert.NoError(err, "view")
	return out
}

func TestChannelReserveViewsMatchMainnet(t *testing.T) {
	c := reserveCtx(hardfork.T13)
	assert.Ok(bytes.Equal(reserveView(t, c, selectorOf("domainSeparator()")), unhex(mainnetDomainSep)), "domain separator")
	assert.Ok(bytes.Equal(reserveView(t, c, selectorOf("VOUCHER_TYPEHASH()")), unhex(mainnetVoucherTH)), "typehash")
	assert.Ok(bytes.Equal(reserveView(t, c, selectorOf("CLOSE_GRACE_PERIOD()")), amount(900)), "grace")
	digest := reserveView(t, c, calldata("getVoucherDigest(bytes32,uint96)", unhex(mainnetChannelID), amount(1000)))
	assert.Ok(bytes.Equal(digest, unhex(mainnetDigest1000)), "voucher digest")
	id := reserveView(t, c, calldata("computeChannelId(address,address,address,address,bytes32,address,bytes32)", unhex(mainnetChannelDesc)))
	assert.Ok(bytes.Equal(id, unhex(mainnetChannelID)), "channel id")
	state := append(append(amount(0), amount(10000)...), amount(0)...)
	got := reserveView(t, c, calldata("getChannelState(bytes32)", unhex(mainnetChannelID)))
	assert.Ok(bytes.Equal(got, state), "channel state")
	full := reserveView(t, c, calldata("getChannel((address,address,address,address,bytes32,address,bytes32))", unhex(mainnetChannelDesc)))
	assert.Ok(bytes.Equal(full, append(unhex(mainnetChannelDesc), state...)), "getChannel")
	batch := reserveView(t, c, calldata("getChannelStatesBatch(bytes32[])", amount(32), amount(2), unhex(mainnetChannelID), amount(1)))
	want := append(append(append(amount(32), amount(2)...), state...), make([]byte, 96)...)
	assert.Ok(bytes.Equal(batch, want), "batch")
}

func TestChannelReserveForkGates(t *testing.T) {
	_, err := precompiles.Call(reserveCtx(hardfork.T4), precompiles.ChannelReserveAddress, selectorOf("domainSeparator()"))
	assert.Ok(err != nil, "inactive before T5")
	_, err = precompiles.Call(reserveCtx(hardfork.T6), precompiles.ChannelReserveAddress, calldata("storageCredits(address)", word(alice)))
	assert.Ok(err != nil, "storageCredits view arrives with T7")
	out := reserveView(t, reserveCtx(hardfork.T7), calldata("storageCredits(address)", word(alice)))
	assert.Ok(bytes.Equal(out, amount(0)), "no credits")
}

func TestReceiptGuardShortReceipt(t *testing.T) {
	st := memstate.New()
	st.SetCode(precompiles.ReceivePolicyGuardAddress, []byte{0xef})
	st.FinalizeTx()
	c := precompiles.NewContext(st, hardfork.T6, 1_000_000)
	_, err := precompiles.Call(c, precompiles.ReceivePolicyGuardAddress, calldata("balanceOf(bytes)", amount(32), amount(1), make([]byte, 32)))
	data, ok := revertData(err)
	assert.Ok(ok, "short receipt reverts")
	assert.Ok(bytes.Equal(data, selectorOf("InvalidReceipt()")), "InvalidReceipt")
	receipt := make([]byte, 320)
	out, err := precompiles.Call(c, precompiles.ReceivePolicyGuardAddress, calldata("balanceOf(bytes)", amount(32), amount(320), receipt))
	assert.NoError(err, "zero receipt balance")
	assert.Ok(bytes.Equal(out, amount(0)), "empty")
}
