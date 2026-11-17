package precompiles

import "github.com/gakonst/goempo/internal/tempo/hardfork"

const (
	guardReceiptWords  = 10
	guardReceiptBytes  = guardReceiptWords * 32
	guardKindInvalid   = uint8(0xff)
	guardRecoveryOrig  = uint8(0)
	guardRecoveryRecv  = uint8(1)
	guardRecoveryThird = uint8(2)
	guardKindWord      = 8
)

const _ = uint(guardReceiptWords - guardKindWord - 1)

var (
	evReceiptClaimed = topic("ReceiptClaimed(address,address,uint64,uint64,uint8,address,address,address,address,address,uint256)")
	evReceiptBurned  = topic("ReceiptBurned(address,address,uint64,uint64,uint8,address,address,address,address,uint256)")
)

type claimReceipt struct {
	version   uint8
	token     Address
	authority Address
	origin    Address
	recipient Address
	at        uint64
	nonce     uint64
	reason    uint8
	kind      uint8
	memo      Hash
}

func receiptWord(b []byte, i int) Hash { return Hash(b[i*32 : i*32+32]) }

func receiptU64(h Hash) uint64 { return toWord(h).Uint64() }

func decodeClaimReceipt(b []byte) (claimReceipt, error) {
	if len(b) < guardReceiptBytes {
		return claimReceipt{}, errSel("InvalidReceipt()")
	}
	kind := receiptWord(b, guardKindWord)[31]
	if kind > inboundMint {
		kind = guardKindInvalid
	}
	return claimReceipt{
		version:   receiptWord(b, 0)[31],
		token:     wordAddress(receiptWord(b, 1)),
		authority: wordAddress(receiptWord(b, 2)),
		origin:    wordAddress(receiptWord(b, 3)),
		recipient: wordAddress(receiptWord(b, 4)),
		at:        receiptU64(receiptWord(b, 5)),
		nonce:     receiptU64(receiptWord(b, 6)),
		reason:    receiptWord(b, 7)[31],
		kind:      kind,
		memo:      receiptWord(b, 9),
	}, nil
}

func (r claimReceipt) encode() []byte {
	return encodeWords(
		wordOfU64(uint64(r.version)), addressWord(r.token), addressWord(r.authority), addressWord(r.origin),
		addressWord(r.recipient), wordOfU64(r.at), wordOfU64(r.nonce), wordOfU64(uint64(r.reason)),
		wordOfU64(uint64(r.kind)), r.memo,
	)
}

func (c *Context) receiptKey(r claimReceipt) (slot, error) {
	buf := r.encode()
	if err := c.chargeKeccak(len(buf)); err != nil {
		return slot{}, err
	}
	return c.mapKey(c.keccak(buf), slotGuardBalances), nil
}

type resolvedReceipt struct {
	r        claimReceipt
	receiver Address
	mode     uint8
}

func (c *Context) resolveReceipt(b []byte) (resolvedReceipt, error) {
	r, err := decodeClaimReceipt(b)
	if err != nil {
		return resolvedReceipt{}, err
	}
	to, err := c.resolveRecipient(r.recipient)
	if err != nil {
		return resolvedReceipt{}, errSel("InvalidClaimAddress()")
	}
	mode := guardRecoveryThird
	if r.authority == (Address{}) {
		mode = guardRecoveryOrig
	} else if r.authority == to.target {
		mode = guardRecoveryRecv
	}
	return resolvedReceipt{r: r, receiver: to.target, mode: mode}, nil
}

func (rr resolvedReceipt) authority() Address {
	if rr.mode == guardRecoveryOrig {
		return rr.r.origin
	}
	return rr.r.authority
}

func (rr resolvedReceipt) policySubject() Address {
	if rr.mode == guardRecoveryOrig {
		return rr.r.origin
	}
	return rr.receiver
}

func (rr resolvedReceipt) isReroute(to Address) bool {
	return rr.mode == guardRecoveryOrig || to != rr.receiver
}

func (c *Context) receiptBalance(b []byte) (Word, error) {
	r, err := decodeClaimReceipt(b)
	if err != nil {
		return Word{}, err
	}
	key, err := c.receiptKey(r)
	if err != nil {
		return Word{}, err
	}
	return c.sload(ReceivePolicyGuardAddress, key)
}

type claimReq struct {
	sender  Address
	to      Address
	receipt []byte
}

