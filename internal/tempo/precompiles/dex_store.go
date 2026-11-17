package precompiles

import (
	"math/bits"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	dexMinTick     int16  = -2000
	dexMaxTick     int16  = 2000
	dexPriceScale  uint64 = 100_000
	dexTickSpacing int16  = 10
	dexMinPrice    uint64 = 98_000
	dexMaxPrice    uint64 = 102_000
)

var (
	dexMinOrderAmount = u256.FromUint64(100_000_000)
	slotDexBooks      = slotOf(0)
	slotDexOrders     = slotOf(1)
	slotDexBalances   = slotOf(2)
	slotDexNextID     = slotOf(3)
	slotDexBookKeys   = slotOf(4)
	slotDexCredits    = slotOf(5)
)

var (
	fDexOrderID    = field{offset: 0, size: 16}
	fDexMaker      = field{offset: 0, size: 20}
	fDexIsBid      = field{offset: 0, size: 1}
	fDexTick       = field{offset: 1, size: 2}
	fDexAmount     = field{offset: 3, size: 16}
	fDexLow128     = field{offset: 0, size: 16}
	fDexHigh128    = field{offset: 16, size: 16}
	fDexIsFlip     = field{offset: 16, size: 1}
	fDexFlipTick   = field{offset: 17, size: 2}
	fDexMeta       = field{offset: 20, size: 1}
	fDexCTick      = field{offset: 21, size: 2}
	fDexCFlipTick  = field{offset: 23, size: 2}
	fDexBookIndex  = field{offset: 25, size: 4}
	fDexVersion    = field{offset: 31, size: 1}
	fDexBestBid    = field{offset: 0, size: 2}
	fDexBestAsk    = field{offset: 2, size: 2}
	fDexBookID     = field{offset: 4, size: 4}
	dexFlagBid     = uint64(1)
	dexFlagFlip    = uint64(2)
	dexLegacySlots = uint64(6)
	dexV1Slots     = uint64(4)
	dexV2Slots     = uint64(3)
)

type orderVersion uint8

const (
	orderLegacy orderVersion = iota
	orderV1
	orderV2
)

func (v orderVersion) slots() uint64 {
	if v == orderV1 {
		return dexV1Slots
	}
	if v == orderV2 {
		return dexV2Slots
	}
	return dexLegacySlots
}

type dexOrder struct {
	id        Word
	maker     Address
	bookKey   Hash
	isBid     bool
	tick      int16
	amount    Word
	remaining Word
	prev      Word
	next      Word
	isFlip    bool
	flipTick  int16
}

func (o dexOrder) flipped() dexOrder {
	return dexOrder{
		id: o.id, maker: o.maker, bookKey: o.bookKey, isBid: !o.isBid, tick: o.flipTick,
		amount: o.amount, remaining: o.amount, isFlip: true, flipTick: o.tick,
	}
}

func i16Of(w Word, f field) int16 { return int16(uint16(f.u64(w))) }

func wordOfI16(v int16) Word { return u256.FromUint64(uint64(uint16(v))) }

func tickKey(t int16) Hash {
	var h Hash
	h[30] = byte(uint16(t) >> 8)
	h[31] = byte(uint16(t))
	return h
}

type tokenPair struct {
	base  Address
	quote Address
}

func dexBookKey(p tokenPair) Hash {
	var buf [40]byte
	copy(buf[:20], p.base[:])
	copy(buf[20:], p.quote[:])
	return keccak.Sum256(buf[:])
}

type lane struct {
	f field
	v Word
}

func (c *Context) storeRun(addr Address, s slot, lanes ...lane) error {
	w := Word{}
	if !c.at(hardfork.T4) {
		cur, err := c.sload(addr, s)
		if err != nil {
			return err
		}
		w = cur
	}
	p := pack(w)
	for _, l := range lanes {
		p = p.with(l.f, l.v)
	}
	return c.sstore(addr, s, p.w)
}

func (c *Context) dexLoad(s slot) (Word, error) { return c.sload(StablecoinDEXAddress, s) }

func (c *Context) dexLoadUint(s slot, bytes uint) (Word, error) {
	w, err := c.dexLoad(s)
	if err != nil {
		return Word{}, err
	}
	if w.Gt(fieldMask(bytes)) {
		return Word{}, underOverflow()
	}
	return w, nil
}

func (c *Context) dexStore(s slot, v Word) error { return c.sstore(StablecoinDEXAddress, s, v) }

func (c *Context) dexStoreField(s slot, f field, v Word) error {
	return c.storeField(StablecoinDEXAddress, s, f, v)
}

