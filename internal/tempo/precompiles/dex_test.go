package precompiles_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

func dexView(fork hardfork.Fork, data []byte) ([]byte, error) {
	c := precompiles.NewContext(memstate.New(), fork, 1_000_000)
	return precompiles.Call(c, precompiles.StablecoinDEXAddress, data)
}

func int16Arg(v int16) []byte {
	w := u256.FromUint64(uint64(uint16(v)))
	if v < 0 {
		w = u256.Max().Sub(u256.FromUint64(uint64(-int32(v)) - 1))
	}
	b := w.Bytes32()
	return b[:]
}

func dexRevert(err error) string {
	rv, ok := err.(*precompiles.Revert)
	assert.Ok(ok, "reverts with data")
	return string(rv.Data)
}

func TestDEXConstants(t *testing.T) {
	cases := []struct {
		name string
		want []byte
	}{
		{"MIN_TICK()", int16Arg(-2000)},
		{"MAX_TICK()", int16Arg(2000)},
		{"TICK_SPACING()", int16Arg(10)},
		{"PRICE_SCALE()", amount(100_000)},
		{"MIN_PRICE()", amount(98_000)},
		{"MAX_PRICE()", amount(102_000)},
		{"MIN_ORDER_AMOUNT()", amount(100_000_000)},
		{"nextOrderId()", amount(1)},
	}
	for _, tc := range cases {
		out, err := dexView(hardfork.T13, calldata(tc.name))
		assert.NoError(err, tc.name)
		assert.Equal(string(out), string(tc.want), tc.name)
	}
}

func TestDEXTickPrice(t *testing.T) {
	out, err := dexView(hardfork.T13, calldata("tickToPrice(int16)", int16Arg(-2000)))
	assert.NoError(err, "tickToPrice min")
	assert.Equal(string(out), string(amount(98_000)), "price at min tick")
	out, err = dexView(hardfork.T13, calldata("priceToTick(uint32)", amount(100_010)))
	assert.NoError(err, "priceToTick")
	assert.Equal(string(out), string(int16Arg(10)), "tick of 100010")
	_, err = dexView(hardfork.T13, calldata("tickToPrice(int16)", int16Arg(5)))
	assert.Equal(dexRevert(err), string(selectorOf("InvalidTick()")), "off-spacing tick after T2")
	out, err = dexView(hardfork.T1, calldata("tickToPrice(int16)", int16Arg(5)))
	assert.NoError(err, "off-spacing tick allowed before T2")
	assert.Equal(string(out), string(amount(100_005)), "price before T2")
	_, err = dexView(hardfork.T13, calldata("priceToTick(uint32)", amount(97_999)))
	want := calldata("TickOutOfBounds(int16)", int16Arg(-2001))
	assert.Equal(dexRevert(err), string(want), "price below range")
}

func TestDEXPairKeyAndEmptyBook(t *testing.T) {
	base := precompiles.TIP20Address(5)
	out, err := dexView(hardfork.T13, calldata("pairKey(address,address)", word(base), word(precompiles.PathUSDAddress)))
	assert.NoError(err, "pairKey")
	want := keccak.Sum256(append(append([]byte{}, base[:]...), precompiles.PathUSDAddress[:]...))
	assert.Equal(string(out), string(want[:]), "keccak(base ++ quote)")
	_, err = dexView(hardfork.T13, calldata("getOrder(uint128)", amount(1)))
	assert.Equal(dexRevert(err), string(selectorOf("OrderDoesNotExist()")), "missing order")
	_, err = dexView(hardfork.T13, calldata("bookKeyForIndex(uint32)", amount(0)))
	assert.Equal(dexRevert(err), string(selectorOf("PairDoesNotExist()")), "no books")
	_, err = dexView(hardfork.T7, calldata("bookKeyForIndex(uint32)", amount(0)))
	assert.Equal(dexRevert(err)[:4], string(selectorOf("UnknownFunctionSelector(bytes4)")), "book index views start at T8")
}

func TestDEXStaticCallRejectsMutation(t *testing.T) {
	c := precompiles.NewContext(memstate.New(), hardfork.T11, 1_000_000)
	c.Static = true
	_, err := precompiles.Call(c, precompiles.StablecoinDEXAddress, calldata("cancel(uint128)", amount(1)))
	assert.Equal(dexRevert(err), string(selectorOf("StaticCallNotAllowed()")), "static cancel before T12")
	c = precompiles.NewContext(memstate.New(), hardfork.T13, 1_000_000)
	c.Static = true
	_, err = precompiles.Call(c, precompiles.StablecoinDEXAddress, calldata("cancel(uint128)", amount(1)))
	_, isRevert := err.(*precompiles.Revert)
	assert.Ok(err != nil, "static cancel fails from T12")
	assert.Ok(!isRevert, "static cancel halts rather than reverts from T12")
}
