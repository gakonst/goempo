package precompiles

import (
	"bytes"
	"math"
	"slices"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	dexPathTokensMax = 15_790
	dexWalkMax       = 1 << 20
	dexEmptyBid      = int16(math.MinInt16)
	dexEmptyAsk      = int16(math.MaxInt16)
)

const _ = uint(dexWalkMax - dexPathTokensMax)

const _ = uint(math.MaxInt32 - dexWalkMax)

type rounding uint8

const (
	roundDown rounding = iota
	roundUp
)

func tickPrice(t int16) uint64 { return uint64(uint32(int32(dexPriceScale) + int32(t))) }

type fraction struct {
	num Word
	den Word
}

func divRound(f fraction, r rounding) Word {
	q := f.num.Div(f.den)
	if r == roundUp && !f.num.Mod(f.den).IsZero() {
		q = q.Add(u256.One())
	}
	return q
}

type priced struct {
	amount Word
	tick   int16
	round  rounding
}

func baseToQuote(p priced) (Word, bool) {
	num := p.amount.Mul(u256.FromUint64(tickPrice(p.tick)))
	q := divRound(fraction{num: num, den: u256.FromUint64(dexPriceScale)}, p.round)
	return q, fitsU128(q)
}

func quoteToBase(p priced) (Word, bool) {
	price := tickPrice(p.tick)
	if price == 0 {
		return Word{}, false
	}
	num := p.amount.Mul(u256.FromUint64(dexPriceScale))
	q := divRound(fraction{num: num, den: u256.FromUint64(price)}, p.round)
	return q, fitsU128(q)
}

func takerOutput(fill Word, tick int16, makerBid flag) (Word, bool) {
	if makerBid {
		return baseToQuote(priced{amount: fill, tick: tick, round: roundDown})
	}
	return fill, true
}

func i16Hash(v int16) Hash {
	if v >= 0 {
		return wordOfU64(uint64(v))
	}
	return toHash(Word{uint64(int64(v)), math.MaxUint64, math.MaxUint64, math.MaxUint64})
}

func (v abiValue) i16() int16 { return int16(uint16(v.word[30])<<8 | uint16(v.word[31])) }

func tickOutOfBounds(tick int16) error {
	return revert(abiError(selector("TickOutOfBounds(int16)"), i16Hash(tick)))
}

func belowMinimumOrder(amount Word) error {
	return revert(abiError(selector("BelowMinimumOrderSize(uint128)"), toHash(amount)))
}

func revertSelector(err error) Hash {
	var h Hash
	rv, ok := err.(*Revert)
	if ok && len(rv.Data) >= 4 {
		copy(h[:4], rv.Data[:4])
	}
	return h
}

func isPanicRevert(err error) bool {
	rv, ok := err.(*Revert)
	return ok && len(rv.Data) == 36 && [4]byte(rv.Data[:4]) == selPanic
}

func isSystemError(err error) bool {
	if _, ok := err.(*Revert); ok {
		return isPanicRevert(err)
	}
	return true
}

func (c *Context) isPolicyLookupError(err error) bool {
	rv, ok := err.(*Revert)
	if !ok {
		return false
	}
	if !c.at(hardfork.T2) {
		return bytes.Equal(rv.Data, abiError(selPanic, wordOfU64(panicUnderOverflow)))
	}
	if len(rv.Data) != 4 {
		return false
	}
	sel := [4]byte(rv.Data)
	return sel == selector("InvalidPolicyType()") || sel == selector("PolicyNotFound()")
}

func (c *Context) checkpoint(fn func() error) error {
	if !c.at(hardfork.T1C) {
		return fn()
	}
	snap := c.State.Snapshot()
	err := fn()
	if err != nil {
		c.State.RevertToSnapshot(snap)
	}
	return err
}

func dexToken(token Address) error {
	if !IsTIP20(token) {
		return errSel("InvalidToken()")
	}
	return nil
}

func (c *Context) dexQuoteOf(token Address) (Address, error) {
	if err := dexToken(token); err != nil {
		return Address{}, err
	}
	return TIP20(c, token).quoteToken()
}

type payout struct {
	token  Address
	to     Address
	amount Word
}

func (c *Context) dexTransfer(p payout) error {
	if err := dexToken(p.token); err != nil {
		return err
	}
	return TIP20(c, p.token).transfer(transferReq{from: StablecoinDEXAddress, to: p.to, amount: p.amount})
}

