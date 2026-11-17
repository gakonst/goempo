package precompiles

import (
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	permitTypehash       = topic("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)")
	eip712DomainTypehash = topic("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)")
	versionHash          = topic("1")
	retTrue              = encodeWords(flag(true).hash())
)

func (c *Context) cur() Token { return TIP20(c, c.self) }

func retBool(b flag) []byte     { return encodeWords(b.hash()) }
func retU64(v uint64) []byte    { return encodeWords(wordOfU64(v)) }
func retString(s []byte) []byte { return newEncoder(1).bytes(s).out() }

func memoOf(a []abiValue, i int) *Hash {
	h := a[i].bytes32()
	return &h
}

func stringView(s slot) handler {
	return func(c *Context, a []abiValue) ([]byte, error) {
		v, err := c.loadString(c.self, s)
		return retString(v), err
	}
}

func wordView(s slot) handler {
	return func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(c.self, s)
		return retWord(w), err
	}
}

func constView(h Hash) handler {
	return func(c *Context, a []abiValue) ([]byte, error) { return encodeWords(h), nil }
}

func transferCall(a []abiValue, memoAt int) func(c *Context) transferReq {
	return func(c *Context) transferReq {
		req := transferReq{from: c.Caller, to: a[0].addr(), amount: a[1].u256()}
		if memoAt >= 0 {
			req.memo = memoOf(a, memoAt)
		}
		return req
	}
}

func transferFromCall(c *Context, a []abiValue) transferReq {
	spender := c.Caller
	req := transferReq{spender: &spender, from: a[0].addr(), to: a[1].addr(), amount: a[2].u256()}
	if len(a) > 3 {
		req.memo = memoOf(a, 3)
	}
	return req
}

func mintCall(c *Context, a []abiValue) mintReq {
	req := mintReq{sender: c.Caller, to: a[0].addr(), amount: a[1].u256()}
	if len(a) > 2 {
		req.memo = memoOf(a, 2)
	}
	return req
}

