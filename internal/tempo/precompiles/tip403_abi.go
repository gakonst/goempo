package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const policyTypeInvalid uint8 = 255

func (c *Context) simplePolicyType(t uint8) (uint8, error) {
	if t == policyWhitelist || t == policyBlacklist {
		return t, nil
	}
	if c.at(hardfork.T2) {
		return 0, errSel("IncompatiblePolicyType()")
	}
	return policyTypeInvalid, nil
}

func (c *Context) nextPolicyID() (uint64, error) {
	id, err := c.policyCounter()
	if err != nil {
		return 0, err
	}
	if id == ^uint64(0) {
		return 0, underOverflow()
	}
	return id, c.sstore(TIP403RegistryAddress, slot403Counter, u256.FromUint64(id+1))
}

func (c *Context) writePolicyData(id uint64, p policyData) error {
	return c.storePackedGroup(TIP403RegistryAddress, c.policyRecordSlot(id), encodePolicyData(p))
}

type policyEvent struct {
	id      uint64
	updater Address
}

func (c *Context) emitPolicyCreated(e policyEvent, t uint8) error {
	return c.emit(TIP403RegistryAddress, []Hash{evPolicyCreated, wordOfU64(e.id), addressWord(e.updater)}, encodeWords(wordOfU64(uint64(t))))
}

func (c *Context) emitPolicyAdmin(e policyEvent, admin Address) error {
	return c.emit(TIP403RegistryAddress, []Hash{evPolicyAdminUpdated, wordOfU64(e.id), addressWord(e.updater), addressWord(admin)}, nil)
}

type newPolicy struct {
	sender   Address
	admin    Address
	kind     uint8
	accounts []Address
}

func (c *Context) createPolicy(p newPolicy) (uint64, error) {
	t, err := c.simplePolicyType(p.kind)
	if err != nil {
		return 0, err
	}
	if c.at(hardfork.T3) {
		for _, a := range p.accounts {
			if IsVirtual(a) {
				return 0, errSel("VirtualAddressNotAllowed()")
			}
		}
	}
	id, err := c.nextPolicyID()
	if err != nil {
		return 0, err
	}
	if err := c.writePolicyData(id, policyData{policyType: t, admin: p.admin}); err != nil {
		return 0, err
	}
	e := policyEvent{id: id, updater: p.sender}
	for _, a := range p.accounts {
		if err := c.sstore(TIP403RegistryAddress, c.mapAddr(a, c.mapU64(id, slot403Set)), flag(true).word()); err != nil {
			return 0, err
		}
		if err := c.emitSetUpdate(setUpdate{event: e, account: a, value: true, kind: p.kind}); err != nil {
			return 0, err
		}
	}
	if err := c.emitPolicyCreated(e, t); err != nil {
		return 0, err
	}
	return id, c.emitPolicyAdmin(e, p.admin)
}

type setUpdate struct {
	event   policyEvent
	account Address
	value   flag
	kind    uint8
}

func (c *Context) emitSetUpdate(u setUpdate) error {
	topic := evWhitelistUpdated
	if u.kind == policyBlacklist {
		topic = evBlacklistUpdated
	} else if u.kind != policyWhitelist {
		return errSel("IncompatiblePolicyType()")
	}
	return c.emit(TIP403RegistryAddress, []Hash{topic, wordOfU64(u.event.id), addressWord(u.event.updater), addressWord(u.account)}, encodeWords(u.value.hash()))
}

func (c *Context) adminPolicyData(e policyEvent) (policyData, error) {
	data, err := c.policyData(e.id)
	if err != nil {
		return policyData{}, err
	}
	if data.admin != e.updater {
		return policyData{}, errSel("Unauthorized()")
	}
	return data, nil
}

func (c *Context) setPolicyAdmin(e policyEvent, admin Address) error {
	data, err := c.adminPolicyData(e)
	if err != nil {
		return err
	}
	data.admin = admin
	if err := c.writePolicyData(e.id, data); err != nil {
		return err
	}
	return c.emitPolicyAdmin(e, admin)
}

