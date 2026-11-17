package precompiles

import (
	"bytes"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotRoles          = slotOf(0)
	slotRoleAdmins     = slotOf(1)
	slotName           = slotOf(2)
	slotSymbol         = slotOf(3)
	slotCurrency       = slotOf(4)
	slotLogoURI        = slotOf(5)
	slotQuoteToken     = slotOf(6)
	slotNextQuoteToken = slotOf(7)
	slotTransferPolicy = slotOf(7)
	slotTotalSupply    = slotOf(8)
	slotBalances       = slotOf(9)
	slotAllowances     = slotOf(10)
	slotPermitNonces   = slotOf(11)
	slotPaused         = slotOf(12)
	slotSupplyCap      = slotOf(13)
	slotGlobalRPT      = slotOf(15)
	slotOptedInSupply  = slotOf(16)
	slotUserRewardInfo = slotOf(17)
)

const (
	tip20Decimals   = 6
	maxLogoURIBytes = 256
)

var (
	fTransferPolicy = field{offset: 20, size: 8}
	fNextQuote      = field{offset: 0, size: 20}
	fU128           = field{offset: 0, size: 16}
)

var (
	roleDefaultAdmin = Hash{}
	rolePause        = topic("PAUSE_ROLE")
	roleUnpause      = topic("UNPAUSE_ROLE")
	roleIssuer       = topic("ISSUER_ROLE")
	roleBurnBlocked  = topic("BURN_BLOCKED_ROLE")
	roleBurnAt       = topic("BURN_AT_ROLE")
	accPrecision     = u256.FromUint64(1_000_000_000_000_000_000)
	u128Max          = fieldMask(16)
	usdCurrency      = []byte("USD")
	zonePortalPrefix = [12]byte{0x5a, 0xd0}
)

type Token struct {
	c    *Context
	addr Address
}

func TIP20(c *Context, addr Address) Token { return Token{c: c, addr: addr} }

func (t Token) BalanceOf(holder Address) (Word, error) { return t.balance(holder) }

func (t Token) load(s slot) (Word, error) { return t.c.sload(t.addr, s) }

func (t Token) store(s slot, v Word) error { return t.c.sstore(t.addr, s, v) }

func (t Token) balanceSlot(holder Address) slot { return t.c.mapAddr(holder, slotBalances) }

type pair struct {
	owner   Address
	spender Address
}

func (t Token) allowanceSlot(p pair) slot {
	return t.c.mapAddr(p.spender, t.c.mapAddr(p.owner, slotAllowances))
}

func (t Token) balance(holder Address) (Word, error) { return t.load(t.balanceSlot(holder)) }

func (t Token) paused() (bool, error) { return t.c.loadBool(t.addr, slotPaused) }

func (t Token) checkNotPaused() error {
	p, err := t.paused()
	if err != nil {
		return err
	}
	if p {
		return errSel("ContractPaused()")
	}
	return nil
}

func (t Token) transferPolicy() (uint64, error) {
	if t.c.at(hardfork.T9) {
		reg, err := t.c.registeredTokenPolicy(t.addr)
		if err != nil {
			return 0, err
		}
		if reg.set {
			return reg.id, nil
		}
	}
	w, err := t.c.loadField(t.addr, slotTransferPolicy, fTransferPolicy)
	return w.Uint64(), err
}

func (t Token) roleSlot(account Address, role Hash) slot {
	return t.c.mapKey(role, t.c.mapAddr(account, slotRoles))
}

func (t Token) hasRole(account Address, role Hash) (bool, error) {
	return t.c.loadBool(t.addr, t.roleSlot(account, role))
}

func (t Token) checkRole(account Address, role Hash) error {
	ok, err := t.hasRole(account, role)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("Unauthorized()")
	}
	return nil
}

func (t Token) roleAdmin(role Hash) (Hash, error) {
	w, err := t.load(t.c.mapKey(role, slotRoleAdmins))
	return toHash(w), err
}

type recipient struct {
	target  Address
	virtual Address
	isVirt  bool
}

func (r recipient) original() Address {
	if r.isVirt {
		return r.virtual
	}
	return r.target
}

func direct(a Address) recipient { return recipient{target: a} }

