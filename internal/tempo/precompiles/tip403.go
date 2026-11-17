package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const (
	policyRejectAll uint64 = 0
	policyAllowAll  uint64 = 1
)

const (
	policyWhitelist uint8 = 0
	policyBlacklist uint8 = 1
	policyCompound  uint8 = 2
)

type authRole uint8

const (
	roleTransfer authRole = iota
	roleSender
	roleRecipient
	roleMintRecipient
)

var (
	slot403Counter        = slotOf(0)
	slot403Records        = slotOf(1)
	slot403Set            = slotOf(2)
	slot403Receive        = slotOf(3)
	slot403TokenTransfers = slotOf(4)
)

var (
	fPolicyType  = field{offset: 0, size: 1}
	fPolicyAdmin = field{offset: 1, size: 20}
	fU64At0      = field{offset: 0, size: 8}
	fU64At8      = field{offset: 8, size: 8}
	fU64At16     = field{offset: 16, size: 8}
	fFlagAt8     = field{offset: 8, size: 1}
	fRcvHas      = field{offset: 0, size: 1}
	fRcvSender   = field{offset: 1, size: 8}
	fRcvSendType = field{offset: 9, size: 1}
	fRcvToken    = field{offset: 10, size: 8}
	fRcvTokType  = field{offset: 18, size: 1}
	fRcvRecovery = field{offset: 19, size: 1}
)

type policyData struct {
	policyType uint8
	admin      Address
}

func (p policyData) isDefault() bool {
	if p.policyType != 0 {
		return false
	}
	return p.admin == (Address{})
}

func decodePolicyData(w Word) policyData {
	return policyData{
		policyType: uint8(fPolicyType.u64(w)),
		admin:      wordAddress(toHash(fPolicyAdmin.of(w))),
	}
}

func encodePolicyData(p policyData) Word {
	return pack(Word{}).withU64(fPolicyType, uint64(p.policyType)).with(fPolicyAdmin, toWord(addressWord(p.admin))).w
}

type builtin uint8

const (
	builtinNone builtin = iota
	builtinAllow
	builtinReject
)

func builtinPolicy(id uint64) builtin {
	if id == policyAllowAll {
		return builtinAllow
	}
	if id == policyRejectAll {
		return builtinReject
	}
	return builtinNone
}

func (c *Context) policyCounter() (uint64, error) {
	w, err := c.sload(TIP403RegistryAddress, slot403Counter)
	if err != nil {
		return 0, err
	}
	return max(fU64At0.u64(w), 2), nil
}

func (c *Context) policyRecordSlot(id uint64) slot { return c.mapU64(id, slot403Records) }

func (c *Context) policyData(id uint64) (policyData, error) {
	w, err := c.sload(TIP403RegistryAddress, c.policyRecordSlot(id))
	if err != nil {
		return policyData{}, err
	}
	data := decodePolicyData(w)
	if !c.at(hardfork.T2) || !data.isDefault() {
		return data, nil
	}
	counter, err := c.policyCounter()
	if err != nil {
		return policyData{}, err
	}
	if id >= counter {
		return policyData{}, errSel("PolicyNotFound()")
	}
	return data, nil
}

func (c *Context) checkedPolicyType(p policyData) (uint8, error) {
	if p.policyType < policyCompound {
		return p.policyType, nil
	}
	if c.at(hardfork.T2) {
		if p.policyType == policyCompound {
			return p.policyType, nil
		}
		return 0, errSel("InvalidPolicyType()")
	}
	return 0, underOverflow()
}

func (c *Context) inPolicySet(id uint64, user Address) (bool, error) {
	return c.loadBool(TIP403RegistryAddress, c.mapAddr(user, c.mapU64(id, slot403Set)))
}

type authQuery struct {
	id   uint64
	user Address
}

func (c *Context) authorizedSimpleInner(q authQuery, data policyData) (bool, error) {
	in, err := c.inPolicySet(q.id, q.user)
	if err != nil {
		return false, err
	}
	t, err := c.checkedPolicyType(data)
	if err != nil {
		return false, err
	}
	if t == policyWhitelist {
		return in, nil
	}
	if t == policyBlacklist {
		return !in, nil
	}
	return false, errSel("IncompatiblePolicyType()")
}

func (c *Context) authorizedSimple(q authQuery) (bool, error) {
	if b := builtinPolicy(q.id); b != builtinNone {
		return b == builtinAllow, nil
	}
	data, err := c.policyData(q.id)
	if err != nil {
		return false, err
	}
	return c.authorizedSimpleInner(q, data)
}

func (c *Context) authorizedCached(q authQuery, data policyData) (bool, error) {
	if b := builtinPolicy(q.id); b != builtinNone {
		return b == builtinAllow, nil
	}
	return c.authorizedSimpleInner(q, data)
}

type compoundPolicy struct {
	sender, recipient, mintRecipient uint64
}

func (c *Context) compoundPolicy(id uint64) (compoundPolicy, error) {
	w, err := c.sload(TIP403RegistryAddress, c.policyRecordSlot(id).plus(1))
	if err != nil {
		return compoundPolicy{}, err
	}
	return compoundPolicy{sender: fU64At0.u64(w), recipient: fU64At8.u64(w), mintRecipient: fU64At16.u64(w)}, nil
}

