package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type flipReq struct {
	placeReq
	flipTick     int16
	internalOnly bool
}

func (c *Context) placeFlip(r flipReq) (Word, error) {
	ref, err := c.bookFor(r.token)
	if err != nil {
		return Word{}, err
	}
	var id Word
	err = c.checkpoint(func() error {
		var ierr error
		id, ierr = c.placeFlipInner(r, ref)
		return ierr
	})
	return id, err
}

func (c *Context) checkFlipTick(r flipReq) error {
	if err := checkTick(r.tick); err != nil {
		return err
	}
	if r.flipTick < dexMinTick || r.flipTick > dexMaxTick {
		return tickOutOfBounds(r.flipTick)
	}
	if r.flipTick%dexTickSpacing != 0 {
		return errSel("InvalidFlipTick()")
	}
	if r.flipTick == r.tick {
		if !c.at(hardfork.T5) {
			return errSel("InvalidFlipTick()")
		}
	}
	if wrongSide(r) {
		return errSel("InvalidFlipTick()")
	}
	if r.amount.Lt(dexMinOrderAmount) {
		return belowMinimumOrder(r.amount)
	}
	return nil
}

func wrongSide(r flipReq) bool {
	if r.isBid {
		return r.flipTick < r.tick
	}
	return r.flipTick > r.tick
}

func (c *Context) debitInternal(sender Address, e escrow) error {
	if err := dexToken(e.token); err != nil {
		return err
	}
	t := TIP20(c, e.token)
	if err := t.ensureTransferAuthorized(hop{from: sender, to: StablecoinDEXAddress}); err != nil {
		return err
	}
	if c.at(hardfork.T4) {
		if err := t.checkNotPaused(); err != nil {
			return err
		}
	}
	h := holding{sender, e.token}
	bal, err := c.dexBalance(h)
	if err != nil {
		return err
	}
	if bal.Lt(e.amount) {
		return errSel("InsufficientBalance()")
	}
	return c.subDexBalance(balanceDelta{h, e.amount})
}

func (c *Context) placeFlipInner(r flipReq, ref bookRef) (Word, error) {
	key, p := ref.key, ref.pair
	if err := c.ensurePair(key, r.token); err != nil {
		return Word{}, err
	}
	if err := c.checkFlipTick(r); err != nil {
		return Word{}, err
	}
	e, err := escrowOf(orderSpec{pair: p, amount: r.amount, isBid: r.isBid, tick: r.tick})
	if err != nil {
		return Word{}, err
	}
	if err := c.checkNonEscrow(holding{user: r.sender, token: e.other}); err != nil {
		return Word{}, err
	}
	if r.internalOnly {
		err = c.debitInternal(r.sender, e)
	} else {
		err = c.debitOrPull(debit{balanceDelta: balanceDelta{holding{r.sender, e.token}, e.amount}, checkPause: true})
	}
	if err != nil {
		return Word{}, err
	}
	id, err := c.nextOrderID()
	if err != nil {
		return Word{}, err
	}
	if c.at(hardfork.T1C) {
		err = c.dexStore(slotDexNextID, id.Add(u256.One()))
	} else {
		err = c.incrementNextOrderID()
	}
	if err != nil {
		return Word{}, err
	}
	o := dexOrder{id: id, maker: r.sender, bookKey: key, isBid: r.isBid, tick: r.tick, amount: r.amount,
		remaining: r.amount, isFlip: true, flipTick: r.flipTick}
	if err := c.commitOrder(o, true); err != nil {
		return Word{}, err
	}
	return id, c.emitOrderPlaced(o, r.token)
}

func (c *Context) flipInPlace(o dexOrder, p tokenPair) error {
	return c.checkpoint(func() error { return c.flipInPlaceInner(o, p) })
}

func (c *Context) checkLeg(token Address, h hop) error {
	if err := dexToken(token); err != nil {
		return err
	}
	t := TIP20(c, token)
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	return t.ensureTransferAuthorized(h)
}

