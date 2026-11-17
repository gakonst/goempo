package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	reserveCloseGrace   uint64 = 15 * 60
	reserveMainnetChain uint64 = 4217
	reserveTestnetChain uint64 = 42431
)

var (
	slotReserveStates      = slotOf(0)
	slotReserveCredits     = slotOf(1)
	slotReserveOpenedInTx  = slotOf(2)
	slotReserveOpenContext = slotOf(3)

	fReserveSettled  = field{offset: 0, size: 12}
	fReserveDeposit  = field{offset: 12, size: 12}
	fReserveCloseAt  = field{offset: 24, size: 4}
	fReserveLogical  = field{offset: 28, size: 1}
	tU96             = scalarArg(kUint, 96)
	reserveVoucherTH = topic("Voucher(bytes32 channelId,uint96 cumulativeAmount)")

	evReserveOpened          = topic("ChannelOpened(bytes32,address,address,address,address,address,bytes32,bytes32,uint96)")
	evReserveSettled         = topic("Settled(bytes32,address,address,uint96,uint96,uint96)")
	evReserveTopUp           = topic("TopUp(bytes32,address,address,uint96,uint96)")
	evReserveCloseRequested  = topic("CloseRequested(bytes32,address,address,uint256)")
	evReserveCloseCancelled  = topic("CloseRequestCancelled(bytes32,address,address)")
	evReserveChannelClosed   = topic("ChannelClosed(bytes32,address,address,uint96,uint96)")
	reserveDescriptorArgs    = []abiType{tAddress, tAddress, tAddress, tAddress, tB32, tAddress, tB32}
	reserveDescriptorArgsLen = len(reserveDescriptorArgs)
)

type channelDescriptor struct {
	payer    Address
	payee    Address
	operator Address
	token    Address
	salt     Hash
	signer   Address
	context  Hash
}

func descriptorOf(a []abiValue) channelDescriptor {
	return channelDescriptor{
		payer: a[0].addr(), payee: a[1].addr(), operator: a[2].addr(), token: a[3].addr(),
		salt: a[4].bytes32(), signer: a[5].addr(), context: a[6].bytes32(),
	}
}

func (d channelDescriptor) expectedSigner() Address {
	if d.signer == (Address{}) {
		return d.payer
	}
	return d.signer
}

func (d channelDescriptor) words() []Hash {
	return []Hash{addressWord(d.payer), addressWord(d.payee), addressWord(d.operator), addressWord(d.token), d.salt, addressWord(d.signer), d.context}
}

type channelState struct {
	settled Word
	deposit Word
	closeAt uint64
	logical flag
}

func decodeChannelState(w Word) channelState {
	return channelState{
		settled: fReserveSettled.of(w), deposit: fReserveDeposit.of(w),
		closeAt: fReserveCloseAt.u64(w), logical: flag(!fReserveLogical.of(w).IsZero()),
	}
}

func (s channelState) word() Word {
	p := pack(Word{}).with(fReserveSettled, s.settled).with(fReserveDeposit, s.deposit)
	p = p.with(fReserveCloseAt, u256.FromUint64(s.closeAt))
	if s.logical {
		p = p.with(fReserveLogical, u256.FromUint64(1))
	}
	return p.w
}

func (s channelState) exists() bool { return !s.deposit.IsZero() }

func (s channelState) policySender(payer Address) Address {
	if s.logical {
		return payer
	}
	return ChannelReserveAddress
}

func (s channelState) encode() []Hash {
	return []Hash{toHash(s.settled), toHash(s.deposit), wordOfU64(s.closeAt)}
}

func SetChannelOpenContext(c *Context, h Hash) {
	c.State.SetTransientStorage(ChannelReserveAddress, Hash(slotReserveOpenContext), toWord(h))
}

func (c *Context) reserveStateSlot(id Hash) slot { return c.mapKey(id, slotReserveStates) }

func (c *Context) reserveCreditSlot(payer Address) slot { return c.mapAddr(payer, slotReserveCredits) }

func (c *Context) reserveNow() uint64 { return c.timestamp() }

func (c *Context) reserveNow32() uint64 { return min(c.timestamp(), uint64(^uint32(0))) }

func (c *Context) reserveToken(addr Address) (Token, error) {
	if !IsTIP20(addr) {
		return Token{}, errSel("InvalidToken()")
	}
	return TIP20(c, addr), nil
}

func (c *Context) channelID(d channelDescriptor) (Hash, error) {
	words := append(d.words(), addressWord(ChannelReserveAddress), toHash(c.ChainID))
	buf := encodeWords(words...)
	if err := c.chargeKeccak(len(buf)); err != nil {
		return Hash{}, err
	}
	return c.keccak(buf), nil
}

func (c *Context) reserveDomainSeparator() Hash {
	return c.keccak(encodeWords(
		topic("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"),
		topic("TIP20 Channel Reserve"),
		topic("1"),
		toHash(c.ChainID),
		addressWord(ChannelReserveAddress),
	))
}

type voucher struct {
	id         Hash
	cumulative Word
}

func (c *Context) voucherDigest(v voucher) (Hash, error) {
	structBuf := encodeWords(reserveVoucherTH, v.id, toHash(v.cumulative))
	if err := c.chargeKeccak(len(structBuf)); err != nil {
		return Hash{}, err
	}
	structHash := c.keccak(structBuf)
	domain := c.reserveDomainSeparator()
	var buf [66]byte
	buf[0], buf[1] = 0x19, 0x01
	copy(buf[2:34], domain[:])
	copy(buf[34:], structHash[:])
	if err := c.chargeKeccak(len(buf)); err != nil {
		return Hash{}, err
	}
	return c.keccak(buf[:]), nil
}