var tip20Table = newTable(
	view("name()", nil, stringView(slotName)),
	view("symbol()", nil, stringView(slotSymbol)),
	view("decimals()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retU64(tip20Decimals), nil }),
	view("currency()", nil, stringView(slotCurrency)),
	view("totalSupply()", nil, wordView(slotTotalSupply)),
	view("supplyCap()", nil, wordView(slotSupplyCap)),
	view("transferPolicyId()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.cur().transferPolicy()
		return retU64(id), err
	}),
	view("paused()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		p, err := c.cur().paused()
		return retBool(flag(p)), err
	}),
	view("logoURI()", nil, stringView(slotLogoURI)).from(hardfork.T5),
	view("balanceOf(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		b, err := c.cur().balance(a[0].addr())
		return retWord(b), err
	}),
	view("allowance(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		v, err := c.cur().allowance(pair{owner: a[0].addr(), spender: a[1].addr()})
		return retWord(v), err
	}),
	view("quoteToken()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		q, err := c.cur().quoteToken()
		return retAddr(q), err
	}),
	view("nextQuoteToken()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		q, err := c.cur().nextQuoteToken()
		return retAddr(q), err
	}),
	view("PAUSE_ROLE()", nil, constView(rolePause)),
	view("UNPAUSE_ROLE()", nil, constView(roleUnpause)),
	view("ISSUER_ROLE()", nil, constView(roleIssuer)),
	view("BURN_BLOCKED_ROLE()", nil, constView(roleBurnBlocked)),
	view("BURN_AT_ROLE()", nil, constView(roleBurnAt)).from(hardfork.T12),
	mutate("transferFrom(address,address,uint256)", []abiType{tAddress, tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return retTrue, c.cur().transfer(transferFromCall(c, a))
	}),
	mutate("transfer(address,uint256)", []abiType{tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return retTrue, c.cur().transfer(transferCall(a, -1)(c))
	}),
	mutate("approve(address,uint256)", []abiType{tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return retTrue, c.cur().approve(pair{owner: c.Caller, spender: a[0].addr()}, a[1].u256())
	}),
	mutate("changeTransferPolicyId(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().changeTransferPolicy(c.Caller, a[0].u64())
	}),
	mutate("setSupplyCap(uint256)", []abiType{tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().setSupplyCap(c.Caller, a[0].u256())
	}),
	mutate("setLogoURI(string)", []abiType{tString}, func(c *Context, a []abiValue) ([]byte, error) {
		t := c.cur()
		if err := t.checkRole(c.Caller, roleDefaultAdmin); err != nil {
			return nil, err
		}
		return nil, t.writeLogoURI(c.Caller, a[0].data)
	}).from(hardfork.T5),
	mutate("pause()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().setPaused(c.Caller, true)
	}),
	mutate("unpause()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().setPaused(c.Caller, false)
	}),
	mutate("setNextQuoteToken(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().setNextQuoteToken(hop{from: c.Caller, to: a[0].addr()})
	}),
	mutate("completeQuoteTokenUpdate()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().completeQuoteTokenUpdate(c.Caller)
	}),
	mutate("mint(address,uint256)", []abiType{tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().mint(mintCall(c, a))
	}),
	mutate("mintWithMemo(address,uint256,bytes32)", []abiType{tAddress, tU256, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().mint(mintCall(c, a))
	}),
	mutate("burn(uint256)", []abiType{tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().burn(c.Caller, a[0].u256(), nil)
	}),
	mutate("burnWithMemo(uint256,bytes32)", []abiType{tU256, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().burn(c.Caller, a[0].u256(), memoOf(a, 1))
	}),
	mutate("burnBlocked(address,uint256)", []abiType{tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().burnBlocked(burnReq{sender: c.Caller, owner: a[0].addr(), amount: a[1].u256()})
	}),
	mutate("burnAt(address,uint256)", []abiType{tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().burnAt(burnReq{sender: c.Caller, owner: a[0].addr(), amount: a[1].u256()})
	}).from(hardfork.T12),
	mutate("transferWithMemo(address,uint256,bytes32)", []abiType{tAddress, tU256, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().transfer(transferCall(a, 2)(c))
	}),
	mutate("transferFromWithMemo(address,address,uint256,bytes32)", []abiType{tAddress, tAddress, tU256, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return retTrue, c.cur().transfer(transferFromCall(c, a))
	}),
	mutate("distributeReward(uint256)", []abiType{tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().distributeReward(c.Caller, a[0].u256())
	}),
	mutate("setRewardRecipient(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().setRewardRecipient(rewardDelegation{holder: c.Caller, recipient: a[0].addr()})
	}),
	mutate("claimRewards()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		amount, err := c.cur().claimRewards(c.Caller)
		return retWord(amount), err
	}),
	view("globalRewardPerToken()", nil, wordView(slotGlobalRPT)),
	view("optedInSupply()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		v, err := c.cur().optedInSupply()
		return retWord(v), err
	}),
	view("userRewardInfo(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		info, err := c.cur().readRewardInfo(a[0].addr())
		return encodeWords(addressWord(info.recipient), toHash(info.rewardPerToken), toHash(info.balance)), err
	}),
	view("getPendingRewards(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		v, err := c.cur().pendingRewards(a[0].addr())
		return retWord(v), err
	}),
	mutate("permit(address,address,uint256,uint256,uint8,bytes32,bytes32)", []abiType{tAddress, tAddress, tU256, tU256, tU8, tB32, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().permit(a)
	}).from(hardfork.T2),
	view("nonces(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(c.self, c.mapAddr(a[0].addr(), slotPermitNonces))
		return retWord(w), err
	}).from(hardfork.T2),
	view("DOMAIN_SEPARATOR()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		h, err := c.cur().domainSeparator()
		return encodeWords(h), err
	}).from(hardfork.T2),
	view("hasRole(address,bytes32)", []abiType{tAddress, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.cur().hasRole(a[0].addr(), a[1].bytes32())
		return retBool(flag(ok)), err
	}),
	view("getRoleAdmin(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		h, err := c.cur().roleAdmin(a[0].bytes32())
		return encodeWords(h), err
	}),
	mutate("grantRole(bytes32,address)", []abiType{tB32, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().changeRole(roleChange{sender: c.Caller, role: a[0].bytes32(), account: a[1].addr(), granted: true})
	}),
	mutate("revokeRole(bytes32,address)", []abiType{tB32, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().changeRole(roleChange{sender: c.Caller, role: a[0].bytes32(), account: a[1].addr()})
	}),
	mutate("renounceRole(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().renounceRole(c.Caller, a[0].bytes32())
	}),
	mutate("setRoleAdmin(bytes32,bytes32)", []abiType{tB32, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.cur().changeRoleAdmin(c.Caller, roleAdminChange{role: a[0].bytes32(), admin: a[1].bytes32()})
	}),
)