func (c *Context) dexPull(p payout) error {
	if err := dexToken(p.token); err != nil {
		return err
	}
	t := TIP20(c, p.token)
	if c.at(hardfork.T5) {
		return t.systemTransferFrom(systemPull{caller: StablecoinDEXAddress, from: p.to, amount: p.amount})
	}
	dex := StablecoinDEXAddress
	return t.transfer(transferReq{spender: &dex, from: p.to, to: dex, amount: p.amount})
}

type debit struct {
	balanceDelta
	checkPause flag
}

func (c *Context) debitOrPull(d debit) error {
	if err := dexToken(d.token); err != nil {
		return err
	}
	t := TIP20(c, d.token)
	if err := t.ensureTransferAuthorized(hop{from: d.user, to: StablecoinDEXAddress}); err != nil {
		return err
	}
	bal, err := c.dexBalance(d.holding)
	if err != nil {
		return err
	}
	if !d.amount.Gt(bal) {
		if bool(d.checkPause) && c.at(hardfork.T4) {
			if err := t.checkNotPaused(); err != nil {
				return err
			}
		}
		return c.subDexBalance(d.balanceDelta)
	}
	if err := c.dexPull(payout{token: d.token, to: d.user, amount: d.amount.Sub(bal)}); err != nil {
		return err
	}
	return c.setDexBalance(d.holding, Word{})
}

type tokenRole struct {
	token Address
	user  Address
	role  authRole
}

func (c *Context) authorizedForToken(q tokenRole) (bool, error) {
	if err := dexToken(q.token); err != nil {
		return false, err
	}
	policy, err := TIP20(c, q.token).transferPolicy()
	if err != nil {
		return false, err
	}
	ok, err := c.isAuthorizedAs(authQuery{id: policy, user: q.user}, q.role)
	if err != nil && c.isPolicyLookupError(err) {
		return false, nil
	}
	return ok, err
}

func (c *Context) dexEmit(sig string, topics []Hash, data []byte) error {
	return c.emit(StablecoinDEXAddress, append([]Hash{topic(sig)}, topics...), data)
}

func (c *Context) emitOrderPlaced(o dexOrder, token Address) error {
	return c.dexEmit("OrderPlaced(uint128,address,address,uint128,bool,int16,bool,int16)",
		[]Hash{toHash(o.id), addressWord(o.maker), addressWord(token)},
		encodeWords(toHash(o.amount), flag(o.isBid).hash(), i16Hash(o.tick), flag(o.isFlip).hash(), i16Hash(o.flipTick)))
}

type fillEvent struct {
	order   dexOrder
	taker   Address
	amount  Word
	partial flag
}

func (c *Context) emitOrderFilled(e fillEvent) error {
	return c.dexEmit("OrderFilled(uint128,address,address,uint128,bool)",
		[]Hash{toHash(e.order.id), addressWord(e.order.maker), addressWord(e.taker)},
		encodeWords(toHash(e.amount), e.partial.hash()))
}

func (c *Context) createPair(base Address) (Hash, error) {
	ok, err := IsTIP20Deployed(c, base)
	if err != nil {
		return Hash{}, err
	}
	if !ok {
		return Hash{}, errSel("InvalidBaseToken()")
	}
	quote, err := c.dexQuoteOf(base)
	if err != nil {
		return Hash{}, err
	}
	if err := c.validateUSD(base); err != nil {
		return Hash{}, err
	}
	if err := c.validateUSD(quote); err != nil {
		return Hash{}, err
	}
	key := dexBookKey(tokenPair{base: base, quote: quote})
	cur, err := c.readBook(key)
	if err != nil {
		return Hash{}, err
	}
	if cur.initialized() {
		return Hash{}, errSel("PairAlreadyExists()")
	}
	if err := c.initBook(key, tokenPair{base: base, quote: quote}); err != nil {
		return Hash{}, err
	}
	return key, c.dexEmit("PairCreated(bytes32,address,address)", []Hash{key, addressWord(base), addressWord(quote)}, nil)
}

func (c *Context) initBook(key Hash, p tokenPair) error {
	book := dexBook{base: p.base, quote: p.quote, bestBid: dexEmptyBid, bestAsk: dexEmptyAsk}
	if c.at(hardfork.T8) {
		n, err := c.bookKeysLen()
		if err != nil {
			return err
		}
		if n >= math.MaxUint32 {
			return errFatal
		}
		book.bookID = n + 1
	}
	if err := c.writeBook(key, book); err != nil {
		return err
	}
	n, err := c.bookKeysLen()
	if err != nil {
		return err
	}
	if err := c.dexStore(c.bookKeysElem(n), toWord(key)); err != nil {
		return err
	}
	return c.dexStore(slotDexBookKeys, u256.FromUint64(n+1))
}