func (c *Context) flipInPlaceInner(o dexOrder, p tokenPair) error {
	f := o.flipped()
	e, err := escrowOf(orderSpec{pair: p, amount: f.amount, isBid: f.isBid, tick: f.tick})
	if err != nil {
		return err
	}
	h := holding{f.maker, e.token}
	bal, err := c.dexBalance(h)
	if err != nil {
		return err
	}
	if bal.Lt(e.amount) {
		return errSel("InsufficientBalance()")
	}
	if err := c.checkLeg(e.token, hop{from: f.maker, to: StablecoinDEXAddress}); err != nil {
		return err
	}
	if err := c.checkLeg(e.other, hop{from: StablecoinDEXAddress, to: f.maker}); err != nil {
		return err
	}
	if err := c.subDexBalance(balanceDelta{h, e.amount}); err != nil {
		return err
	}
	if err := c.commitOrder(f, false); err != nil {
		return err
	}
	return c.dexEmit("OrderFlipped(uint128,address,address,uint128,bool,int16,int16)",
		[]Hash{toHash(f.id), addressWord(f.maker), addressWord(p.base)},
		encodeWords(toHash(f.amount), flag(f.isBid).hash(), i16Hash(f.tick), i16Hash(f.flipTick)))
}

func (c *Context) makerSettlement(book dexBook, o dexOrder, fill Word) error {
	if o.isBid {
		return c.incDexBalance(balanceDelta{holding{o.maker, book.base}, fill})
	}
	q, ok := baseToQuote(priced{amount: fill, tick: o.tick, round: roundUp})
	if !ok {
		return underOverflow()
	}
	return c.incDexBalance(balanceDelta{holding{o.maker, book.quote}, q})
}

type cursor struct {
	book  Hash
	taker Address
	level tickLevel
	order dexOrder
}

func (cur *cursor) ref() levelRef {
	return levelRef{book: cur.order.bookKey, tick: cur.order.tick, isBid: cur.order.isBid}
}

func (c *Context) subLiquidity(l *tickLevel, amount Word) error {
	if c.at(hardfork.T12) {
		return nil
	}
	if amount.Gt(l.liquidity) {
		return underOverflow()
	}
	l.liquidity = l.liquidity.Sub(amount)
	return nil
}

func (c *Context) partialFill(cur *cursor, fill Word) error {
	o := &cur.order
	book, err := c.readBook(o.bookKey)
	if err != nil {
		return err
	}
	o.remaining = o.remaining.Sub(fill)
	if err := c.writeOrderField(o.id, ofRemaining, o.remaining); err != nil {
		return err
	}
	if err := c.makerSettlement(book, *o, fill); err != nil {
		return err
	}
	if !c.at(hardfork.T12) {
		if err := c.subLiquidity(&cur.level, fill); err != nil {
			return err
		}
		if err := c.writeLevel(cur.ref(), cur.level); err != nil {
			return err
		}
	}
	return c.emitOrderFilled(fillEvent{order: *o, taker: cur.taker, amount: fill, partial: true})
}

func (c *Context) flipFilled(o dexOrder, book dexBook) error {
	p := tokenPair{base: book.base, quote: book.quote}
	if c.at(hardfork.T5) {
		return c.flipInPlace(o, p)
	}
	_, err := c.placeFlip(flipReq{
		placeReq:     placeReq{sender: o.maker, token: book.base, amount: o.amount, isBid: !o.isBid, tick: o.flipTick},
		flipTick:     o.tick,
		internalOnly: true,
	})
	return err
}

func (c *Context) settleFlip(d creditDeltas, o dexOrder, book dexBook) error {
	err := c.flipFilled(o, book)
	if err == nil {
		if c.at(hardfork.T5) {
			return nil
		}
		return c.creditDeleted(d, o)
	}
	if err == errFatal {
		return err
	}
	if c.at(hardfork.T1A) {
		if isSystemError(err) {
			return err
		}
	}
	if c.at(hardfork.T5) {
		e := c.dexEmit("FlipFailed(uint128,address,bytes4)", []Hash{toHash(o.id), addressWord(o.maker)},
			encodeWords(revertSelector(err)))
		if e != nil {
			return e
		}
	}
	return c.creditDeleted(d, o)
}