func (c *Context) orderSlot(id Word) slot { return c.mapKey(toHash(id), slotDexOrders) }

type versioned struct {
	version orderVersion
	slot0   Word
}

func (c *Context) orderVersion(base slot) (versioned, error) {
	if !c.at(hardfork.T8) {
		return versioned{}, nil
	}
	w, err := c.dexLoad(base)
	if err != nil {
		return versioned{}, err
	}
	v := fDexVersion.u64(w)
	if v > uint64(orderV2) {
		return versioned{}, errFatal
	}
	return versioned{version: orderVersion(v), slot0: w}, nil
}

type orderField uint8

const (
	ofRemaining orderField = iota
	ofPrev
	ofNext
)

type fieldLoc struct {
	s slot
	f field
}

func (c *Context) orderFieldLoc(id Word, which orderField) (fieldLoc, error) {
	base := c.orderSlot(id)
	v, err := c.orderVersion(base)
	if err != nil {
		return fieldLoc{}, err
	}
	if v.version == orderLegacy {
		if which == ofRemaining {
			return fieldLoc{s: base.plus(4), f: fDexLow128}, nil
		}
		if which == ofPrev {
			return fieldLoc{s: base.plus(4), f: fDexHigh128}, nil
		}
		return fieldLoc{s: base.plus(5), f: fDexLow128}, nil
	}
	if which == ofRemaining {
		return fieldLoc{s: base.plus(1), f: fDexHigh128}, nil
	}
	if which == ofPrev {
		return fieldLoc{s: base.plus(2), f: fDexLow128}, nil
	}
	return fieldLoc{s: base.plus(2), f: fDexHigh128}, nil
}

func (c *Context) writeOrderField(id Word, which orderField, v Word) error {
	loc, err := c.orderFieldLoc(id, which)
	if err != nil {
		return err
	}
	return c.dexStoreField(loc.s, loc.f, v)
}

func (c *Context) orderMaker(id Word) (Address, error) {
	base := c.orderSlot(id)
	v, err := c.orderVersion(base)
	if err != nil {
		return Address{}, err
	}
	if v.version == orderLegacy {
		return c.loadAddress(StablecoinDEXAddress, base.plus(1))
	}
	return wordAddress(toHash(v.slot0)), nil
}

func (c *Context) loadWords(base slot, v orderVersion) ([]Word, error) {
	out := make([]Word, v.slots())
	for i := range out {
		w, err := c.dexLoad(base.plus(uint64(i)))
		if err != nil {
			return nil, err
		}
		out[i] = w
	}
	return out, nil
}

func (c *Context) readOrder(id Word, knownBook *Hash) (dexOrder, error) {
	base := c.orderSlot(id)
	v, err := c.orderVersion(base)
	if err != nil {
		return dexOrder{}, err
	}
	w, err := c.loadWords(base, v.version)
	if err != nil {
		return dexOrder{}, err
	}
	if v.version == orderLegacy {
		return dexOrder{
			id: fDexOrderID.of(w[0]), maker: wordAddress(toHash(w[1])), bookKey: toHash(w[2]),
			isBid: fDexIsBid.u64(w[3]) != 0, tick: i16Of(w[3], fDexTick), amount: fDexAmount.of(w[3]),
			remaining: fDexLow128.of(w[4]), prev: fDexHigh128.of(w[4]), next: fDexLow128.of(w[5]),
			isFlip: fDexIsFlip.u64(w[5]) != 0, flipTick: i16Of(w[5], fDexFlipTick),
		}, nil
	}
	book, err := c.compactBookKey(v.version, w, knownBook)
	if err != nil {
		return dexOrder{}, err
	}
	meta := fDexMeta.u64(w[0])
	return dexOrder{
		id: id, maker: wordAddress(toHash(w[0])), bookKey: book,
		isBid: meta&dexFlagBid != 0, tick: i16Of(w[0], fDexCTick), amount: fDexLow128.of(w[1]),
		remaining: fDexHigh128.of(w[1]), prev: fDexLow128.of(w[2]), next: fDexHigh128.of(w[2]),
		isFlip: meta&dexFlagFlip != 0, flipTick: i16Of(w[0], fDexCFlipTick),
	}, nil
}

func (c *Context) compactBookKey(v orderVersion, w []Word, known *Hash) (Hash, error) {
	if v == orderV1 {
		return toHash(w[3]), nil
	}
	if known != nil {
		return *known, nil
	}
	return c.bookKeyForIndex(fDexBookIndex.u64(w[0]))
}