func (c *Context) resolveRecipient(to Address) (recipient, error) {
	if !c.at(hardfork.T3) || !IsVirtual(to) {
		return direct(to), nil
	}
	m, err := c.registryMaster(virtualMaster(to))
	if err != nil {
		return recipient{}, err
	}
	if m == (Address{}) {
		return recipient{}, errSel("VirtualAddressUnregistered()")
	}
	return recipient{target: m, virtual: to, isVirt: true}, nil
}

func (r recipient) validate() error {
	if r.target == (Address{}) || IsTIP20(r.target) {
		return errSel("InvalidRecipient()")
	}
	return nil
}

func (t Token) emitVirtualHop(r recipient, amount Word) error {
	if !r.isVirt {
		return nil
	}
	return t.emitTransfer(transferEvent{from: r.virtual, to: r.target, amount: amount})
}

type transferEvent struct {
	from   Address
	to     Address
	amount Word
}

func (t Token) emitTransfer(e transferEvent) error {
	return t.c.emit(t.addr, []Hash{evTransfer, addressWord(e.from), addressWord(e.to)}, encodeWords(toHash(e.amount)))
}

func (t Token) emitMemo(e transferEvent, memo Hash) error {
	return t.c.emit(t.addr, []Hash{evTransferWithMemo, addressWord(e.from), addressWord(e.to), memo}, encodeWords(toHash(e.amount)))
}

type hop struct {
	from Address
	to   Address
}

func (t Token) isTransferAuthorized(h hop) (bool, error) {
	policy, err := t.transferPolicy()
	if err != nil {
		return false, err
	}
	sender, err := t.c.isAuthorizedAs(authQuery{id: policy, user: h.from}, roleSender)
	if err != nil {
		return false, err
	}
	if t.c.at(hardfork.T2) && !sender {
		return false, nil
	}
	rcpt, err := t.c.isAuthorizedAs(authQuery{id: policy, user: h.to}, roleRecipient)
	if err != nil {
		return false, err
	}
	return sender && rcpt, nil
}

func (t Token) ensureTransferAuthorized(h hop) error {
	ok, err := t.isTransferAuthorized(h)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("PolicyForbids()")
	}
	return nil
}

type party struct {
	user Address
	role authRole
}

func (t Token) ensureAuthorizedAs(parties ...party) error {
	policy, err := t.transferPolicy()
	if err != nil {
		return err
	}
	for _, p := range parties {
		ok, err := t.c.isAuthorizedAs(authQuery{id: policy, user: p.user}, p.role)
		if err != nil {
			return err
		}
		if !ok {
			return errSel("PolicyForbids()")
		}
	}
	return nil
}

func (t Token) allowance(p pair) (Word, error) { return t.load(t.allowanceSlot(p)) }

func (t Token) consumeAllowance(p pair, amount Word) error {
	allowed, err := t.allowance(p)
	if err != nil {
		return err
	}
	if amount.Gt(allowed) {
		return errSel("InsufficientAllowance()")
	}
	if allowed == u256.Max() {
		return nil
	}
	return t.store(t.allowanceSlot(p), allowed.Sub(amount))
}

type inboundReq struct {
	originator Address
	to         recipient
	amount     Word
	mintSupply *Word
	memo       Hash
}

func (t Token) validateInbound(req inboundReq) (bool, error) {
	if !t.c.at(hardfork.T6) {
		return false, nil
	}
	if req.to.target == ReceivePolicyGuardAddress {
		return false, errSel("AddressReserved()")
	}
	block, err := t.c.checkReceivePolicy(inbound{token: t.addr, sender: req.originator, receiver: req.to.target})
	if err != nil || !block.blocked {
		return false, err
	}
	return true, t.c.blockInbound(t, req, block)
}

type transferReq struct {
	spender *Address
	from    Address
	to      Address
	amount  Word
	memo    *Hash
}

func (r transferReq) memoWord() Hash {
	if r.memo == nil {
		return Hash{}
	}
	return *r.memo
}

type resolved struct {
	to      recipient
	proceed bool
}

