package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type reserveMove struct {
	from         Address
	to           recipient
	amount       Word
	policySender Address
}

func (t Token) ensureReceivePolicyAuthorized(h hop) error {
	block, err := t.c.checkReceivePolicy(inbound{token: t.addr, sender: h.from, receiver: h.to})
	if err != nil {
		return err
	}
	if block.blocked {
		return errSel("PolicyForbids()")
	}
	return nil
}

func (t Token) channelReserveTransfer(m reserveMove) error {
	if m.from != ChannelReserveAddress && m.to.original() != ChannelReserveAddress {
		return errSel("Unauthorized()")
	}
	if err := t.checkNotPaused(); err != nil {
		return err
	}
	if err := m.to.validate(); err != nil {
		return err
	}
	if err := t.c.authorizeTransfer(tokenFlow{account: m.from, token: t.addr, amount: m.amount}); err != nil {
		return err
	}
	if m.to.target == ReceivePolicyGuardAddress {
		return errSel("AddressReserved()")
	}
	if err := t.ensureReceivePolicyAuthorized(hop{from: m.policySender, to: m.to.target}); err != nil {
		return err
	}
	if err := t.move(m.from, m.to, m.amount); err != nil {
		return err
	}
	return t.emitVirtualHop(m.to, m.amount)
}

type openReq struct {
	desc    channelDescriptor
	deposit Word
}

func (c *Context) openChannel(r openReq) (Hash, error) {
	d := r.desc
	if invalidPayee(d) {
		return Hash{}, errSel("InvalidPayee()")
	}
	token, err := c.reserveToken(d.token)
	if err != nil {
		return Hash{}, err
	}
	if r.deposit.IsZero() {
		return Hash{}, errSel("ZeroDeposit()")
	}
	if err := c.ensureVoucherSignerActive(d.expectedSigner()); err != nil {
		return Hash{}, err
	}
	if d.context, err = c.openContext(); err != nil {
		return Hash{}, err
	}
	id, err := c.channelID(d)
	if err != nil {
		return Hash{}, err
	}
	if err := c.ensureChannelAbsent(id); err != nil {
		return Hash{}, err
	}
	if err := c.pullDeposit(depositPull{token: token, desc: d, amount: r.deposit, policySender: d.payer}); err != nil {
		return Hash{}, err
	}
	state := channelState{deposit: r.deposit, logical: flag(c.at(hardfork.T12))}
	if err := c.writeChannelSpendingCredit(channelWrite{payer: d.payer, id: id, state: state}); err != nil {
		return Hash{}, err
	}
	if err := c.tstore(ChannelReserveAddress, c.mapKey(id, slotReserveOpenedInTx), u256.FromUint64(1)); err != nil {
		return Hash{}, err
	}
	err = c.emitReserve(evReserveOpened, d, id, addressWord(d.operator), addressWord(d.token), addressWord(d.signer), d.salt, d.context, toHash(r.deposit))
	return id, err
}

func (c *Context) ensureChannelAbsent(id Hash) error {
	s, err := c.loadChannelState(id)
	if err != nil {
		return err
	}
	if s.exists() {
		return errSel("ChannelAlreadyExists()")
	}
	opened, err := c.tload(ChannelReserveAddress, c.mapKey(id, slotReserveOpenedInTx))
	if err != nil {
		return err
	}
	if !opened.IsZero() {
		return errSel("ChannelAlreadyExists()")
	}
	return nil
}

type depositPull struct {
	token        Token
	desc         channelDescriptor
	amount       Word
	policySender Address
}

func (c *Context) pullDeposit(p depositPull) error {
	payee, err := c.resolveRecipient(p.desc.payee)
	if err != nil {
		return err
	}
	if !c.at(hardfork.T12) {
		if err := p.token.ensureAuthorizedAs(party{user: payee.target, role: roleRecipient}); err != nil {
			return err
		}
		return p.token.systemTransferFrom(systemPull{caller: ChannelReserveAddress, from: p.desc.payer, amount: p.amount})
	}
	if err := p.token.ensureTransferAuthorized(hop{from: p.desc.payer, to: payee.target}); err != nil {
		return err
	}
	if err := p.token.ensureReceivePolicyAuthorized(hop{from: p.policySender, to: payee.target}); err != nil {
		return err
	}
	return p.token.channelReserveTransfer(reserveMove{from: p.desc.payer, to: direct(ChannelReserveAddress), amount: p.amount, policySender: p.desc.payer})
}

type reservePayout struct {
	token  Token
	desc   channelDescriptor
	state  channelState
	amount Word
}

