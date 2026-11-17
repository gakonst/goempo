package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type dexHop struct {
	book         Hash
	baseForQuote bool
}

type tradeLeg struct {
	in  Address
	out Address
}

func (c *Context) pathToRoot(start Address) ([]Address, error) {
	path := []Address{start}
	tok := start
	for range dexPathTokensMax {
		if tok == PathUSDAddress {
			return path, nil
		}
		q, err := c.dexQuoteOf(tok)
		if err != nil {
			return nil, err
		}
		tok = q
		path = append(path, tok)
	}
	return nil, errFatal
}

func (c *Context) tradePath(leg tradeLeg) ([]dexHop, error) {
	if leg.in == leg.out {
		return nil, errSel("IdenticalTokens()")
	}
	if !IsTIP20(leg.in) || !IsTIP20(leg.out) {
		return nil, errSel("InvalidToken()")
	}
	inQ, err := c.dexQuoteOf(leg.in)
	if err != nil {
		return nil, err
	}
	outQ, err := c.dexQuoteOf(leg.out)
	if err != nil {
		return nil, err
	}
	if inQ == leg.out || outQ == leg.in {
		return c.buildRoute([]Address{leg.in, leg.out})
	}
	pin, err := c.pathToRoot(leg.in)
	if err != nil {
		return nil, err
	}
	pout, err := c.pathToRoot(leg.out)
	if err != nil {
		return nil, err
	}
	lcaIn, lcaOut := -1, -1
	for i, t := range pin {
		if j := indexOf(pout, t); j >= 0 {
			lcaIn, lcaOut = i, j
			break
		}
	}
	if lcaIn < 0 {
		return nil, errSel("PairDoesNotExist()")
	}
	trade := append([]Address{}, pin[:lcaIn+1]...)
	for i := lcaOut - 1; i >= 0; i-- {
		trade = append(trade, pout[i])
	}
	return c.buildRoute(trade)
}

func indexOf(xs []Address, a Address) int {
	for i, x := range xs {
		if x == a {
			return i
		}
	}
	return -1
}

func (c *Context) routeHop(leg tradeLeg) (dexHop, error) {
	if err := dexToken(leg.in); err != nil {
		return dexHop{}, err
	}
	t := TIP20(c, leg.in)
	if c.at(hardfork.T3) {
		if err := t.checkNotPaused(); err != nil {
			return dexHop{}, err
		}
	}
	q, err := t.quoteToken()
	if err != nil {
		return dexHop{}, err
	}
	p := tokenPair{base: leg.in, quote: leg.out}
	if q != leg.out {
		oq, err := c.dexQuoteOf(leg.out)
		if err != nil {
			return dexHop{}, err
		}
		if oq != leg.in {
			return dexHop{}, errSel("PairDoesNotExist()")
		}
		p = tokenPair{base: leg.out, quote: leg.in}
	}
	key := dexBookKey(p)
	book, err := c.readBook(key)
	if err != nil {
		return dexHop{}, err
	}
	if book.base == (Address{}) {
		return dexHop{}, errSel("PairDoesNotExist()")
	}
	return dexHop{book: key, baseForQuote: leg.in == p.base}, nil
}

func (c *Context) buildRoute(path []Address) ([]dexHop, error) {
	if len(path) < 2 {
		return nil, nil
	}
	route := make([]dexHop, 0, len(path)-1)
	for i := range len(path) - 1 {
		h, err := c.routeHop(tradeLeg{in: path[i], out: path[i+1]})
		if err != nil {
			return nil, err
		}
		route = append(route, h)
	}
	return route, nil
}

type swapReq struct {
	sender Address
	leg    tradeLeg
	amount Word
	limit  Word
}