func isRevertErr(err error) bool {
	_, ok := err.(*Revert)
	return ok
}

func (c *Context) creditDeleted(d creditDeltas, o dexOrder) error {
	n, err := c.deleteOrder(o.id)
	if err != nil {
		return err
	}
	d.credit(o.maker, n)
	return nil
}

type advance struct {
	ok bool
}

func (c *Context) fillOrder(d creditDeltas, cur *cursor) (advance, error) {
	o := cur.order
	book, err := c.readBook(cur.book)
	if err != nil {
		return advance{}, err
	}
	fill := o.remaining
	if err := c.makerSettlement(book, o, fill); err != nil {
		return advance{}, err
	}
	if err := c.emitOrderFilled(fillEvent{order: o, taker: cur.taker, amount: fill}); err != nil {
		return advance{}, err
	}
	if o.isFlip {
		err = c.settleFlip(d, o, book)
	} else {
		err = c.creditDeleted(d, o)
	}
	if err != nil {
		return advance{}, err
	}
	if o.next.IsZero() {
		return c.clearLevel(cur)
	}
	return c.advanceInLevel(d, cur, fill)
}

func emptyTick(isBid flag) int16 {
	if isBid {
		return dexEmptyBid
	}
	return dexEmptyAsk
}

func (c *Context) clearLevel(cur *cursor) (advance, error) {
	o := cur.order
	ref := levelRef{book: cur.book, tick: o.tick, isBid: o.isBid}
	if err := c.deleteLevel(ref); err != nil {
		return advance{}, err
	}
	if err := c.updateTickBit(ref, bitClear); err != nil {
		return advance{}, err
	}
	next, err := c.nextInitializedTick(ref)
	if err != nil {
		return advance{}, err
	}
	best := emptyTick(flag(o.isBid))
	if next.initialized {
		best = next.tick
	}
	if err := c.writeBestTick(cur.book, flag(o.isBid), best); err != nil {
		return advance{}, err
	}
	if !next.initialized {
		return advance{}, nil
	}
	l, err := c.readLevel(levelRef{book: cur.book, tick: next.tick, isBid: o.isBid})
	if err != nil {
		return advance{}, err
	}
	no, err := c.readOrder(l.head, &cur.book)
	if err != nil {
		return advance{}, err
	}
	cur.level, cur.order = l, no
	return advance{ok: true}, nil
}

func (c *Context) advanceInLevel(d creditDeltas, cur *cursor, fill Word) (advance, error) {
	o := cur.order
	cur.level.head = o.next
	before := uint64(0)
	var err error
	if c.at(hardfork.T7) {
		if before, err = c.creditBalance(StablecoinDEXAddress); err != nil {
			return advance{}, err
		}
	}
	if err := c.writeOrderField(o.next, ofPrev, Word{}); err != nil {
		return advance{}, err
	}
	minted, err := c.mintedSince(StablecoinDEXAddress, before)
	if err != nil {
		return advance{}, err
	}
	if err := c.subLiquidity(&cur.level, fill); err != nil {
		return advance{}, err
	}
	if err := c.writeLevel(levelRef{book: cur.book, tick: o.tick, isBid: o.isBid}, cur.level); err != nil {
		return advance{}, err
	}
	no, err := c.readOrder(o.next, &cur.book)
	if err != nil {
		return advance{}, err
	}
	d.credit(no.maker, minted)
	cur.order = no
	return advance{ok: true}, nil
}

func (c *Context) bestLevel(book Hash, isBid flag) (tickLevel, error) {
	b, err := c.readBook(book)
	if err != nil {
		return tickLevel{}, err
	}
	tick := b.bestAsk
	if isBid {
		tick = b.bestBid
	}
	if tick == emptyTick(isBid) {
		return tickLevel{}, errSel("InsufficientLiquidity()")
	}
	return c.readLevel(levelRef{book: book, tick: tick, isBid: bool(isBid)})
}