func (c *Context) payPayeeT12(p reservePayout) error {
	payee, err := c.resolveRecipient(p.desc.payee)
	if err != nil {
		return err
	}
	if err := p.token.ensureTransferAuthorized(hop{from: p.desc.payer, to: payee.target}); err != nil {
		return err
	}
	return p.token.channelReserveTransfer(reserveMove{from: ChannelReserveAddress, to: payee, amount: p.amount, policySender: p.state.policySender(p.desc.payer)})
}

func (c *Context) refundPayerT12(p reservePayout) error {
	payer, err := c.resolveRecipient(p.desc.payer)
	if err != nil {
		return err
	}
	return p.token.channelReserveTransfer(reserveMove{from: ChannelReserveAddress, to: payer, amount: p.amount, policySender: ChannelReserveAddress})
}

func (c *Context) reserveSend(p reservePayout, to Address) error {
	return p.token.transfer(transferReq{from: ChannelReserveAddress, to: to, amount: p.amount})
}

type settleReq struct {
	sender Address
	desc   channelDescriptor
	amount Word
	sig    []byte
}

func (c *Context) settleChannel(r settleReq) error {
	d := r.desc
	id, err := c.channelID(d)
	if err != nil {
		return err
	}
	state, err := c.loadExistingChannel(id)
	if err != nil {
		return err
	}
	if err := ensurePayeeOrOperator(r.sender, d); err != nil {
		return err
	}
	if r.amount.Gt(state.deposit) {
		return errSel("AmountExceedsDeposit()")
	}
	if !r.amount.Gt(state.settled) {
		return errSel("AmountNotIncreasing()")
	}
	if err := c.validateVoucher(signedVoucher{desc: d, v: voucher{id: id, cumulative: r.amount}, sig: r.sig}); err != nil {
		return err
	}
	delta := r.amount.Sub(state.settled)
	token, err := c.reserveToken(d.token)
	if err != nil {
		return err
	}
	p := reservePayout{token: token, desc: d, state: state, amount: delta}
	state.settled = r.amount
	if c.at(hardfork.T12) {
		if err := c.payPayeeT12(p); err != nil {
			return err
		}
		if err := c.writeChannelState(id, state); err != nil {
			return err
		}
	} else {
		if err := token.ensureAuthorizedAs(party{user: d.payer, role: roleSender}); err != nil {
			return err
		}
		if err := c.writeChannelState(id, state); err != nil {
			return err
		}
		if err := c.reserveSend(p, d.payee); err != nil {
			return err
		}
	}
	return c.emitReserve(evReserveSettled, d, id, toHash(r.amount), toHash(delta), toHash(r.amount))
}

type topUpReq struct {
	sender Address
	desc   channelDescriptor
	amount Word
}

var maxU96 = fieldMask(12)

func (c *Context) topUpChannel(r topUpReq) error {
	d := r.desc
	id, err := c.channelID(d)
	if err != nil {
		return err
	}
	state, err := c.loadExistingChannel(id)
	if err != nil {
		return err
	}
	if err := ensurePayer(r.sender, d); err != nil {
		return err
	}
	hadClose := state.closeAt != 0
	if r.amount.IsZero() && !hadClose {
		return nil
	}
	if !r.amount.IsZero() {
		if err := c.ensureVoucherSignerActive(d.expectedSigner()); err != nil {
			return err
		}
		next := state.deposit.Add(r.amount)
		if next.Gt(maxU96) {
			return errSel("DepositOverflow()")
		}
		token, err := c.reserveToken(d.token)
		if err != nil {
			return err
		}
		pull := depositPull{token: token, desc: d, amount: r.amount, policySender: state.policySender(r.sender)}
		state.deposit = next
		if err := c.pullDeposit(pull); err != nil {
			return err
		}
	}
	state.closeAt = 0
	if err := c.writeChannelState(id, state); err != nil {
		return err
	}
	if hadClose {
		if err := c.emitReserve(evReserveCloseCancelled, d, id); err != nil {
			return err
		}
	}
	return c.emitReserve(evReserveTopUp, d, id, toHash(r.amount), toHash(state.deposit))
}

type closeReq struct {
	sender  Address
	desc    channelDescriptor
	amount  Word
	capture Word
	sig     []byte
}