type roleChange struct {
	sender  Address
	role    Hash
	account Address
	granted bool
}

func (t Token) emitRoleMembership(r roleChange) error {
	return t.c.emit(t.addr, []Hash{evRoleMembershipUpdated, r.role, addressWord(r.account), addressWord(r.sender)}, retBool(flag(r.granted)))
}

func (t Token) setRole(r roleChange) error {
	return t.store(t.roleSlot(r.account, r.role), flag(r.granted).word())
}

func (t Token) changeRole(r roleChange) error {
	admin, err := t.roleAdmin(r.role)
	if err != nil {
		return err
	}
	if err := t.checkRole(r.sender, admin); err != nil {
		return err
	}
	if err := t.setRole(r); err != nil {
		return err
	}
	return t.emitRoleMembership(r)
}

func (t Token) renounceRole(sender Address, role Hash) error {
	if err := t.checkRole(sender, role); err != nil {
		return err
	}
	r := roleChange{sender: sender, role: role, account: sender}
	if err := t.setRole(r); err != nil {
		return err
	}
	return t.emitRoleMembership(r)
}

type roleAdminChange struct {
	role  Hash
	admin Hash
}

func (t Token) changeRoleAdmin(sender Address, ch roleAdminChange) error {
	current, err := t.roleAdmin(ch.role)
	if err != nil {
		return err
	}
	if err := t.checkRole(sender, current); err != nil {
		return err
	}
	if err := t.store(t.c.mapKey(ch.role, slotRoleAdmins), toWord(ch.admin)); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evRoleAdminUpdated, ch.role, ch.admin, addressWord(sender)}, nil)
}

func (t Token) changeTransferPolicy(sender Address, id uint64) error {
	if err := t.checkRole(sender, roleDefaultAdmin); err != nil {
		return err
	}
	ok, err := t.c.policyExists(id)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("InvalidTransferPolicyId()")
	}
	if t.c.at(hardfork.T9) {
		err = t.c.setTokenTransferPolicy(t.addr, id)
	} else {
		err = t.c.storeField(t.addr, slotTransferPolicy, fTransferPolicy, u256.FromUint64(id))
	}
	if err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evTransferPolicyUpdate, addressWord(sender), wordOfU64(id)}, nil)
}

func (t Token) setSupplyCap(sender Address, cap Word) error {
	if err := t.checkRole(sender, roleDefaultAdmin); err != nil {
		return err
	}
	supply, err := t.totalSupply()
	if err != nil {
		return err
	}
	if cap.Lt(supply) {
		return errSel("InvalidSupplyCap()")
	}
	if cap.Gt(u128Max) {
		return errSel("SupplyCapExceeded()")
	}
	if err := t.store(slotSupplyCap, cap); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evSupplyCapUpdate, addressWord(sender), toHash(cap)}, nil)
}

func (t Token) writeLogoURI(updater Address, uri []byte) error {
	if err := validLogoURI(uri); err != nil {
		return err
	}
	if err := t.c.storeString(t.addr, slotLogoURI, uri); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evLogoURIUpdated, addressWord(updater)}, retString(uri))
}

func (t Token) setPaused(sender Address, paused flag) error {
	role := rolePause
	if !paused {
		role = roleUnpause
	}
	if err := t.checkRole(sender, role); err != nil {
		return err
	}
	if err := t.store(slotPaused, paused.word()); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evPauseStateUpdate, addressWord(sender)}, retBool(paused))
}

func (t Token) setNextQuoteToken(h hop) error {
	sender, next := h.from, h.to
	if err := t.checkRole(sender, roleDefaultAdmin); err != nil {
		return err
	}
	if t.addr == PathUSDAddress {
		return errSel("InvalidQuoteToken()")
	}
	ok, err := IsTIP20Deployed(t.c, next)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("InvalidQuoteToken()")
	}
	cur, err := t.currency()
	if err != nil {
		return err
	}
	if string(cur) == string(usdCurrency) {
		qc, err := TIP20(t.c, next).currency()
		if err != nil {
			return err
		}
		if string(qc) != string(usdCurrency) {
			return errSel("InvalidQuoteToken()")
		}
	}
	if err := t.c.storeField(t.addr, slotNextQuoteToken, fNextQuote, toWord(addressWord(next))); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evNextQuoteTokenSet, addressWord(sender), addressWord(next)}, nil)
}