type signedVoucher struct {
	desc channelDescriptor
	v    voucher
	sig  []byte
}

func (c *Context) validateVoucher(s signedVoucher) error {
	digest, err := c.voucherDigest(s.v)
	if err != nil {
		return err
	}
	signer, err := c.recoverSigner(digest, s.sig)
	if _, ok := err.(*Revert); ok {
		return errSel("InvalidSignature()")
	}
	if err != nil {
		return err
	}
	if signer != s.desc.expectedSigner() {
		return errSel("InvalidSignature()")
	}
	return nil
}

func (c *Context) ensureVoucherSignerActive(signer Address) error {
	retired, err := c.isRetiredRoot(signer)
	if err != nil {
		return err
	}
	if retired {
		return revert(abiError(selector("RootKeyRetired(address)"), addressWord(signer)))
	}
	return nil
}

func (c *Context) loadChannelState(id Hash) (channelState, error) {
	w, err := c.sload(ChannelReserveAddress, c.reserveStateSlot(id))
	return decodeChannelState(w), err
}

func (c *Context) loadExistingChannel(id Hash) (channelState, error) {
	s, err := c.loadChannelState(id)
	if err != nil {
		return s, err
	}
	if !s.exists() {
		return s, errSel("ChannelNotFound()")
	}
	return s, nil
}

func (c *Context) writeChannelState(id Hash, s channelState) error {
	return c.sstore(ChannelReserveAddress, c.reserveStateSlot(id), s.word())
}

type channelWrite struct {
	payer Address
	id    Hash
	state channelState
}

func (c *Context) writeChannelSpendingCredit(w channelWrite) error {
	if !c.at(hardfork.T7) {
		return c.writeChannelState(w.id, w.state)
	}
	cs := c.reserveCreditSlot(w.payer)
	cur, err := c.sload(ChannelReserveAddress, cs)
	if err != nil {
		return err
	}
	if cur.IsZero() {
		return c.writeChannelState(w.id, w.state)
	}
	if !cur.IsUint64() {
		return underOverflow()
	}
	if err := c.sstore(ChannelReserveAddress, cs, Word{}); err != nil {
		return err
	}
	b, err := c.budgetBegin(ChannelReserveAddress, cur.Uint64())
	if err != nil {
		return err
	}
	werr := c.writeChannelState(w.id, w.state)
	d, err := c.budgetEnd(b)
	if werr != nil {
		return werr
	}
	if err != nil {
		return err
	}
	if !d.spentOne() {
		return errFatal
	}
	return c.creditChannelSlots(creditGrant{payer: w.payer, slots: cur.Uint64() - 1})
}

type creditGrant struct {
	payer Address
	slots uint64
}

func (c *Context) creditChannelSlots(g creditGrant) error {
	if g.slots == 0 {
		return nil
	}
	cs := c.reserveCreditSlot(g.payer)
	cur, err := c.sload(ChannelReserveAddress, cs)
	if err != nil {
		return err
	}
	if !cur.IsUint64() {
		return underOverflow()
	}
	updated := cur.Uint64() + g.slots
	if updated < g.slots {
		updated = ^uint64(0)
	}
	if !cur.IsZero() {
		return c.sstore(ChannelReserveAddress, cs, u256.FromUint64(updated))
	}
	b, err := c.budgetBegin(ChannelReserveAddress, 1)
	if err != nil {
		return err
	}
	werr := c.sstore(ChannelReserveAddress, cs, u256.FromUint64(updated))
	d, err := c.budgetEnd(b)
	if werr != nil {
		return werr
	}
	if err != nil {
		return err
	}
	if c.at(hardfork.T7) && !d.spentOne() {
		return errFatal
	}
	return nil
}

func (c *Context) deleteChannelCreditingPayer(payer Address, id Hash) error {
	before := uint64(0)
	if c.at(hardfork.T7) {
		b, err := c.creditBalance(ChannelReserveAddress)
		if err != nil {
			return err
		}
		before = b
	}
	if err := c.sstore(ChannelReserveAddress, c.reserveStateSlot(id), Word{}); err != nil {
		return err
	}
	minted, err := c.mintedSince(ChannelReserveAddress, before)
	if err != nil {
		return err
	}
	return c.creditChannelSlots(creditGrant{payer: payer, slots: minted})
}

func (c *Context) openContext() (Hash, error) {
	w, err := c.tload(ChannelReserveAddress, slotReserveOpenContext)
	if err != nil {
		return Hash{}, err
	}
	if w.IsZero() {
		return Hash{}, errSel("ExpiringNonceHashNotSet()")
	}
	return toHash(w), nil
}

func ensurePayer(sender Address, d channelDescriptor) error {
	if sender != d.payer {
		return errSel("NotPayer()")
	}
	return nil
}

func ensurePayeeOrOperator(sender Address, d channelDescriptor) error {
	if sender == d.payee {
		return nil
	}
	if d.operator != (Address{}) && sender == d.operator {
		return nil
	}
	return errSel("NotPayeeOrOperator()")
}

func invalidPayee(d channelDescriptor) bool {
	if d.payee == (Address{}) || IsTIP20(d.payee) {
		return true
	}
	if !IsVirtual(d.payee) {
		return false
	}
	return d.operator == (Address{}) || IsVirtual(d.operator)
}

func (c *Context) emitReserve(ev Hash, d channelDescriptor, id Hash, data ...Hash) error {
	return c.emit(ChannelReserveAddress, []Hash{ev, id, addressWord(d.payer), addressWord(d.payee)}, encodeWords(data...))
}