func (c *Context) modifyPolicySet(u setUpdate) error {
	if c.at(hardfork.T3) && IsVirtual(u.account) {
		return errSel("VirtualAddressNotAllowed()")
	}
	data, err := c.adminPolicyData(u.event)
	if err != nil {
		return err
	}
	t, err := c.checkedPolicyType(data)
	if err != nil {
		return err
	}
	if t != u.kind {
		return errSel("IncompatiblePolicyType()")
	}
	if err := c.sstore(TIP403RegistryAddress, c.mapAddr(u.account, c.mapU64(u.event.id, slot403Set)), u.value.word()); err != nil {
		return err
	}
	return c.emitSetUpdate(u)
}

func (c *Context) validateSimplePolicy(id uint64) (uint8, error) {
	if builtinPolicy(id) != builtinNone {
		return uint8(id), nil
	}
	counter, err := c.policyCounter()
	if err != nil {
		return 0, err
	}
	if id >= counter {
		return 0, errSel("PolicyNotFound()")
	}
	data, err := c.policyData(id)
	if err != nil {
		return 0, err
	}
	if data.policyType > policyBlacklist {
		return data.policyType, errSel("PolicyNotSimple()")
	}
	return data.policyType, nil
}

func (c *Context) createCompoundPolicy(sender Address, cp compoundPolicy) (uint64, error) {
	for _, id := range []uint64{cp.sender, cp.recipient, cp.mintRecipient} {
		if _, err := c.validateSimplePolicy(id); err != nil {
			return 0, err
		}
	}
	id, err := c.nextPolicyID()
	if err != nil {
		return 0, err
	}
	if err := c.writePolicyData(id, policyData{policyType: policyCompound}); err != nil {
		return 0, err
	}
	w := pack(Word{}).withU64(fU64At0, cp.sender).withU64(fU64At8, cp.recipient).withU64(fU64At16, cp.mintRecipient).w
	if err := c.storePackedGroup(TIP403RegistryAddress, c.policyRecordSlot(id).plus(1), w); err != nil {
		return 0, err
	}
	data := encodeWords(wordOfU64(cp.sender), wordOfU64(cp.recipient), wordOfU64(cp.mintRecipient))
	return id, c.emit(TIP403RegistryAddress, []Hash{evCompoundPolicyCreated, wordOfU64(id), addressWord(sender)}, data)
}

type receivePolicyReq struct {
	sender       Address
	senderPolicy uint64
	tokenFilter  uint64
	recovery     Address
}

func (c *Context) validateReceivePolicyID(id uint64) (uint8, error) {
	t, err := c.validateSimplePolicy(id)
	if err != nil && isRevert(err) && t > policyBlacklist {
		return 0, errSel("InvalidReceivePolicyType()")
	}
	return t, err
}

func (c *Context) setReceivePolicy(r receivePolicyReq) error {
	if IsVirtual(r.sender) {
		return errSel("VirtualAddressNotAllowed()")
	}
	mode, stored := recoveryOriginator, Address{}
	if r.recovery == r.sender && r.recovery != (Address{}) {
		mode = recoveryReceiver
	} else if r.recovery != (Address{}) {
		mode, stored = 2, r.recovery
	}
	if IsPrecompile(r.recovery, c.Fork) || IsVirtual(r.recovery) {
		return errSel("InvalidRecoveryAuthority()")
	}
	st, err := c.validateReceivePolicyID(r.senderPolicy)
	if err != nil {
		return err
	}
	tt, err := c.validateReceivePolicyID(r.tokenFilter)
	if err != nil {
		return err
	}
	w := pack(Word{}).with(fRcvHas, flag(true).word()).withU64(fRcvSender, r.senderPolicy).withU64(fRcvSendType, uint64(st)).
		withU64(fRcvToken, r.tokenFilter).withU64(fRcvTokType, uint64(tt)).withU64(fRcvRecovery, uint64(mode)).w
	base := c.receivePolicySlot(r.sender)
	if err := c.storePackedGroup(TIP403RegistryAddress, base, w); err != nil {
		return err
	}
	if err := c.storeAddress(TIP403RegistryAddress, base.plus(1), stored); err != nil {
		return err
	}
	data := encodeWords(wordOfU64(r.senderPolicy), wordOfU64(r.tokenFilter), addressWord(r.recovery))
	return c.emit(TIP403RegistryAddress, []Hash{evReceivePolicyUpdated, addressWord(r.sender)}, data)
}