func (c *Context) closeChannel(r closeReq) error {
	d := r.desc
	id, err := c.channelID(d)
	if err != nil {
		return err
	}
	state, err := c.loadExistingChannel(id)
	if err != nil {
		return err
	}
	if err := ensurePayeeOrOperator(r.sender, d); err != nil {
		return err
	}
	if r.capture.Lt(state.settled) || r.capture.Gt(r.amount) {
		return errSel("CaptureAmountInvalid()")
	}
	if r.capture.Gt(state.deposit) {
		return errSel("AmountExceedsDeposit()")
	}
	if r.capture.Gt(state.settled) {
		if err := c.validateVoucher(signedVoucher{desc: d, v: voucher{id: id, cumulative: r.amount}, sig: r.sig}); err != nil {
			return err
		}
	}
	split := payoutSplit{desc: d, state: state, id: id, delta: r.capture.Sub(state.settled), refund: state.deposit.Sub(r.capture)}
	if err := c.payOut(split); err != nil {
		return err
	}
	return c.emitReserve(evReserveChannelClosed, d, id, toHash(r.capture), toHash(split.refund))
}

type payoutSplit struct {
	desc   channelDescriptor
	state  channelState
	id     Hash
	delta  Word
	refund Word
}

func (c *Context) payOut(s payoutSplit) error {
	if c.at(hardfork.T12) {
		return c.payOutT12(s)
	}
	if err := c.deleteChannelCreditingPayer(s.desc.payer, s.id); err != nil {
		return err
	}
	token, err := c.reserveToken(s.desc.token)
	if err != nil {
		return err
	}
	if !s.delta.IsZero() {
		if err := token.ensureAuthorizedAs(party{user: s.desc.payer, role: roleSender}); err != nil {
			return err
		}
		if err := c.reserveSend(reservePayout{token: token, amount: s.delta}, s.desc.payee); err != nil {
			return err
		}
	}
	if s.refund.IsZero() {
		return nil
	}
	return c.reserveSend(reservePayout{token: token, amount: s.refund}, s.desc.payer)
}

func (c *Context) payOutT12(s payoutSplit) error {
	token, err := c.reserveToken(s.desc.token)
	if err != nil {
		return err
	}
	if !s.delta.IsZero() {
		if err := c.payPayeeT12(reservePayout{token: token, desc: s.desc, state: s.state, amount: s.delta}); err != nil {
			return err
		}
	}
	if !s.refund.IsZero() {
		if err := c.refundPayerT12(reservePayout{token: token, desc: s.desc, amount: s.refund}); err != nil {
			return err
		}
	}
	return c.deleteChannelCreditingPayer(s.desc.payer, s.id)
}

type channelAction struct {
	sender Address
	desc   channelDescriptor
}

func (c *Context) requestCloseChannel(r channelAction) error {
	d := r.desc
	id, err := c.channelID(d)
	if err != nil {
		return err
	}
	state, err := c.loadExistingChannel(id)
	if err != nil {
		return err
	}
	if err := ensurePayer(r.sender, d); err != nil {
		return err
	}
	if state.closeAt != 0 {
		return nil
	}
	state.closeAt = c.reserveNow32()
	if err := c.writeChannelState(id, state); err != nil {
		return err
	}
	return c.emitReserve(evReserveCloseRequested, d, id, wordOfU64(c.reserveNow()+reserveCloseGrace))
}

func (c *Context) withdrawChannel(r channelAction) error {
	d := r.desc
	id, err := c.channelID(d)
	if err != nil {
		return err
	}
	state, err := c.loadExistingChannel(id)
	if err != nil {
		return err
	}
	if err := ensurePayer(r.sender, d); err != nil {
		return err
	}
	if state.closeAt == 0 || c.reserveNow() < state.closeAt+reserveCloseGrace {
		return errSel("CloseNotReady()")
	}
	refund := state.deposit.Sub(state.settled)
	if err := c.withdrawRefund(payoutSplit{desc: d, state: state, id: id, refund: refund}); err != nil {
		return err
	}
	return c.emitReserve(evReserveChannelClosed, d, id, toHash(state.settled), toHash(refund))
}

func (c *Context) withdrawRefund(s payoutSplit) error {
	if c.at(hardfork.T12) {
		if !s.refund.IsZero() {
			token, err := c.reserveToken(s.desc.token)
			if err != nil {
				return err
			}
			if err := c.refundPayerT12(reservePayout{token: token, desc: s.desc, amount: s.refund}); err != nil {
				return err
			}
		}
		return c.deleteChannelCreditingPayer(s.desc.payer, s.id)
	}
	if err := c.deleteChannelCreditingPayer(s.desc.payer, s.id); err != nil {
		return err
	}
	if s.refund.IsZero() {
		return nil
	}
	token, err := c.reserveToken(s.desc.token)
	if err != nil {
		return err
	}
	return c.reserveSend(reservePayout{token: token, amount: s.refund}, s.desc.payer)
}