type escrow struct {
	token  Address
	amount Word
	other  Address
}

type orderSpec struct {
	pair   tokenPair
	amount Word
	isBid  bool
	tick   int16
}

func escrowOf(s orderSpec) (escrow, error) {
	if s.isBid {
		q, ok := baseToQuote(priced{amount: s.amount, tick: s.tick, round: roundUp})
		if !ok {
			return escrow{}, errSel("InsufficientBalance()")
		}
		return escrow{token: s.pair.quote, amount: q, other: s.pair.base}, nil
	}
	return escrow{token: s.pair.base, amount: s.amount, other: s.pair.quote}, nil
}

func checkTick(tick int16) error {
	if tick < dexMinTick || tick > dexMaxTick {
		return tickOutOfBounds(tick)
	}
	if tick%dexTickSpacing != 0 {
		return errSel("InvalidTick()")
	}
	return nil
}

func (c *Context) checkNonEscrow(h holding) error {
	if err := dexToken(h.token); err != nil {
		return err
	}
	t := TIP20(c, h.token)
	if err := t.ensureTransferAuthorized(hop{from: StablecoinDEXAddress, to: h.user}); err != nil {
		return err
	}
	if c.at(hardfork.T4) {
		return t.checkNotPaused()
	}
	return nil
}

type placeReq struct {
	sender Address
	token  Address
	amount Word
	isBid  bool
	tick   int16
}

type bookRef struct {
	key  Hash
	pair tokenPair
}

func (c *Context) bookFor(token Address) (bookRef, error) {
	quote, err := c.dexQuoteOf(token)
	if err != nil {
		return bookRef{}, err
	}
	p := tokenPair{base: token, quote: quote}
	return bookRef{key: dexBookKey(p), pair: p}, nil
}

func (c *Context) ensurePair(key Hash, base Address) error {
	book, err := c.readBook(key)
	if err != nil || book.initialized() {
		return err
	}
	_, err = c.createPair(base)
	return err
}

func (c *Context) incrementNextOrderID() error {
	id, err := c.nextOrderID()
	if err != nil {
		return err
	}
	return c.dexStore(slotDexNextID, id.Add(u256.One()))
}

func (c *Context) place(r placeReq) (Word, error) {
	ref, err := c.bookFor(r.token)
	if err != nil {
		return Word{}, err
	}
	key, p := ref.key, ref.pair
	if err := c.ensurePair(key, r.token); err != nil {
		return Word{}, err
	}
	if err := checkTick(r.tick); err != nil {
		return Word{}, err
	}
	if r.amount.Lt(dexMinOrderAmount) {
		return Word{}, belowMinimumOrder(r.amount)
	}
	e, err := escrowOf(orderSpec{pair: p, amount: r.amount, isBid: r.isBid, tick: r.tick})
	if err != nil {
		return Word{}, err
	}
	if err := c.checkNonEscrow(holding{user: r.sender, token: e.other}); err != nil {
		return Word{}, err
	}
	err = c.debitOrPull(debit{balanceDelta: balanceDelta{holding{r.sender, e.token}, e.amount}, checkPause: true})
	if err != nil {
		return Word{}, err
	}
	id, err := c.nextOrderID()
	if err != nil {
		return Word{}, err
	}
	if err := c.incrementNextOrderID(); err != nil {
		return Word{}, err
	}
	o := dexOrder{id: id, maker: r.sender, bookKey: key, isBid: r.isBid, tick: r.tick, amount: r.amount, remaining: r.amount}
	if err := c.commitOrder(o, true); err != nil {
		return Word{}, err
	}
	return id, c.emitOrderPlaced(o, r.token)
}

type tailLink struct {
	book Hash
	tail Word
	next Word
}

func (c *Context) linkTailBeforeT8(l tailLink) error {
	prev, err := c.readOrder(l.tail, &l.book)
	if err != nil {
		return err
	}
	prev.next = l.next
	return c.writeOrderLegacy(l.tail, prev)
}

