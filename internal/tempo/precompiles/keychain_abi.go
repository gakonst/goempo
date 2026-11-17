package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

func (s storedSet) remove(v Hash) (bool, error) {
	posW, err := s.c.sload(s.addr, s.positionSlot(v))
	if err != nil || posW.IsZero() {
		return false, err
	}
	n, err := s.length()
	if err != nil {
		return false, err
	}
	if n == 0 || !posW.IsUint64() || posW.Uint64() > uint64(n) {
		return false, underOverflow()
	}
	last := n - 1
	index := int(posW.Uint64() - 1)
	if index != last {
		lastW, err := s.c.loadField(s.addr, s.elemSlot(last), s.elemField(last))
		if err != nil {
			return false, err
		}
		if err := s.c.sstore(s.addr, s.positionSlot(toHash(lastW)), posW); err != nil {
			return false, err
		}
		if err := s.writeElem(index, toHash(lastW)); err != nil {
			return false, err
		}
	}
	if err := s.writeElem(last, Hash{}); err != nil {
		return false, err
	}
	if err := s.c.sstore(s.addr, s.base, u256.FromUint64(uint64(last))); err != nil {
		return false, err
	}
	return true, s.c.sstore(s.addr, s.positionSlot(v), Word{})
}

func (c *Context) ensureNotWitnessBurned(account Address, witness Hash) error {
	burned, err := c.loadBool(AccountKeychainAddress, c.witnessSlot(account, witness))
	if err != nil {
		return err
	}
	if burned {
		return errSel("KeyAuthorizationWitnessAlreadyBurned()")
	}
	return nil
}

func (c *Context) burnWitness(account Address, witness Hash) error {
	if err := c.ensureAdminCaller(account); err != nil {
		return err
	}
	if err := c.ensureNotWitnessBurned(account, witness); err != nil {
		return err
	}
	if err := c.sstore(AccountKeychainAddress, c.witnessSlot(account, witness), flag(true).word()); err != nil {
		return err
	}
	return c.emit(AccountKeychainAddress, []Hash{evKeyAuthWitnessBurned, addressWord(account), witness}, nil)
}

func (c *Context) revokeKey(k keyRef) error {
	if err := c.ensureAdminCaller(k.account); err != nil {
		return err
	}
	stored, err := c.readAuthorizedKey(k)
	if err != nil {
		return err
	}
	if stored.expiry == 0 {
		return errSel("KeyNotFound()")
	}
	if err := c.storePackedGroup(AccountKeychainAddress, c.keySlot(k), encodeAuthorizedKey(authorizedKey{revoked: true}), keyFields...); err != nil {
		return err
	}
	return c.emit(AccountKeychainAddress, []Hash{evKeyRevoked, addressWord(k.account), addressWord(k.keyID)}, nil)
}

func (c *Context) activeNonAdminKey(k keyRef) (authorizedKey, error) {
	if err := c.ensureAdminCaller(k.account); err != nil {
		return authorizedKey{}, err
	}
	key, err := c.loadActiveKey(k)
	if err != nil {
		return key, err
	}
	if key.admin {
		return key, errSel("InvalidKeyId()")
	}
	return key, nil
}

func (c *Context) updateSpendingLimit(sp spend) error {
	key, err := c.activeNonAdminKey(sp.key)
	if err != nil {
		return err
	}
	if !key.enforceLimits {
		key.enforceLimits = true
		if err := c.storePackedGroup(AccountKeychainAddress, c.keySlot(sp.key), encodeAuthorizedKey(key), keyFields...); err != nil {
			return err
		}
	}
	s := c.limitSlot(sp.key, sp.token)
	if c.at(hardfork.T3) {
		limit, rerr := c.readLimit(s)
		if rerr != nil {
			return rerr
		}
		if sp.amount.Gt(u128Max) {
			return errSel("InvalidSpendingLimit()")
		}
		limit.remaining = sp.amount
		limit.max = sp.amount
		err = c.writeLimit(s, limit)
	} else {
		err = c.sstore(AccountKeychainAddress, s, sp.amount)
	}
	if err != nil {
		return err
	}
	return c.emit(AccountKeychainAddress, []Hash{evSpendingLimitUpdated, addressWord(sp.key.account), addressWord(sp.key.keyID), addressWord(sp.token)}, retWord(sp.amount))
}

func (c *Context) removeAllowedCalls(k keyRef, target Address) error {
	if _, err := c.activeNonAdminKey(k); err != nil {
		return err
	}
	base := c.scopeBase(c.spendingLimitKey(k))
	scoped, err := c.loadField(AccountKeychainAddress, base, fScoped)
	if err != nil || scoped.IsZero() {
		return err
	}
	removed, err := c.addressSet(base.plus(1)).remove(addressWord(target))
	if err != nil || !removed {
		return err
	}
	return c.clearTargetSelectors(base, target)
}

type limitView struct {
	remaining Word
	periodEnd uint64
}