func (c *Context) swapExactIn(r swapReq) (Word, error) {
	route, err := c.tradePath(r.leg)
	if err != nil {
		return Word{}, err
	}
	err = c.debitOrPull(debit{balanceDelta: balanceDelta{holding{r.sender, r.leg.in}, r.amount}})
	if err != nil {
		return Word{}, err
	}
	amount, d := r.amount, creditDeltas{}
	for _, h := range route {
		amount, err = c.walkBook(walkReq{mode: walkSettle, kind: exactIn, deltas: d, amount: amount, isBid: h.baseForQuote}, h.book, r.sender)
		if err != nil {
			return Word{}, err
		}
	}
	if amount.Lt(r.limit) {
		return Word{}, errSel("InsufficientOutput()")
	}
	if err := c.dexTransfer(payout{token: r.leg.out, to: r.sender, amount: amount}); err != nil {
		return Word{}, err
	}
	return amount, c.flushCredits(d)
}

func (c *Context) swapExactOut(r swapReq) (Word, error) {
	route, err := c.tradePath(r.leg)
	if err != nil {
		return Word{}, err
	}
	amount, d := r.amount, creditDeltas{}
	for i := len(route) - 1; i >= 0; i-- {
		h := route[i]
		amount, err = c.walkBook(walkReq{mode: walkSettle, kind: exactOut, deltas: d, amount: amount, isBid: h.baseForQuote}, h.book, r.sender)
		if err != nil {
			return Word{}, err
		}
	}
	if amount.Gt(r.limit) {
		return Word{}, errSel("MaxInputExceeded()")
	}
	err = c.debitOrPull(debit{balanceDelta: balanceDelta{holding{r.sender, r.leg.in}, amount}})
	if err != nil {
		return Word{}, err
	}
	if err := c.dexTransfer(payout{token: r.leg.out, to: r.sender, amount: r.amount}); err != nil {
		return Word{}, err
	}
	return amount, c.flushCredits(d)
}

func (c *Context) quoteStartTick(book Hash, isBid flag) (int16, error) {
	b, err := c.readBook(book)
	if err != nil {
		return 0, err
	}
	tick := b.bestAsk
	if isBid {
		tick = b.bestBid
	}
	if tick == dexEmptyBid || tick == dexEmptyAsk {
		return 0, errSel("InsufficientLiquidity()")
	}
	return tick, nil
}

type tickFill struct {
	fill     Word
	in       Word
	out      Word
	consumed Word
}

type tickDemand struct {
	remaining Word
	liquidity Word
	tick      int16
	isBid     bool
}

func tickFillOut(d tickDemand) (tickFill, bool) {
	remaining, liq, tick := d.remaining, d.liquidity, d.tick
	if d.isBid {
		need, ok := quoteToBase(priced{amount: remaining, tick: tick, round: roundUp})
		if !ok {
			return tickFill{}, false
		}
		fill := num(need).min(liq)
		out, ok := baseToQuote(priced{amount: fill, tick: tick, round: roundDown})
		return tickFill{fill: fill, in: fill, out: num(out).min(remaining)}, ok
	}
	fill := num(remaining).min(liq)
	in, ok := baseToQuote(priced{amount: fill, tick: tick, round: roundUp})
	return tickFill{fill: fill, in: in, out: fill}, ok
}

func tickFillIn(d tickDemand) (tickFill, bool) {
	remaining, liq, tick := d.remaining, d.liquidity, d.tick
	if d.isBid {
		fill := num(remaining).min(liq)
		out, ok := baseToQuote(priced{amount: fill, tick: tick, round: roundDown})
		return tickFill{fill: fill, out: out, consumed: fill}, ok
	}
	get, ok := quoteToBase(priced{amount: remaining, tick: tick, round: roundDown})
	if !ok {
		return tickFill{}, false
	}
	fill := num(get).min(liq)
	consumed, ok := baseToQuote(priced{amount: fill, tick: tick, round: roundUp})
	return tickFill{fill: fill, out: fill, consumed: consumed}, ok
}

type tickQuote struct {
	kind   stepKind
	book   Hash
	amount Word
	isBid  bool
}