func (c *Context) writeOrderLegacy(recordID Word, o dexOrder) error {
	base := c.orderSlot(recordID)
	a := StablecoinDEXAddress
	if err := c.storeRun(a, base, lane{fDexOrderID, o.id}); err != nil {
		return err
	}
	if err := c.storeRun(a, base.plus(1), lane{fDexMaker, toWord(addressWord(o.maker))}); err != nil {
		return err
	}
	if err := c.dexStore(base.plus(2), toWord(o.bookKey)); err != nil {
		return err
	}
	err := c.storeRun(a, base.plus(3), lane{fDexIsBid, flag(o.isBid).word()},
		lane{fDexTick, wordOfI16(o.tick)}, lane{fDexAmount, o.amount})
	if err != nil {
		return err
	}
	err = c.storeRun(a, base.plus(4), lane{fDexLow128, o.remaining}, lane{fDexHigh128, o.prev})
	if err != nil {
		return err
	}
	return c.storeRun(a, base.plus(5), lane{fDexLow128, o.next}, lane{fDexIsFlip, flag(o.isFlip).word()},
		lane{fDexFlipTick, wordOfI16(o.flipTick)})
}

type bookIndex struct {
	index uint64
	ok    bool
}

func compactSlot0(o dexOrder) packed {
	meta := uint64(0)
	if o.isBid {
		meta |= dexFlagBid
	}
	if o.isFlip {
		meta |= dexFlagFlip
	}
	return pack(toWord(addressWord(o.maker))).withU64(fDexMeta, meta).
		with(fDexCTick, wordOfI16(o.tick)).with(fDexCFlipTick, wordOfI16(o.flipTick))
}