func (c *Context) isAuthorizedAs(q authQuery, role authRole) (bool, error) {
	if c.at(hardfork.T6) && q.user == ReceivePolicyGuardAddress {
		return true, nil
	}
	if b := builtinPolicy(q.id); b != builtinNone {
		return b == builtinAllow, nil
	}
	data, err := c.policyData(q.id)
	if err != nil {
		return false, err
	}
	if data.policyType != policyCompound {
		return c.authorizedSimpleInner(q, data)
	}
	cp, err := c.compoundPolicy(q.id)
	if err != nil {
		return false, err
	}
	if !c.at(hardfork.T2) {
		role = roleTransfer
	}
	if role == roleSender {
		return c.authorizedSimple(authQuery{id: cp.sender, user: q.user})
	}
	if role == roleRecipient {
		return c.authorizedSimple(authQuery{id: cp.recipient, user: q.user})
	}
	if role == roleMintRecipient {
		return c.authorizedSimple(authQuery{id: cp.mintRecipient, user: q.user})
	}
	sender, err := c.authorizedSimple(authQuery{id: cp.sender, user: q.user})
	if err != nil {
		return false, err
	}
	if c.at(hardfork.T2) && !sender {
		return false, nil
	}
	recipient, err := c.authorizedSimple(authQuery{id: cp.recipient, user: q.user})
	if err != nil {
		return false, err
	}
	return sender && recipient, nil
}

func (c *Context) policyExists(id uint64) (bool, error) {
	if builtinPolicy(id) != builtinNone {
		return true, nil
	}
	counter, err := c.policyCounter()
	if err != nil {
		return false, err
	}
	return id < counter, nil
}

func (c *Context) tokenTransferPolicySlot(token Address) slot {
	return c.mapAddr(token, slot403TokenTransfers)
}

type registeredPolicy struct {
	id  uint64
	set bool
}

func (c *Context) registeredTokenPolicy(token Address) (registeredPolicy, error) {
	w, err := c.sload(TIP403RegistryAddress, c.tokenTransferPolicySlot(token))
	if err != nil {
		return registeredPolicy{}, err
	}
	return registeredPolicy{id: fU64At0.u64(w), set: !fFlagAt8.of(w).IsZero()}, nil
}

func (c *Context) setTokenTransferPolicy(token Address, id uint64) error {
	w := pack(Word{}).withU64(fU64At0, id).with(fFlagAt8, flag(true).word()).w
	return c.storePackedGroup(TIP403RegistryAddress, c.tokenTransferPolicySlot(token), w, fU64At0, fFlagAt8)
}

type receiveConfig struct {
	has          bool
	senderPolicy uint64
	senderType   uint8
	tokenFilter  uint64
	tokenType    uint8
	recoveryMode uint8
}

func decodeReceiveConfig(w Word) receiveConfig {
	return receiveConfig{
		has:          !fRcvHas.of(w).IsZero(),
		senderPolicy: fRcvSender.u64(w),
		senderType:   uint8(fRcvSendType.u64(w)),
		tokenFilter:  fRcvToken.u64(w),
		tokenType:    uint8(fRcvTokType.u64(w)),
		recoveryMode: uint8(fRcvRecovery.u64(w)),
	}
}

const recoveryModeMax = 2

func (c *Context) readReceiveConfig(account Address) (receiveConfig, error) {
	w, err := c.sload(TIP403RegistryAddress, c.receivePolicySlot(account))
	if err != nil {
		return receiveConfig{}, err
	}
	cfg := decodeReceiveConfig(w)
	if cfg.recoveryMode > recoveryModeMax {
		return receiveConfig{}, revert(abiError(selPanic, wordOfU64(panicEnumConversion)))
	}
	return cfg, nil
}

func (c *Context) receivePolicySlot(account Address) slot { return c.mapAddr(account, slot403Receive) }

type receiveBlock struct {
	blocked  bool
	reason   uint8
	recovery Address
}

const (
	blockedTokenFilter   uint8 = 1
	blockedReceivePolicy uint8 = 2
)

const (
	recoveryOriginator uint8 = 0
	recoveryReceiver   uint8 = 1
)

func (c *Context) receiveRecovery(account Address, mode uint8) (Address, error) {
	if mode == recoveryOriginator {
		return Address{}, nil
	}
	if mode == recoveryReceiver {
		return account, nil
	}
	return c.loadAddress(TIP403RegistryAddress, c.receivePolicySlot(account).plus(1))
}

type inbound struct {
	token    Address
	sender   Address
	receiver Address
}

func (c *Context) checkReceivePolicy(in inbound) (receiveBlock, error) {
	cfg, err := c.readReceiveConfig(in.receiver)
	if err != nil {
		return receiveBlock{}, err
	}
	if !cfg.has {
		return receiveBlock{}, nil
	}
	ok, err := c.authorizedCached(authQuery{id: cfg.tokenFilter, user: in.token}, policyData{policyType: cfg.tokenType})
	if err != nil {
		return receiveBlock{}, err
	}
	reason := blockedTokenFilter
	if ok {
		ok, err = c.authorizedCached(authQuery{id: cfg.senderPolicy, user: in.sender}, policyData{policyType: cfg.senderType})
		if err != nil {
			return receiveBlock{}, err
		}
		reason = blockedReceivePolicy
	}
	if ok {
		return receiveBlock{}, nil
	}
	rec, err := c.receiveRecovery(in.receiver, cfg.recoveryMode)
	return receiveBlock{blocked: true, reason: reason, recovery: rec}, err
}