func (c *Context) migrateTransferPolicyIDs(tokens []Hash) (uint64, error) {
	migrated := uint64(0)
	for _, h := range tokens {
		token := wordAddress(h)
		ok, err := IsTIP20Deployed(c, token)
		if err != nil {
			return 0, err
		}
		if !ok {
			continue
		}
		reg, err := c.registeredTokenPolicy(token)
		if err != nil {
			return 0, err
		}
		if reg.set {
			continue
		}
		legacy, err := c.loadField(token, slotTransferPolicy, fTransferPolicy)
		if err != nil {
			return 0, err
		}
		if err := c.setTokenTransferPolicy(token, legacy.Uint64()); err != nil {
			return 0, err
		}
		if err := c.storeField(token, slotTransferPolicy, fTransferPolicy, Word{}); err != nil {
			return 0, err
		}
		migrated++
	}
	return migrated, nil
}

func authView(role authRole) handler {
	return func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.isAuthorizedAs(authQuery{id: a[0].u64(), user: a[1].addr()}, role)
		return retBool(flag(ok)), err
	}
}

func addressItems(v abiValue) []Address {
	out := make([]Address, len(v.items))
	for i, row := range v.items {
		out[i] = wordAddress(row[0])
	}
	return out
}

var tip403Table = register(TIP403RegistryAddress, newTable(
	view("policyIdCounter()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		n, err := c.policyCounter()
		return retU64(n), err
	}),
	view("policyExists(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.policyExists(a[0].u64())
		return retBool(flag(ok)), err
	}),
	view("tokenTransferPolicyId(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		token := a[0].addr()
		ok, err := IsTIP20Deployed(c, token)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errSel("InvalidToken()")
		}
		reg, err := c.registeredTokenPolicy(token)
		if err != nil || reg.set {
			return encodeWords(flag(reg.set).hash(), wordOfU64(reg.id)), err
		}
		legacy, err := c.loadField(token, slotTransferPolicy, fTransferPolicy)
		return encodeWords(flag(false).hash(), toHash(legacy)), err
	}).from(hardfork.T9),
	view("policyData(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		id := a[0].u64()
		if c.at(hardfork.T2) && builtinPolicy(id) != builtinNone {
			return encodeWords(wordOfU64(id), Hash{}), nil
		}
		if !c.at(hardfork.T2) {
			ok, err := c.policyExists(id)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errSel("PolicyNotFound()")
			}
		}
		data, err := c.policyData(id)
		if err != nil {
			return nil, err
		}
		t, err := c.checkedPolicyType(data)
		return encodeWords(wordOfU64(uint64(t)), addressWord(data.admin)), err
	}),
	view("isAuthorized(uint64,address)", []abiType{tU64, tAddress}, authView(roleTransfer)),
	view("isAuthorizedSender(uint64,address)", []abiType{tU64, tAddress}, authView(roleSender)).from(hardfork.T2),
	view("isAuthorizedRecipient(uint64,address)", []abiType{tU64, tAddress}, authView(roleRecipient)).from(hardfork.T2),
	view("isAuthorizedMintRecipient(uint64,address)", []abiType{tU64, tAddress}, authView(roleMintRecipient)).from(hardfork.T2),
	view("compoundPolicyData(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		id := a[0].u64()
		data, err := c.policyData(id)
		if err != nil {
			return nil, err
		}
		if data.policyType != policyCompound {
			ok, err := c.policyExists(id)
			if err != nil {
				return nil, err
			}
			if ok {
				return nil, errSel("IncompatiblePolicyType()")
			}
			return nil, errSel("PolicyNotFound()")
		}
		cp, err := c.compoundPolicy(id)
		return encodeWords(wordOfU64(cp.sender), wordOfU64(cp.recipient), wordOfU64(cp.mintRecipient)), err
	}).from(hardfork.T2),
	view("receivePolicy(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(TIP403RegistryAddress, c.receivePolicySlot(a[0].addr()))
		if err != nil {
			return nil, err
		}
		cfg := decodeReceiveConfig(w)
		rec, err := c.receiveRecovery(a[0].addr(), cfg.recoveryMode)
		return encodeWords(flag(cfg.has).hash(), wordOfU64(cfg.senderPolicy), wordOfU64(uint64(cfg.senderType)),
			wordOfU64(cfg.tokenFilter), wordOfU64(uint64(cfg.tokenType)), addressWord(rec)), err
	}).from(hardfork.T6),
	view("validateReceivePolicy(address,address,address)", []abiType{tAddress, tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		b, err := c.checkReceivePolicy(inbound{token: a[0].addr(), sender: a[1].addr(), receiver: a[2].addr()})
		reason := uint64(0)
		if b.blocked {
			reason = uint64(b.reason)
		}
		return encodeWords(flag(!b.blocked).hash(), wordOfU64(reason)), err
	}).from(hardfork.T6),
	mutate("setReceivePolicy(uint64,uint64,address)", []abiType{tU64, tU64, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.setReceivePolicy(receivePolicyReq{sender: c.Caller, senderPolicy: a[0].u64(), tokenFilter: a[1].u64(), recovery: a[2].addr()})
	}).from(hardfork.T6),
	mutate("migrateTransferPolicyIds(address[])", []abiType{tScalarArray(tAddress)}, func(c *Context, a []abiValue) ([]byte, error) {
		rows := make([]Hash, len(a[0].items))
		for i, row := range a[0].items {
			rows[i] = row[0]
		}
		n, err := c.migrateTransferPolicyIDs(rows)
		return retU64(n), err
	}).from(hardfork.T9),
	mutate("createPolicy(address,uint8)", []abiType{tAddress, tPolType}, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.createPolicy(newPolicy{sender: c.Caller, admin: a[0].addr(), kind: a[1].u8()})
		return retU64(id), err
	}),
	mutate("createPolicyWithAccounts(address,uint8,address[])", []abiType{tAddress, tPolType, tScalarArray(tAddress)}, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.createPolicy(newPolicy{sender: c.Caller, admin: a[0].addr(), kind: a[1].u8(), accounts: addressItems(a[2])})
		return retU64(id), err
	}),
	mutate("setPolicyAdmin(uint64,address)", []abiType{tU64, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.setPolicyAdmin(policyEvent{id: a[0].u64(), updater: c.Caller}, a[1].addr())
	}),
	mutate("modifyPolicyWhitelist(uint64,address,bool)", []abiType{tU64, tAddress, tBool}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.modifyPolicySet(setUpdate{event: policyEvent{id: a[0].u64(), updater: c.Caller}, account: a[1].addr(), value: flag(a[2].boolean()), kind: policyWhitelist})
	}),
	mutate("modifyPolicyBlacklist(uint64,address,bool)", []abiType{tU64, tAddress, tBool}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.modifyPolicySet(setUpdate{event: policyEvent{id: a[0].u64(), updater: c.Caller}, account: a[1].addr(), value: flag(a[2].boolean()), kind: policyBlacklist})
	}),
	mutate("createCompoundPolicy(uint64,uint64,uint64)", []abiType{tU64, tU64, tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.createCompoundPolicy(c.Caller, compoundPolicy{sender: a[0].u64(), recipient: a[1].u64(), mintRecipient: a[2].u64()})
		return retU64(id), err
	}).from(hardfork.T2),
))