func (c *Context) linkIntoLevel(o *dexOrder, book dexBook, l *tickLevel) error {
	ref := levelRef{book: o.bookKey, tick: o.tick, isBid: o.isBid}
	if l.tail.IsZero() {
		l.head, l.tail = o.id, o.id
		if err := c.updateTickBit(ref, bitSet); err != nil {
			return err
		}
		if o.isBid && o.tick > book.bestBid {
			return c.writeBestTick(o.bookKey, flag(true), o.tick)
		}
		if !o.isBid && o.tick < book.bestAsk {
			return c.writeBestTick(o.bookKey, flag(false), o.tick)
		}
		return nil
	}
	var err error
	if c.at(hardfork.T8) {
		err = c.writeOrderField(l.tail, ofNext, o.id)
	} else {
		err = c.linkTailBeforeT8(tailLink{book: o.bookKey, tail: l.tail, next: o.id})
	}
	o.prev = l.tail
	l.tail = o.id
	return err
}

func (c *Context) commitOrder(order dexOrder, charge flag) error {
	o := order
	book, err := c.readBook(o.bookKey)
	if err != nil {
		return err
	}
	idx := book.index()
	ref := levelRef{book: o.bookKey, tick: o.tick, isBid: o.isBid}
	l, err := c.readLevel(ref)
	if err != nil {
		return err
	}
	if err := c.linkIntoLevel(&o, book, &l); err != nil {
		return err
	}
	if !c.at(hardfork.T12) {
		l.liquidity = l.liquidity.Add(o.remaining)
		if !fitsU128(l.liquidity) {
			return underOverflow()
		}
	}
	if err := c.writeLevel(ref, l); err != nil {
		return err
	}
	if charge {
		if c.at(hardfork.T7) {
			return c.writeOrderSpendingCredits(o, idx)
		}
		return c.writeOrder(o, &idx)
	}
	if !c.at(hardfork.T8) {
		return c.writeOrder(o, &idx)
	}
	before, err := c.creditBalance(StablecoinDEXAddress)
	if err != nil {
		return err
	}
	if err := c.writeOrder(o, &idx); err != nil {
		return err
	}
	minted, err := c.mintedSince(StablecoinDEXAddress, before)
	if err != nil {
		return err
	}
	return c.creditDexSlots(o.maker, minted)
}

func (c *Context) writeOrderSpendingCredits(o dexOrder, idx bookIndex) error {
	have, err := c.dexCredits(o.maker)
	if err != nil {
		return err
	}
	if have == 0 {
		return c.writeOrder(o, &idx)
	}
	if err := c.dexStore(c.dexCreditsSlot(o.maker), Word{}); err != nil {
		return err
	}
	b, err := c.budgetBegin(StablecoinDEXAddress, have)
	if err != nil {
		return err
	}
	werr := c.writeOrder(o, &idx)
	d, err := c.budgetEnd(b)
	if err != nil {
		return err
	}
	if werr != nil {
		return werr
	}
	return c.creditDexSlots(o.maker, uint64(sat(have).sub(d.spent)))
}

func (c *Context) creditDexSlots(user Address, slots uint64) error {
	if slots == 0 || !c.at(hardfork.T7) {
		return nil
	}
	cur, err := c.dexCredits(user)
	if err != nil {
		return err
	}
	next := u256.FromUint64(uint64(sat(cur).add(slots)))
	if cur != 0 {
		return c.dexStore(c.dexCreditsSlot(user), next)
	}
	b, err := c.budgetBegin(StablecoinDEXAddress, 1)
	if err != nil {
		return err
	}
	werr := c.dexStore(c.dexCreditsSlot(user), next)
	d, err := c.budgetEnd(b)
	if err != nil {
		return err
	}
	if werr != nil {
		return werr
	}
	if !d.spentOne() {
		return errFatal
	}
	return nil
}

func (c *Context) deleteOrder(id Word) (uint64, error) {
	if !c.at(hardfork.T7) {
		return 0, c.deleteOrderRecord(id)
	}
	before, err := c.creditBalance(StablecoinDEXAddress)
	if err != nil {
		return 0, err
	}
	if err := c.deleteOrderRecord(id); err != nil {
		return 0, err
	}
	return c.mintedSince(StablecoinDEXAddress, before)
}

type creditDeltas map[Address]uint64

func (d creditDeltas) credit(user Address, slots uint64) {
	if slots == 0 {
		return
	}
	d[user] = uint64(sat(d[user]).add(slots))
}

func (c *Context) flushCredits(d creditDeltas) error {
	users := make([]Address, 0, len(d))
	for u := range d {
		users = append(users, u)
	}
	slices.SortFunc(users, func(a, b Address) int { return bytes.Compare(a[:], b[:]) })
	for _, u := range users {
		if err := c.creditDexSlots(u, d[u]); err != nil {
			return err
		}
	}
	return nil
}