func (t Token) validateTransfer(req transferReq) (resolved, error) {
	r, err := t.c.resolveRecipient(req.to)
	if err != nil {
		return resolved{}, err
	}
	if err := t.checkNotPaused(); err != nil {
		return resolved{}, err
	}
	if err := r.validate(); err != nil {
		return resolved{}, err
	}
	if err := t.ensureTransferAuthorized(hop{from: req.from, to: r.target}); err != nil {
		return resolved{}, err
	}
	if req.spender != nil {
		err = t.consumeAllowance(pair{owner: req.from, spender: *req.spender}, req.amount)
	} else {
		err = t.c.authorizeTransfer(tokenFlow{account: req.from, token: t.addr, amount: req.amount})
	}
	if err != nil {
		return resolved{}, err
	}
	blocked, err := t.validateInbound(inboundReq{originator: req.from, to: r, amount: req.amount, memo: req.memoWord()})
	return resolved{to: r, proceed: !blocked}, err
}

type balanceShortfall struct {
	have Word
	want Word
}

func (t Token) insufficientBalance(s balanceShortfall) error {
	return revert(abiError(selector("InsufficientBalance(uint256,uint256,address)"), toHash(s.have), toHash(s.want), addressWord(t.addr)))
}

func (t Token) incrementBalance(holder Address, amount Word) error {
	s := t.balanceSlot(holder)
	cur, err := t.load(s)
	if err != nil {
		return err
	}
	next, overflow := cur.AddOverflow(amount)
	if overflow {
		return errSel("SupplyCapExceeded()")
	}
	return t.store(s, next)
}

func (t Token) decrementBalance(holder Address, amount Word) error {
	s := t.balanceSlot(holder)
	cur, err := t.load(s)
	if err != nil {
		return err
	}
	if amount.Gt(cur) {
		return t.insufficientBalance(balanceShortfall{have: cur, want: amount})
	}
	return t.store(s, cur.Sub(amount))
}

func (t Token) move(from Address, to recipient, amount Word) error {
	var fromBalance Word
	legacy := !t.c.at(hardfork.T8)
	if legacy {
		b, err := t.balance(from)
		if err != nil {
			return err
		}
		if amount.Gt(b) {
			return t.insufficientBalance(balanceShortfall{have: b, want: amount})
		}
		fromBalance = b
	}
	if err := t.rewardsOnTransfer(hop{from: from, to: to.target}, amount); err != nil {
		return err
	}
	var err error
	if legacy {
		err = t.store(t.balanceSlot(from), fromBalance.Sub(amount))
	} else {
		err = t.decrementBalance(from, amount)
	}
	if err != nil {
		return err
	}
	if to.target != (Address{}) {
		if err := t.incrementBalance(to.target, amount); err != nil {
			return err
		}
	}
	return t.emitTransfer(transferEvent{from: from, to: to.original(), amount: amount})
}

func (t Token) transfer(req transferReq) error {
	res, err := t.validateTransfer(req)
	if err != nil || !res.proceed {
		return err
	}
	if err := t.move(req.from, res.to, req.amount); err != nil {
		return err
	}
	if req.memo != nil {
		if err := t.emitMemo(transferEvent{from: req.from, to: req.to, amount: req.amount}, *req.memo); err != nil {
			return err
		}
	}
	return t.emitVirtualHop(res.to, req.amount)
}

func (t Token) approve(p pair, amount Word) error {
	old, err := t.allowance(p)
	if err != nil {
		return err
	}
	if err := t.c.authorizeApprove(approvalChange{owner: p.owner, token: t.addr, old: old, next: amount}); err != nil {
		return err
	}
	if err := t.store(t.allowanceSlot(p), amount); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evApproval, addressWord(p.owner), addressWord(p.spender)}, encodeWords(toHash(amount)))
}

func (t Token) totalSupply() (Word, error) { return t.load(slotTotalSupply) }

type mintInto struct {
	to     recipient
	supply Word
	amount Word
}

func (t Token) mintInto(m mintInto) error {
	next, err := num(m.supply).add(m.amount)
	if err != nil {
		return err
	}
	cap, err := t.load(slotSupplyCap)
	if err != nil {
		return err
	}
	if next.Gt(cap) {
		return errSel("SupplyCapExceeded()")
	}
	if err := t.rewardsOnMint(m.to.target, m.amount); err != nil {
		return err
	}
	if err := t.store(slotTotalSupply, next); err != nil {
		return err
	}
	if err := t.incrementBalance(m.to.target, m.amount); err != nil {
		return err
	}
	return t.emitTransfer(transferEvent{to: m.to.original(), amount: m.amount})
}