type stepKind uint8

const (
	exactIn stepKind = iota
	exactOut
)

type orderStep struct {
	fill       Word
	accumulate Word
	nextAmount Word
}

type stepReq struct {
	kind   stepKind
	amount Word
	order  dexOrder
	isBid  bool
}

func (a num) satSub(b Word) Word {
	if b.Gt(Word(a)) {
		return Word{}
	}
	return Word(a).Sub(b)
}

func (a num) min(b Word) Word {
	if Word(a).Lt(b) {
		return Word(a)
	}
	return b
}

func (a num) addU128(b Word) (Word, error) {
	s := Word(a).Add(b)
	if !fitsU128(s) {
		return Word{}, underOverflow()
	}
	return s, nil
}

func stepExactIn(r stepReq) (orderStep, bool) {
	rem, tick := r.order.remaining, r.order.tick
	var fill, next Word
	if r.isBid {
		fill, next = num(r.amount).min(rem), num(r.amount).satSub(rem)
	} else {
		baseOut, ok := quoteToBase(priced{amount: r.amount, tick: tick, round: roundDown})
		if !ok {
			return orderStep{}, false
		}
		if baseOut.Gt(rem) {
			need, ok := baseToQuote(priced{amount: rem, tick: tick, round: roundUp})
			if !ok || need.Gt(r.amount) {
				return orderStep{}, false
			}
			next = r.amount.Sub(need)
		}
		fill = num(baseOut).min(rem)
	}
	acc, ok := takerOutput(fill, tick, flag(r.order.isBid))
	return orderStep{fill: fill, accumulate: acc, nextAmount: next}, ok
}

func stepExactOut(r stepReq) (orderStep, bool) {
	rem, tick := r.order.remaining, r.order.tick
	var fill, acc, demand Word
	if r.isBid {
		need, ok := quoteToBase(priced{amount: r.amount, tick: tick, round: roundUp})
		if !ok {
			return orderStep{}, false
		}
		fill = num(need).min(rem)
		acc, demand = fill, need
	} else {
		fill = num(r.amount).min(rem)
		q, ok := baseToQuote(priced{amount: fill, tick: tick, round: roundUp})
		if !ok {
			return orderStep{}, false
		}
		acc, demand = q, r.amount
	}
	next := Word{}
	if demand.Gt(rem) {
		got, ok := takerOutput(rem, tick, flag(r.order.isBid))
		if !ok || got.Gt(r.amount) {
			return orderStep{}, false
		}
		next = r.amount.Sub(got)
	}
	return orderStep{fill: fill, accumulate: acc, nextAmount: next}, true
}

func computeStep(r stepReq) (orderStep, bool) {
	if r.kind == exactIn {
		return stepExactIn(r)
	}
	return stepExactOut(r)
}

type walkMode uint8

const (
	walkSettle walkMode = iota
	walkQuote
)

type walkReq struct {
	mode   walkMode
	kind   stepKind
	deltas creditDeltas
	amount Word
	isBid  bool
}

func (c *Context) walkOrders(r walkReq, cur *cursor) (Word, error) {
	amount, total := r.amount, Word{}
	for steps := 0; steps < dexWalkMax && !amount.IsZero(); steps++ {
		s, ok := computeStep(stepReq{kind: r.kind, amount: amount, order: cur.order, isBid: r.isBid})
		if !ok {
			return Word{}, underOverflow()
		}
		if s.fill.Lt(cur.order.remaining) {
			if r.mode == walkSettle {
				if err := c.partialFill(cur, s.fill); err != nil {
					return Word{}, err
				}
			}
			return num(total).addU128(s.accumulate)
		}
		adv, err := c.walkNext(r, cur)
		if err != nil {
			return Word{}, err
		}
		if total, err = num(total).addU128(s.accumulate); err != nil {
			return Word{}, err
		}
		if !adv.ok {
			if !s.nextAmount.IsZero() {
				return Word{}, errSel("InsufficientLiquidity()")
			}
			return total, nil
		}
		amount = s.nextAmount
	}
	if !amount.IsZero() {
		return Word{}, errFatal
	}
	return total, nil
}