func (c *Context) effectiveLimit(k keyRef, token Address) (limitView, error) {
	if k.keyID == (Address{}) && c.at(hardfork.T3) {
		return limitView{}, nil
	}
	key, err := c.readAuthorizedKey(k)
	if err != nil {
		return limitView{}, err
	}
	if key.revoked || key.expiry == 0 {
		return limitView{}, nil
	}
	if c.timestamp() >= key.expiry && c.at(hardfork.T3) {
		return limitView{}, nil
	}
	s := c.limitSlot(k, token)
	remaining, err := c.sload(AccountKeychainAddress, s)
	if err != nil || !c.at(hardfork.T3) {
		return limitView{remaining: remaining}, err
	}
	period, err := c.loadField(AccountKeychainAddress, s.plus(1), fLimitPeriod)
	if err != nil || period.IsZero() {
		return limitView{remaining: remaining}, err
	}
	if c.at(hardfork.T7) && remaining == zeroPeriodic {
		remaining = Word{}
	}
	end, err := c.loadField(AccountKeychainAddress, s.plus(1), fLimitPerEnd)
	if err != nil {
		return limitView{}, err
	}
	now := c.timestamp()
	if now < end.Uint64() {
		return limitView{remaining: remaining, periodEnd: end.Uint64()}, nil
	}
	l := spendingLimit{period: period.Uint64(), periodEnd: end.Uint64()}
	max, err := c.loadField(AccountKeychainAddress, s.plus(1), fLimitMax)
	return limitView{remaining: max, periodEnd: l.nextPeriodEnd(now)}, err
}

func keyArgs(c *Context, a []abiValue) keyRef { return keyRef{account: c.Caller, keyID: a[0].addr()} }

func viewKeyArgs(a []abiValue) keyRef { return keyRef{account: a[0].addr(), keyID: a[1].addr()} }

func legacyLimits(v abiValue) []TokenLimit {
	out := make([]TokenLimit, len(v.items))
	for i, row := range v.items {
		out[i] = TokenLimit{Token: wordAddress(row[0]), Amount: toWord(row[1])}
	}
	return out
}

var keychainTable = register(AccountKeychainAddress, newTable(
	mutate("authorizeKey(address,uint8,uint64,bool,(address,uint256)[])", []abiType{tAddress, tSigType, tU64, tBool, tTupleArray(tAddress.scalar, tU256.scalar)}, func(c *Context, a []abiValue) ([]byte, error) {
		if configurableTypeRefused(c, a[1].u8()) {
			return nil, revert(nil)
		}
		if c.at(hardfork.T3) {
			var w Hash
			sel := selector("authorizeKey(address,uint8,(uint64,bool,(address,uint256,uint64)[],bool,(address,(bytes4,address[])[])[]))")
			copy(w[:], sel[:])
			return nil, revert(abiError(selector("LegacyAuthorizeKeySelectorChanged(bytes4)"), w))
		}
		r := KeyRestrictions{Expiry: a[2].u64(), EnforceLimits: a[3].boolean(), Limits: legacyLimits(a[4]), AllowAnyCalls: true}
		return nil, AuthorizeKey(c, KeyGrant{Account: c.Caller, KeyID: a[0].addr(), SigType: a[1].u8(), Restrictions: r})
	}).mutatesUntil(hardfork.T3),
	mutate("burnKeyAuthorizationWitness(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.burnWitness(c.Caller, a[0].bytes32())
	}).from(hardfork.T5),
	mutate("revokeKey(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.revokeKey(keyArgs(c, a))
	}),
	mutate("updateSpendingLimit(address,address,uint256)", []abiType{tAddress, tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.updateSpendingLimit(spend{key: keyArgs(c, a), token: a[1].addr(), amount: a[2].u256()})
	}),
	mutate("removeAllowedCalls(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.removeAllowedCalls(keyArgs(c, a), a[1].addr())
	}).from(hardfork.T3),
	view("getKey(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		k := viewKeyArgs(a)
		key, err := c.readAuthorizedKey(k)
		if err != nil {
			return nil, err
		}
		if key.expiry == 0 || key.revoked {
			return encodeWords(Hash{}, Hash{}, Hash{}, Hash{}, flag(key.revoked).hash()), err
		}
		return encodeWords(wordOfU64(uint64(key.sigType)), addressWord(k.keyID), wordOfU64(key.expiry), flag(key.enforceLimits).hash(), flag(false).hash()), err
	}),
	view("getRemainingLimit(address,address,address)", []abiType{tAddress, tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		if !c.at(hardfork.T2) {
			w, err := c.sload(AccountKeychainAddress, c.limitSlot(viewKeyArgs(a), a[2].addr()))
			return retWord(w), err
		}
		v, err := c.effectiveLimit(viewKeyArgs(a), a[2].addr())
		return retWord(v.remaining), err
	}).before(hardfork.T3),
	view("getRemainingLimitWithPeriod(address,address,address)", []abiType{tAddress, tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		v, err := c.effectiveLimit(viewKeyArgs(a), a[2].addr())
		return encodeWords(toHash(v.remaining), wordOfU64(v.periodEnd)), err
	}).from(hardfork.T3),
	view("getAllowedCalls(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		v, err := c.allowedCalls(viewKeyArgs(a))
		return encodeAllowedCalls(v), err
	}).from(hardfork.T3),
	view("isKeyAuthorizationWitnessBurned(address,bytes32)", []abiType{tAddress, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		b, err := c.loadBool(AccountKeychainAddress, c.witnessSlot(a[0].addr(), a[1].bytes32()))
		return retBool(flag(b)), err
	}).from(hardfork.T5),
	view("isAdminKey(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.isAdminKey(viewKeyArgs(a))
		return retBool(flag(ok)), err
	}).from(hardfork.T6),
	view("getTransactionKey()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		k, err := c.transactionKey()
		return retAddr(k), err
	}),
))