func (c *Context) claimReceipt(q claimReq) error {
	if q.to == ReceivePolicyGuardAddress {
		return errSel("InvalidClaimAddress()")
	}
	rr, err := c.resolveReceipt(q.receipt)
	if err != nil {
		return err
	}
	auth := rr.authority()
	if auth != q.sender {
		return errSel("UnauthorizedClaimer()")
	}
	key, err := c.receiptKey(rr.r)
	if err != nil {
		return err
	}
	amount, err := c.sload(ReceivePolicyGuardAddress, key)
	if err != nil {
		return err
	}
	if amount.IsZero() {
		return errSel("InvalidReceipt()")
	}
	if err := c.sstore(ReceivePolicyGuardAddress, key, Word{}); err != nil {
		return err
	}
	token, err := c.reserveToken(rr.r.token)
	if err != nil {
		return err
	}
	if err := token.releaseBlockedFunds(releaseReq{rr: rr, to: q.to, amount: amount, auth: auth}); err != nil {
		return err
	}
	r := rr.r
	data := encodeWords(wordOfU64(r.at), wordOfU64(uint64(r.version)), addressWord(r.origin), addressWord(r.recipient),
		addressWord(r.authority), addressWord(q.sender), addressWord(q.to), toHash(amount))
	return c.emit(ReceivePolicyGuardAddress, []Hash{evReceiptClaimed, addressWord(r.token), addressWord(rr.receiver), wordOfU64(r.nonce)}, data)
}

type releaseReq struct {
	rr     resolvedReceipt
	to     Address
	amount Word
	auth   Address
}

func (t Token) releaseBlockedFunds(q releaseReq) error {
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	dest, err := t.c.resolveRecipient(q.to)
	if err != nil {
		return err
	}
	if err := dest.validate(); err != nil {
		return err
	}
	if q.rr.isReroute(q.to) {
		if err := t.releaseReroute(q, dest); err != nil {
			return err
		}
	} else if err := t.ensureAuthorizedAs(party{user: dest.target, role: roleRecipient}); err != nil {
		return err
	}
	if err := t.move(ReceivePolicyGuardAddress, dest, q.amount); err != nil {
		return err
	}
	return t.emitVirtualHop(dest, q.amount)
}

func (t Token) releaseReroute(q releaseReq, dest recipient) error {
	subject := q.rr.policySubject()
	if err := t.ensureTransferAuthorized(hop{from: subject, to: dest.target}); err != nil {
		return err
	}
	if err := t.ensureReceivePolicyAuthorized(hop{from: subject, to: dest.target}); err != nil {
		return err
	}
	if q.rr.mode == guardRecoveryThird {
		return nil
	}
	return t.c.authorizeTransfer(tokenFlow{account: q.auth, token: t.addr, amount: q.amount})
}

type burnReceiptReq struct {
	sender  Address
	receipt []byte
}

func (c *Context) burnBlockedReceipt(q burnReceiptReq) error {
	rr, err := c.resolveReceipt(q.receipt)
	if err != nil {
		return err
	}
	key, err := c.receiptKey(rr.r)
	if err != nil {
		return err
	}
	amount, err := c.sload(ReceivePolicyGuardAddress, key)
	if err != nil {
		return err
	}
	if amount.IsZero() {
		return errSel("InvalidReceipt()")
	}
	token, err := c.reserveToken(rr.r.token)
	if err != nil {
		return err
	}
	if err := token.burnGuardHeld(burnReq{sender: q.sender, owner: rr.policySubject(), amount: amount}); err != nil {
		return err
	}
	if err := c.sstore(ReceivePolicyGuardAddress, key, Word{}); err != nil {
		return err
	}
	r := rr.r
	data := encodeWords(wordOfU64(r.at), wordOfU64(uint64(r.version)), addressWord(r.origin), addressWord(r.recipient),
		addressWord(r.authority), addressWord(q.sender), toHash(amount))
	return c.emit(ReceivePolicyGuardAddress, []Hash{evReceiptBurned, addressWord(r.token), addressWord(rr.receiver), wordOfU64(r.nonce)}, data)
}

func (t Token) burnGuardHeld(req burnReq) error {
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	if err := t.checkRole(req.sender, roleBurnBlocked); err != nil {
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
	if err := t.move(ReceivePolicyGuardAddress, direct(Address{}), req.amount); err != nil {
		return err
	}
	if err := t.reduceSupply(req.amount); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evBurnBlocked, addressWord(req.owner)}, encodeWords(toHash(req.amount)))
}

var receivePolicyGuardTable = register(ReceivePolicyGuardAddress, newTable(
	view("balanceOf(bytes)", []abiType{tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.receiptBalance(a[0].data)
		return retWord(w), err
	}).from(hardfork.T6),
	mutate("claim(address,bytes)", []abiType{tAddress, tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.claimReceipt(claimReq{sender: c.Caller, to: a[0].addr(), receipt: a[1].data})
	}).from(hardfork.T6),
	mutate("burnBlockedReceipt(bytes)", []abiType{tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.burnBlockedReceipt(burnReceiptReq{sender: c.Caller, receipt: a[0].data})
	}).from(hardfork.T6),
))