func (c *Context) walkNext(r walkReq, cur *cursor) (advance, error) {
	if r.mode == walkSettle {
		return c.fillOrder(r.deltas, cur)
	}
	o := cur.order
	if !o.next.IsZero() {
		no, err := c.readOrder(o.next, &cur.book)
		cur.order = no
		return advance{ok: true}, err
	}
	ref := levelRef{book: cur.book, tick: o.tick, isBid: r.isBid}
	next, err := c.nextInitializedTick(ref)
	if err != nil || !next.initialized {
		return advance{}, err
	}
	l, err := c.readLevel(levelRef{book: cur.book, tick: next.tick, isBid: r.isBid})
	if err != nil {
		return advance{}, err
	}
	no, err := c.readOrder(l.head, &cur.book)
	cur.level, cur.order = l, no
	return advance{ok: true}, err
}

func (c *Context) walkBook(r walkReq, book Hash, taker Address) (Word, error) {
	l, err := c.bestLevel(book, flag(r.isBid))
	if err != nil {
		return Word{}, err
	}
	o, err := c.readOrder(l.head, &book)
	if err != nil {
		return Word{}, err
	}
	return c.walkOrders(r, &cursor{book: book, taker: taker, level: l, order: o})
}

func (c *Context) unlinkNeighbor(id Word, which orderField, v Word) error {
	if !c.at(hardfork.T7) {
		return c.writeOrderField(id, which, v)
	}
	before, err := c.creditBalance(StablecoinDEXAddress)
	if err != nil {
		return err
	}
	if err := c.writeOrderField(id, which, v); err != nil {
		return err
	}
	minted, err := c.mintedSince(StablecoinDEXAddress, before)
	if err != nil || minted == 0 {
		return err
	}
	maker, err := c.orderMaker(id)
	if err != nil {
		return err
	}
	return c.creditDexSlots(maker, minted)
}

func (c *Context) cancel(sender Address, id Word) error {
	o, err := c.readOrder(id, nil)
	if err != nil {
		return err
	}
	if o.maker == (Address{}) {
		return errSel("OrderDoesNotExist()")
	}
	if o.maker != sender {
		return errSel("Unauthorized()")
	}
	if o.remaining.IsZero() {
		return errSel("OrderDoesNotExist()")
	}
	return c.cancelActive(o)
}

func (c *Context) unlinkOrder(o dexOrder, l *tickLevel) error {
	if o.prev.IsZero() {
		l.head = o.next
	} else if err := c.unlinkNeighbor(o.prev, ofNext, o.next); err != nil {
		return err
	}
	if o.next.IsZero() {
		l.tail = o.prev
		return nil
	}
	return c.unlinkNeighbor(o.next, ofPrev, o.prev)
}

func (c *Context) retireTick(o dexOrder) error {
	ref := levelRef{book: o.bookKey, tick: o.tick, isBid: o.isBid}
	if err := c.updateTickBit(ref, bitClear); err != nil {
		return err
	}
	book, err := c.readBook(o.bookKey)
	if err != nil {
		return err
	}
	best := book.bestAsk
	if o.isBid {
		best = book.bestBid
	}
	if best != o.tick {
		return nil
	}
	next, err := c.nextInitializedTick(ref)
	if err != nil {
		return err
	}
	nb := emptyTick(flag(o.isBid))
	if next.initialized {
		nb = next.tick
	}
	return c.writeBestTick(o.bookKey, flag(o.isBid), nb)
}

func (c *Context) refundOrder(o dexOrder) error {
	book, err := c.readBook(o.bookKey)
	if err != nil {
		return err
	}
	if !o.isBid {
		return c.incDexBalance(balanceDelta{holding{o.maker, book.base}, o.remaining})
	}
	q, ok := baseToQuote(priced{amount: o.remaining, tick: o.tick, round: roundUp})
	if !ok {
		return underOverflow()
	}
	return c.incDexBalance(balanceDelta{holding{o.maker, book.quote}, q})
}