const maxQuoteChain = 1 << 16

func (t Token) completeQuoteTokenUpdate(sender Address) error {
	if err := t.checkRole(sender, roleDefaultAdmin); err != nil {
		return err
	}
	next, err := t.nextQuoteToken()
	if err != nil {
		return err
	}
	current := next
	steps := 0
	for ; steps < maxQuoteChain; steps++ {
		if current == PathUSDAddress {
			break
		}
		if current == t.addr {
			return errSel("InvalidQuoteToken()")
		}
		if !IsTIP20(current) {
			return errSel("InvalidToken()")
		}
		if current, err = TIP20(t.c, current).quoteToken(); err != nil {
			return err
		}
	}
	if steps == maxQuoteChain {
		return errSel("InvalidQuoteToken()")
	}
	if err := t.c.storeAddress(t.addr, slotQuoteToken, next); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evQuoteTokenUpdate, addressWord(sender), addressWord(next)}, nil)
}

func (t Token) domainSeparator() (Hash, error) {
	name, err := t.c.loadString(t.addr, slotName)
	if err != nil {
		return Hash{}, err
	}
	if err := t.c.chargeKeccak(len(name)); err != nil {
		return Hash{}, err
	}
	nameHash := t.c.keccak(name)
	enc := encodeWords(eip712DomainTypehash, nameHash, versionHash, toHash(t.c.ChainID), addressWord(t.addr))
	if err := t.c.chargeKeccak(len(enc)); err != nil {
		return Hash{}, err
	}
	return t.c.keccak(enc), nil
}

const gasEcrecover uint64 = 3_000

func (t Token) permitDigest(a []abiValue, nonce Word) (Hash, error) {
	enc := encodeWords(permitTypehash, addressWord(a[0].addr()), addressWord(a[1].addr()), a[2].word, toHash(nonce), a[3].word)
	if err := t.c.chargeKeccak(len(enc)); err != nil {
		return Hash{}, err
	}
	structHash := t.c.keccak(enc)
	domain, err := t.domainSeparator()
	if err != nil {
		return Hash{}, err
	}
	msg := make([]byte, 0, 66)
	msg = append(msg, 0x19, 0x01)
	msg = append(msg, domain[:]...)
	msg = append(msg, structHash[:]...)
	if err := t.c.chargeKeccak(len(msg)); err != nil {
		return Hash{}, err
	}
	return t.c.keccak(msg), nil
}

func (t Token) permit(a []abiValue) error {
	owner, spender, value, deadline := a[0].addr(), a[1].addr(), a[2].u256(), a[3].u256()
	if u256.FromUint64(t.c.timestamp()).Gt(deadline) {
		return errSel("PermitExpired()")
	}
	nonceSlot := t.c.mapAddr(owner, slotPermitNonces)
	nonce, err := t.load(nonceSlot)
	if err != nil {
		return err
	}
	digest, err := t.permitDigest(a, nonce)
	if err != nil {
		return err
	}
	if err := t.c.charge(gasEcrecover); err != nil {
		return err
	}
	out, ok := secp256k1.Ecrecover(encodeWords(digest, wordOfU64(uint64(a[4].u8())), a[5].bytes32(), a[6].bytes32()))
	if !ok || wordAddress(Hash(out)) != owner {
		return errSel("InvalidSignature()")
	}
	retired, err := t.c.isRetiredRoot(owner)
	if err != nil {
		return err
	}
	if retired {
		return revert(abiError(selector("RootKeyRetired(address)"), addressWord(owner)))
	}
	next, err := num(nonce).add(u256.One())
	if err != nil {
		return err
	}
	if err := t.store(nonceSlot, next); err != nil {
		return err
	}
	if err := t.store(t.allowanceSlot(pair{owner: owner, spender: spender}), value); err != nil {
		return err
	}
	return t.c.emit(t.addr, []Hash{evApproval, addressWord(owner), addressWord(spender)}, retWord(value))
}