func (c *Context) quotePerTick(q tickQuote) (Word, error) {
	remaining, total := q.amount, Word{}
	tick, err := c.quoteStartTick(q.book, flag(q.isBid))
	if err != nil {
		return Word{}, err
	}
	for steps := 0; steps < dexWalkMax && !remaining.IsZero(); steps++ {
		ref := levelRef{book: q.book, tick: tick, isBid: q.isBid}
		l, err := c.readLevel(ref)
		if err != nil {
			return Word{}, err
		}
		if l.liquidity.IsZero() {
			next, err := c.nextInitializedTick(ref)
			if err != nil {
				return Word{}, err
			}
			if !next.initialized {
				return Word{}, errSel("InsufficientLiquidity()")
			}
			tick = next.tick
			continue
		}
		var f tickFill
		var ok bool
		if q.kind == exactOut {
			f, ok = tickFillOut(tickDemand{remaining: remaining, liquidity: l.liquidity, tick: tick, isBid: q.isBid})
			remaining, total = num(remaining).satSub(f.out), total.Add(f.in)
		} else {
			f, ok = tickFillIn(tickDemand{remaining: remaining, liquidity: l.liquidity, tick: tick, isBid: q.isBid})
			if ok && f.consumed.Gt(remaining) {
				ok = false
			}
			if ok {
				remaining, total = remaining.Sub(f.consumed), total.Add(f.out)
			}
		}
		if !ok || !fitsU128(total) {
			return Word{}, underOverflow()
		}
		if !f.fill.Eq(l.liquidity) {
			return total, nil
		}
		next, err := c.nextInitializedTick(ref)
		if err != nil {
			return Word{}, err
		}
		if !next.initialized && !remaining.IsZero() {
			return Word{}, errSel("InsufficientLiquidity()")
		}
		tick = next.tick
	}
	if !remaining.IsZero() {
		return Word{}, errFatal
	}
	return total, nil
}

func (c *Context) quoteHop(kind stepKind, h dexHop, amount Word) (Word, error) {
	if c.at(hardfork.T12) {
		return c.walkBook(walkReq{mode: walkQuote, kind: kind, amount: amount, isBid: h.baseForQuote}, h.book, StablecoinDEXAddress)
	}
	return c.quotePerTick(tickQuote{kind: kind, book: h.book, amount: amount, isBid: h.baseForQuote})
}

func (c *Context) quoteExactIn(leg tradeLeg, amount Word) (Word, error) {
	route, err := c.tradePath(leg)
	if err != nil {
		return Word{}, err
	}
	for _, h := range route {
		if amount, err = c.quoteHop(exactIn, h, amount); err != nil {
			return Word{}, err
		}
	}
	return amount, nil
}

func (c *Context) quoteExactOut(leg tradeLeg, amount Word) (Word, error) {
	route, err := c.tradePath(leg)
	if err != nil {
		return Word{}, err
	}
	for i := len(route) - 1; i >= 0; i-- {
		if amount, err = c.quoteHop(exactOut, route[i], amount); err != nil {
			return Word{}, err
		}
	}
	return amount, nil
}

func (c *Context) priceOfTick(tick int16) (uint64, error) {
	if c.at(hardfork.T2) && tick%dexTickSpacing != 0 {
		return 0, errSel("InvalidTick()")
	}
	return tickPrice(tick), nil
}

func (c *Context) tickOfPrice(price uint64) (int16, error) {
	tick := int16(int32(uint32(price)) - int32(dexPriceScale))
	if price < dexMinPrice || price > dexMaxPrice {
		return 0, tickOutOfBounds(tick)
	}
	if c.at(hardfork.T2) && tick%dexTickSpacing != 0 {
		return 0, errSel("InvalidTick()")
	}
	return tick, nil
}

func (c *Context) setBookIndex(index uint64) error {
	key, err := c.bookKeyForIndex(index)
	if err != nil {
		return err
	}
	cur, err := c.bookKeyIndex(key)
	if err != nil {
		return err
	}
	if cur.ok {
		if cur.index == index {
			return nil
		}
		return errSel("IndexAlreadySet()")
	}
	return c.dexStoreField(c.bookSlot(key).plus(4), fDexBookID, u256.FromUint64(index+1))
}