func (t Token) emitMint(to Address, amount Word) error {
	return t.c.emit(t.addr, []Hash{evMint, addressWord(to)}, encodeWords(toHash(amount)))
}

type mintReq struct {
	sender Address
	to     Address
	amount Word
	memo   *Hash
}

func (t Token) mint(req mintReq) error {
	r, err := t.c.resolveRecipient(req.to)
	if err != nil {
		return err
	}
	if err := t.checkRole(req.sender, roleIssuer); err != nil {
		return err
	}
	supply, err := t.totalSupply()
	if err != nil {
		return err
	}
	if t.c.at(hardfork.T3) {
		if err := t.checkNotPaused(); err != nil {
			return err
		}
		if err := r.validate(); err != nil {
			return err
		}
	}
	if err := t.ensureAuthorizedAs(party{user: r.target, role: roleMintRecipient}); err != nil {
		return err
	}
	memo := transferReq{memo: req.memo}.memoWord()
	blocked, err := t.validateInbound(inboundReq{originator: req.sender, to: r, amount: req.amount, mintSupply: &supply, memo: memo})
	if err != nil || blocked {
		return err
	}
	if err := t.mintInto(mintInto{to: r, supply: supply, amount: req.amount}); err != nil {
		return err
	}
	if req.memo != nil {
		if err := t.emitMemo(transferEvent{to: req.to, amount: req.amount}, *req.memo); err != nil {
			return err
		}
	}
	if err := t.emitMint(req.to, req.amount); err != nil {
		return err
	}
	return t.emitVirtualHop(r, req.amount)
}

func (t Token) reduceSupply(amount Word) error {
	supply, err := t.totalSupply()
	if err != nil {
		return err
	}
	if amount.Gt(supply) {
		return t.insufficientBalance(balanceShortfall{have: supply, want: amount})
	}
	return t.store(slotTotalSupply, supply.Sub(amount))
}

func (t Token) burn(sender Address, amount Word, memo *Hash) error {
	if t.c.at(hardfork.T3) {
		if err := t.checkNotPaused(); err != nil {
			return err
		}
	}
	if err := t.checkRole(sender, roleIssuer); err != nil {
		return err
	}
	if err := t.move(sender, direct(Address{}), amount); err != nil {
		return err
	}
	if err := t.reduceSupply(amount); err != nil {
		return err
	}
	if memo != nil {
		if err := t.emitMemo(transferEvent{from: sender, amount: amount}, *memo); err != nil {
			return err
		}
	}
	return t.c.emit(t.addr, []Hash{evBurn, addressWord(sender)}, encodeWords(toHash(amount)))
}

func (t Token) isProtected(from Address) bool {
	f := t.c.Fork
	if from == FeeManagerAddress || from == StablecoinDEXAddress {
		return true
	}
	if f >= hardfork.T5 {
		if from == ChannelReserveAddress || from == t.addr {
			return true
		}
	}
	if f >= hardfork.T6 && from == ReceivePolicyGuardAddress {
		return true
	}
	return f >= hardfork.T12 && bytes.HasPrefix(from[:], zonePortalPrefix[:2])
}

func (t Token) checkBurnAddress(from Address) error {
	if t.isProtected(from) {
		return errSel("ProtectedAddress()")
	}
	return nil
}

type burnReq struct {
	sender Address
	owner  Address
	amount Word
}

func (t Token) burnBlocked(req burnReq) error {
	if t.c.at(hardfork.T3) {
		if err := t.checkNotPaused(); err != nil {
			return err
		}
	}
	if err := t.checkRole(req.sender, roleBurnBlocked); err != nil {
		return err
	}
	if err := t.checkBurnAddress(req.owner); err != nil {
		return err
	}
	policy, err := t.transferPolicy()
	if err != nil {
		return err
	}
	ok, err := t.c.isAuthorizedAs(authQuery{id: policy, user: req.owner}, roleSender)
	if err != nil {
		return err
	}
	if ok {
		return errSel("PolicyForbids()")
	}
	if err := t.move(req.owner, direct(Address{}), req.amount); err != nil {
		return err
	}
	if err := t.reduceSupply(req.amount); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evBurnBlocked, addressWord(req.owner)}, encodeWords(toHash(req.amount)))
}

