package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

func dexMutate(sig string, args []abiType, fn handler) method {
	return mutate(sig, args, func(c *Context, a []abiValue) ([]byte, error) {
		if err := c.preserveCredits(StablecoinDEXAddress); err != nil {
			return nil, err
		}
		return fn(c, a)
	})
}

func retI16(v int16) []byte { return encodeWords(i16Hash(v)) }

func encodeOrder(o dexOrder) []byte {
	return encodeWords(toHash(o.id), addressWord(o.maker), o.bookKey, flag(o.isBid).hash(), i16Hash(o.tick),
		toHash(o.amount), toHash(o.remaining), toHash(o.prev), toHash(o.next), flag(o.isFlip).hash(), i16Hash(o.flipTick))
}

func swapArgs(c *Context, a []abiValue) swapReq {
	return swapReq{sender: c.Caller, leg: tradeLeg{in: a[0].addr(), out: a[1].addr()}, amount: a[2].u256(), limit: a[3].u256()}
}

var (
	dexSwapArgs  = []abiType{tAddress, tAddress, tU128, tU128}
	dexQuoteArgs = []abiType{tAddress, tAddress, tU128}
)

var dexTable = register(StablecoinDEXAddress, newTable(
	dexMutate("place(address,uint128,bool,int16)", []abiType{tAddress, tU128, tBool, tI16}, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.place(placeReq{sender: c.Caller, token: a[0].addr(), amount: a[1].u256(), isBid: a[2].boolean(), tick: a[3].i16()})
		return retWord(id), err
	}),
	dexMutate("placeFlip(address,uint128,bool,int16,int16)", []abiType{tAddress, tU128, tBool, tI16, tI16}, func(c *Context, a []abiValue) ([]byte, error) {
		r := placeReq{sender: c.Caller, token: a[0].addr(), amount: a[1].u256(), isBid: a[2].boolean(), tick: a[3].i16()}
		id, err := c.placeFlip(flipReq{placeReq: r, flipTick: a[4].i16()})
		return retWord(id), err
	}),
	view("balanceOf(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		b, err := c.dexBalance(holding{a[0].addr(), a[1].addr()})
		return retWord(b), err
	}),
	view("getOrder(uint128)", []abiType{tU128}, func(c *Context, a []abiValue) ([]byte, error) {
		o, err := c.getOrder(a[0].u256())
		if err != nil {
			return nil, err
		}
		return encodeOrder(o), nil
	}),
	view("getTickLevel(address,int16,bool)", []abiType{tAddress, tI16, tBool}, func(c *Context, a []abiValue) ([]byte, error) {
		l, err := c.getPriceLevel(levelQuery{base: a[0].addr(), tick: a[1].i16(), isBid: a[2].boolean()})
		return encodeWords(toHash(l.head), toHash(l.tail), toHash(l.liquidity)), err
	}),
	view("pairKey(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return encodeWords(dexBookKey(tokenPair{base: a[0].addr(), quote: a[1].addr()})), nil
	}),
	view("books(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		b, err := c.readBook(a[0].bytes32())
		return encodeWords(addressWord(b.base), addressWord(b.quote), i16Hash(b.bestBid), i16Hash(b.bestAsk)), err
	}),
	view("nextOrderId()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.nextOrderID()
		return retWord(id), err
	}),
	dexMutate("createPair(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		k, err := c.createPair(a[0].addr())
		return encodeWords(k), err
	}),
	dexMutate("withdraw(address,uint128)", []abiType{tAddress, tU128}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.withdraw(payout{token: a[0].addr(), to: c.Caller, amount: a[1].u256()})
	}),
	dexMutate("cancel(uint128)", []abiType{tU128}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cancel(c.Caller, a[0].u256())
	}),
	dexMutate("cancelStaleOrder(uint128)", []abiType{tU128}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cancelStale(a[0].u256())
	}),
	dexMutate("swapExactAmountIn(address,address,uint128,uint128)", dexSwapArgs, func(c *Context, a []abiValue) ([]byte, error) {
		out, err := c.swapExactIn(swapArgs(c, a))
		return retWord(out), err
	}),
	dexMutate("swapExactAmountOut(address,address,uint128,uint128)", dexSwapArgs, func(c *Context, a []abiValue) ([]byte, error) {
		in, err := c.swapExactOut(swapArgs(c, a))
		return retWord(in), err
	}),
	view("quoteSwapExactAmountIn(address,address,uint128)", dexQuoteArgs, func(c *Context, a []abiValue) ([]byte, error) {
		out, err := c.quoteExactIn(tradeLeg{in: a[0].addr(), out: a[1].addr()}, a[2].u256())
		return retWord(out), err
	}),
	view("quoteSwapExactAmountOut(address,address,uint128)", dexQuoteArgs, func(c *Context, a []abiValue) ([]byte, error) {
		in, err := c.quoteExactOut(tradeLeg{in: a[0].addr(), out: a[1].addr()}, a[2].u256())
		return retWord(in), err
	}),
	view("MIN_TICK()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retI16(dexMinTick), nil }),
	view("MAX_TICK()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retI16(dexMaxTick), nil }),
	view("TICK_SPACING()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retI16(dexTickSpacing), nil }),
	view("PRICE_SCALE()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retU64(dexPriceScale), nil }),
	view("MIN_ORDER_AMOUNT()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retWord(dexMinOrderAmount), nil }),
	view("MIN_PRICE()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retU64(dexMinPrice), nil }),
	view("MAX_PRICE()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retU64(dexMaxPrice), nil }),
	view("tickToPrice(int16)", []abiType{tI16}, func(c *Context, a []abiValue) ([]byte, error) {
		p, err := c.priceOfTick(a[0].i16())
		return retU64(p), err
	}),
	view("priceToTick(uint32)", []abiType{tU32}, func(c *Context, a []abiValue) ([]byte, error) {
		t, err := c.tickOfPrice(a[0].u64())
		return retI16(t), err
	}),
	view("storageCredits(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		n, err := c.dexCredits(a[0].addr())
		return retU64(n), err
	}).from(hardfork.T7),
	view("bookIndexForKey(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		i, err := c.bookKeyIndex(a[0].bytes32())
		return encodeWords(flag(i.ok).hash(), wordOfU64(i.index)), err
	}).from(hardfork.T8),
	view("bookKeyForIndex(uint32)", []abiType{tU32}, func(c *Context, a []abiValue) ([]byte, error) {
		k, err := c.bookKeyForIndex(a[0].u64())
		return encodeWords(k), err
	}).from(hardfork.T8),
	dexMutate("setBookIndex(uint32)", []abiType{tU32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.setBookIndex(a[0].u64())
	}).from(hardfork.T8),
))