func (c *Context) cancelActive(o dexOrder) error {
	ref := levelRef{book: o.bookKey, tick: o.tick, isBid: o.isBid}
	l, err := c.readLevel(ref)
	if err != nil {
		return err
	}
	if err := c.unlinkOrder(o, &l); err != nil {
		return err
	}
	changed := true
	if c.at(hardfork.T12) {
		changed = o.prev.IsZero() || o.next.IsZero()
	} else if err := c.subLiquidity(&l, o.remaining); err != nil {
		return err
	}
	if l.head.IsZero() {
		if err := c.retireTick(o); err != nil {
			return err
		}
	}
	if changed {
		if err := c.writeLevel(ref, l); err != nil {
			return err
		}
	}
	if err := c.refundOrder(o); err != nil {
		return err
	}
	n, err := c.deleteOrder(o.id)
	if err != nil {
		return err
	}
	if err := c.creditDexSlots(o.maker, n); err != nil {
		return err
	}
	return c.dexEmit("OrderCancelled(uint128)", []Hash{toHash(o.id)}, nil)
}

func (c *Context) cancelStale(id Word) error {
	o, err := c.readOrder(id, nil)
	if err != nil {
		return err
	}
	if o.maker == (Address{}) {
		return errSel("OrderDoesNotExist()")
	}
	ok, err := c.makerAuthorized(o)
	if err != nil {
		return err
	}
	if ok {
		return errSel("OrderNotStale()")
	}
	return c.cancelActive(o)
}

func (c *Context) makerAuthorized(o dexOrder) (bool, error) {
	book, err := c.readBook(o.bookKey)
	if err != nil {
		return false, err
	}
	in, out := book.base, book.quote
	if o.isBid {
		in, out = book.quote, book.base
	}
	ok, err := c.authorizedForToken(tokenRole{token: in, user: o.maker, role: roleSender})
	if err != nil || !ok || !c.at(hardfork.T4) {
		return ok, err
	}
	return c.authorizedForToken(tokenRole{token: out, user: o.maker, role: roleRecipient})
}

func (c *Context) withdraw(p payout) error {
	h := holding{p.to, p.token}
	bal, err := c.dexBalance(h)
	if err != nil {
		return err
	}
	if bal.Lt(p.amount) {
		return errSel("InsufficientBalance()")
	}
	if err := c.subDexBalance(balanceDelta{h, p.amount}); err != nil {
		return err
	}
	return c.dexTransfer(p)
}

func (c *Context) getOrder(id Word) (dexOrder, error) {
	o, err := c.readOrder(id, nil)
	if err != nil {
		return dexOrder{}, err
	}
	if o.maker == (Address{}) {
		return dexOrder{}, errSel("OrderDoesNotExist()")
	}
	next, err := c.nextOrderID()
	if err != nil {
		return dexOrder{}, err
	}
	if o.id.Lt(next) {
		return o, nil
	}
	return dexOrder{}, errSel("OrderDoesNotExist()")
}

type levelQuery struct {
	base  Address
	tick  int16
	isBid bool
}

func (c *Context) getPriceLevel(q levelQuery) (tickLevel, error) {
	ref, err := c.bookFor(q.base)
	if err != nil {
		return tickLevel{}, err
	}
	key := ref.key
	l, err := c.readLevel(levelRef{book: key, tick: q.tick, isBid: q.isBid})
	if err != nil || !c.at(hardfork.T12) {
		return l, err
	}
	l.liquidity = Word{}
	id := l.head
	for steps := 0; steps < dexWalkMax && !id.IsZero(); steps++ {
		o, err := c.readOrder(id, &key)
		if err != nil {
			return tickLevel{}, err
		}
		if l.liquidity, err = num(l.liquidity).addU128(o.remaining); err != nil {
			return tickLevel{}, err
		}
		id = o.next
	}
	if !id.IsZero() {
		return tickLevel{}, errFatal
	}
	return l, nil
}