func (t Token) burnAt(req burnReq) error {
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	if err := t.checkRole(req.sender, roleBurnAt); err != nil {
		return err
	}
	if err := t.checkBurnAddress(req.owner); err != nil {
		return err
	}
	if err := t.c.authorizeTransfer(tokenFlow{account: req.owner, token: t.addr, amount: req.amount}); err != nil {
		return err
	}
	if err := t.move(req.owner, direct(Address{}), req.amount); err != nil {
		return err
	}
	if err := t.reduceSupply(req.amount); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evBurnAt, addressWord(req.sender), addressWord(req.owner), toHash(req.amount)}, nil)
}

type systemPull struct {
	caller Address
	from   Address
	amount Word
}

func (t Token) systemTransferFrom(p systemPull) error {
	if t.c.at(hardfork.T5) && !isImplicitlyApproved(p.caller, t.c.Fork) {
		return errSel("Unauthorized()")
	}
	res, err := t.validateTransfer(transferReq{from: p.from, to: p.caller, amount: p.amount})
	if err != nil || !res.proceed {
		return err
	}
	if err := t.move(p.from, res.to, p.amount); err != nil {
		return err
	}
	return t.emitVirtualHop(res.to, p.amount)
}

func (t Token) transferFeePreTx(from Address, amount Word) error {
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	if err := t.c.authorizeTransfer(tokenFlow{account: from, token: t.addr, amount: amount}); err != nil {
		return err
	}
	delegate, err := t.updateRewards(from)
	if err != nil {
		return err
	}
	if delegate != (Address{}) {
		if err := t.adjustOptedIn(amount, optOut); err != nil {
			return err
		}
	}
	if err := t.decrementBalance(from, amount); err != nil {
		return err
	}
	return t.incrementBalance(FeeManagerAddress, amount)
}

type feeReturn struct {
	to     Address
	refund Word
	actual Word
}

func (t Token) transferFeePostTx(f feeReturn) error {
	if err := t.emitTransfer(transferEvent{from: f.to, to: FeeManagerAddress, amount: f.actual}); err != nil {
		return err
	}
	if f.refund.IsZero() {
		return nil
	}
	if t.c.at(hardfork.T1C) {
		if err := t.c.refundSpendingLimit(tokenFlow{account: f.to, token: t.addr, amount: f.refund}); err != nil {
			return err
		}
	}
	delegate, err := t.updateRewards(f.to)
	if err != nil {
		return err
	}
	if delegate != (Address{}) {
		if err := t.adjustOptedIn(f.refund, optIn); err != nil {
			return err
		}
	}
	if err := t.decrementBalance(FeeManagerAddress, f.refund); err != nil {
		return err
	}
	return t.incrementBalance(f.to, f.refund)
}

func (t Token) currency() ([]byte, error) { return t.c.loadString(t.addr, slotCurrency) }

func (t Token) quoteToken() (Address, error) { return t.c.loadAddress(t.addr, slotQuoteToken) }

func (t Token) nextQuoteToken() (Address, error) {
	w, err := t.c.loadField(t.addr, slotNextQuoteToken, fNextQuote)
	return wordAddress(toHash(w)), err
}

func validLogoURI(uri []byte) error {
	if len(uri) > maxLogoURIBytes {
		return errSel("LogoURITooLong()")
	}
	if len(uri) > 0 && !allowedLogoURI(uri) {
		return errSel("InvalidLogoURI()")
	}
	return nil
}

func isASCIIAlpha(b byte) bool {
	lower := b | 0x20
	return lower >= 'a' && lower <= 'z'
}

func isSchemeByte(b byte) bool {
	if isASCIIAlpha(b) {
		return true
	}
	if b >= '0' && b <= '9' {
		return true
	}
	return b == '+' || b == '-' || b == '.'
}

func allowedLogoURI(uri []byte) bool {
	colon := bytes.IndexByte(uri, ':')
	if colon <= 0 || !isASCIIAlpha(uri[0]) {
		return false
	}
	scheme := uri[:colon]
	for _, b := range scheme {
		if !isSchemeByte(b) {
			return false
		}
	}
	for _, allowed := range []string{"https", "http", "ipfs", "data"} {
		if bytes.EqualFold(scheme, []byte(allowed)) {
			return true
		}
	}
	return false
}