func (c *Context) writeOrder(o dexOrder, known *bookIndex) error {
	if !c.at(hardfork.T8) {
		return c.writeOrderLegacy(o.id, o)
	}
	base := c.orderSlot(o.id)
	old, err := c.orderVersion(base)
	if err != nil {
		return err
	}
	idx := bookIndex{}
	if known != nil {
		idx = *known
	} else if idx, err = c.bookKeyIndex(o.bookKey); err != nil {
		return err
	}
	s0 := compactSlot0(o)
	s1 := pack(Word{}).with(fDexLow128, o.amount).with(fDexHigh128, o.remaining).w
	s2 := pack(Word{}).with(fDexLow128, o.prev).with(fDexHigh128, o.next).w
	words := []Word{s0.withU64(fDexBookIndex, idx.index).withU64(fDexVersion, uint64(orderV2)).w, s1, s2}
	if !idx.ok {
		words = []Word{s0.withU64(fDexVersion, uint64(orderV1)).w, s1, s2, toWord(o.bookKey)}
	}
	for i, w := range words {
		if err := c.dexStore(base.plus(uint64(i)), w); err != nil {
			return err
		}
	}
	if old.slot0.IsZero() {
		return nil
	}
	for i := uint64(len(words)); i < old.version.slots(); i++ {
		if err := c.dexStore(base.plus(i), Word{}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) deleteOrderRecord(id Word) error {
	base := c.orderSlot(id)
	v, err := c.orderVersion(base)
	if err != nil {
		return err
	}
	for i := uint64(0); i < v.version.slots(); i++ {
		if err := c.dexStore(base.plus(i), Word{}); err != nil {
			return err
		}
	}
	return nil
}

type dexBook struct {
	base    Address
	quote   Address
	bestBid int16
	bestAsk int16
	bookID  uint64
}

func (b dexBook) initialized() bool { return b.base != (Address{}) }

func (b dexBook) index() bookIndex {
	if b.bookID == 0 {
		return bookIndex{}
	}
	return bookIndex{index: b.bookID - 1, ok: true}
}

func (c *Context) bookSlot(key Hash) slot { return c.mapKey(key, slotDexBooks) }

func (c *Context) readBook(key Hash) (dexBook, error) {
	base := c.bookSlot(key)
	b, err := c.loadAddress(StablecoinDEXAddress, base)
	if err != nil {
		return dexBook{}, err
	}
	q, err := c.loadAddress(StablecoinDEXAddress, base.plus(1))
	if err != nil {
		return dexBook{}, err
	}
	t, err := c.dexLoad(base.plus(4))
	if err != nil {
		return dexBook{}, err
	}
	return dexBook{base: b, quote: q, bestBid: i16Of(t, fDexBestBid), bestAsk: i16Of(t, fDexBestAsk),
		bookID: fDexBookID.u64(t)}, nil
}

func (c *Context) writeBook(key Hash, b dexBook) error {
	base := c.bookSlot(key)
	a := StablecoinDEXAddress
	if err := c.storeRun(a, base, lane{fDexMaker, toWord(addressWord(b.base))}); err != nil {
		return err
	}
	if err := c.storeRun(a, base.plus(1), lane{fDexMaker, toWord(addressWord(b.quote))}); err != nil {
		return err
	}
	return c.storeRun(a, base.plus(4), lane{fDexBestBid, wordOfI16(b.bestBid)},
		lane{fDexBestAsk, wordOfI16(b.bestAsk)}, lane{fDexBookID, u256.FromUint64(b.bookID)})
}

func (c *Context) writeBestTick(key Hash, isBid flag, tick int16) error {
	f := fDexBestAsk
	if isBid {
		f = fDexBestBid
	}
	return c.dexStoreField(c.bookSlot(key).plus(4), f, wordOfI16(tick))
}

type tickLevel struct {
	head      Word
	tail      Word
	liquidity Word
}

type levelRef struct {
	book  Hash
	tick  int16
	isBid bool
}

func (c *Context) levelSlot(r levelRef) slot {
	side := uint64(3)
	if r.isBid {
		side = 2
	}
	return c.mapKey(tickKey(r.tick), c.bookSlot(r.book).plus(side))
}

func (c *Context) readLevel(r levelRef) (tickLevel, error) {
	s := c.levelSlot(r)
	links, err := c.dexLoad(s)
	if err != nil {
		return tickLevel{}, err
	}
	l := tickLevel{head: fDexLow128.of(links), tail: fDexHigh128.of(links)}
	if !c.at(hardfork.T12) {
		liq, err := c.dexLoad(s.plus(1))
		if err != nil {
			return tickLevel{}, err
		}
		l.liquidity = fDexLow128.of(liq)
	}
	return l, nil
}

func (c *Context) writeLevel(r levelRef, l tickLevel) error {
	s := c.levelSlot(r)
	a := StablecoinDEXAddress
	if !c.at(hardfork.T4) {
		if err := c.storeRun(a, s, lane{fDexLow128, l.head}, lane{fDexHigh128, l.tail}); err != nil {
			return err
		}
		return c.storeRun(a, s.plus(1), lane{fDexLow128, l.liquidity})
	}
	links := pack(Word{}).with(fDexLow128, l.head).with(fDexHigh128, l.tail).w
	if err := c.dexStore(s, links); err != nil {
		return err
	}
	if c.at(hardfork.T12) {
		return nil
	}
	return c.dexStore(s.plus(1), l.liquidity)
}

func (c *Context) deleteLevel(r levelRef) error {
	s := c.levelSlot(r)
	if err := c.dexStore(s, Word{}); err != nil {
		return err
	}
	if c.at(hardfork.T12) {
		return nil
	}
	return c.dexStore(s.plus(1), Word{})
}

func (c *Context) bitmapSlot(book Hash, wordIndex int16, isBid flag) slot {
	side := uint64(6)
	if isBid {
		side = 5
	}
	return c.mapKey(tickKey(wordIndex), c.bookSlot(book).plus(side))
}

func tickWordIndex(tick int16) (int16, error) {
	if tick < dexMinTick || tick > dexMaxTick {
		return 0, errSel("InvalidTick()")
	}
	return tick >> 8, nil
}

func tickBit(tick int16) Word { return u256.One().Lsh(uint(uint16(tick) & 0xff)) }

type bitOp uint8

const (
	bitClear bitOp = iota
	bitSet
)

func (c *Context) updateTickBit(r levelRef, op bitOp) error {
	wi, err := tickWordIndex(r.tick)
	if err != nil {
		return err
	}
	s := c.bitmapSlot(r.book, wi, flag(r.isBid))
	cur, err := c.dexLoad(s)
	if err != nil {
		return err
	}
	if op == bitSet {
		return c.dexStore(s, cur.Or(tickBit(r.tick)))
	}
	return c.dexStore(s, cur.And(tickBit(r.tick).Not()))
}

type nextTick struct {
	tick        int16
	initialized bool
}

func (c *Context) nextInitializedTick(r levelRef) (nextTick, error) {
	if r.isBid {
		return c.nextBidTick(r.book, r.tick)
	}
	return c.nextAskTick(r.book, r.tick)
}

const dexBitmapWords = int(dexMaxTick>>8) - int(dexMinTick>>8) + 2

func (c *Context) nextAskTick(book Hash, tick int16) (nextTick, error) {
	if tick >= dexMaxTick {
		return nextTick{tick: dexMaxTick}, nil
	}
	next := tick + 1
	maxWord := dexMaxTick >> 8
	for steps := 0; steps < dexBitmapWords; steps++ {
		wi := next >> 8
		if wi > maxWord {
			return nextTick{tick: next}, nil
		}
		w, err := c.dexLoad(c.bitmapSlot(book, wi, flag(false)))
		if err != nil {
			return nextTick{}, err
		}
		masked := w.And(u256.Max().Lsh(uint(uint16(next) & 0xff)))
		if !masked.IsZero() {
			found := wi<<8 | int16(trailingZeros(masked))
			return nextTick{tick: found, initialized: found <= dexMaxTick}, nil
		}
		if wi+1 > maxWord {
			return nextTick{tick: (wi + 1) << 8}, nil
		}
		next = (wi + 1) << 8
	}
	return nextTick{}, errFatal
}

func (c *Context) nextBidTick(book Hash, tick int16) (nextTick, error) {
	if tick <= dexMinTick {
		return nextTick{tick: dexMinTick}, nil
	}
	next := tick - 1
	minWord := dexMinTick >> 8
	for steps := 0; steps < dexBitmapWords; steps++ {
		wi := next >> 8
		if wi < minWord {
			return nextTick{tick: next}, nil
		}
		w, err := c.dexLoad(c.bitmapSlot(book, wi, flag(true)))
		if err != nil {
			return nextTick{}, err
		}
		masked := w.And(u256.Max().Rsh(255 - uint(uint16(next)&0xff)))
		if !masked.IsZero() {
			found := wi<<8 | int16(masked.BitLen()-1)
			return nextTick{tick: found, initialized: found >= dexMinTick}, nil
		}
		if wi-1 < minWord {
			return nextTick{tick: (wi-1)<<8 | 0xff}, nil
		}
		next = (wi-1)<<8 | 0xff
	}
	return nextTick{}, errFatal
}

func trailingZeros(w Word) int {
	for i := 0; i < 4; i++ {
		if w[i] != 0 {
			return i*64 + bits.TrailingZeros64(w[i])
		}
	}
	return 256
}

func (c *Context) bookKeyIndex(key Hash) (bookIndex, error) {
	b, err := c.readBook(key)
	if err != nil {
		return bookIndex{}, err
	}
	if !b.initialized() {
		return bookIndex{}, errSel("PairDoesNotExist()")
	}
	return b.index(), nil
}

func (c *Context) bookKeysLen() (uint64, error) {
	n, err := c.dexLoad(slotDexBookKeys)
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() {
		return 0, errFatal
	}
	return n.Uint64(), nil
}

func (c *Context) bookKeysElem(i uint64) slot {
	return slot(keccak.Sum256(slotDexBookKeys[:])).plus(i)
}

func (c *Context) bookKeyForIndex(i uint64) (Hash, error) {
	n, err := c.bookKeysLen()
	if err != nil {
		return Hash{}, err
	}
	if i >= n {
		return Hash{}, errSel("PairDoesNotExist()")
	}
	w, err := c.dexLoad(c.bookKeysElem(i))
	return toHash(w), err
}

func (c *Context) nextOrderID() (Word, error) {
	w, err := c.dexLoadUint(slotDexNextID, 16)
	if err != nil {
		return Word{}, err
	}
	if w.IsZero() {
		return u256.One(), nil
	}
	return w, nil
}

type holding struct {
	user  Address
	token Address
}

func (c *Context) dexBalanceSlot(h holding) slot {
	return c.mapAddr(h.token, c.mapAddr(h.user, slotDexBalances))
}

func (c *Context) dexBalance(h holding) (Word, error) {
	return c.dexLoadUint(c.dexBalanceSlot(h), 16)
}

func (c *Context) setDexBalance(h holding, v Word) error {
	return c.dexStore(c.dexBalanceSlot(h), v)
}

type balanceDelta struct {
	holding
	amount Word
}

func (c *Context) incDexBalance(d balanceDelta) error {
	cur, err := c.dexBalance(d.holding)
	if err != nil {
		return err
	}
	next := cur.Add(d.amount)
	if !fitsU128(next) {
		return underOverflow()
	}
	return c.setDexBalance(d.holding, next)
}

func (c *Context) subDexBalance(d balanceDelta) error {
	cur, err := c.dexBalance(d.holding)
	if err != nil {
		return err
	}
	if d.amount.Gt(cur) {
		return underOverflow()
	}
	return c.setDexBalance(d.holding, cur.Sub(d.amount))
}

func fitsU128(w Word) bool { return w[2]|w[3] == 0 }

func (c *Context) dexCreditsSlot(user Address) slot { return c.mapAddr(user, slotDexCredits) }

func (c *Context) dexCredits(user Address) (uint64, error) {
	w, err := c.dexLoadUint(c.dexCreditsSlot(user), 8)
	return w.Uint64(), err
}
