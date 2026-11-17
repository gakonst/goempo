package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const maxNestedItems = 1 << 12

type abiCursor struct {
	data   []byte
	base   int
	strict flag
}

func (cur abiCursor) word(i int) (Hash, error) {
	at := cur.base + 32*i
	if at < 0 || at+32 > len(cur.data) {
		return Hash{}, errABI
	}
	return wordAt(cur.data, at), nil
}

func (cur abiCursor) scalar(i int, t abiType) (Hash, error) {
	w, err := cur.word(i)
	if err != nil {
		return Hash{}, err
	}
	return decodeScalar(w, t.scalar, decodeMode{strict: cur.strict})
}

func (cur abiCursor) follow(i int) (abiCursor, error) {
	w, err := cur.word(i)
	if err != nil {
		return abiCursor{}, err
	}
	off, err := smallInt(w, len(cur.data))
	if err != nil {
		return abiCursor{}, err
	}
	next := abiCursor{data: cur.data, base: cur.base + off, strict: cur.strict}
	if next.base > len(cur.data) {
		return abiCursor{}, errABI
	}
	return next, nil
}

type abiArray struct {
	items abiCursor
	n     int
}

func (cur abiCursor) array() (abiArray, error) {
	w, err := cur.word(0)
	if err != nil {
		return abiArray{}, err
	}
	n, err := smallInt(w, maxNestedItems)
	if err != nil {
		return abiArray{}, err
	}
	return abiArray{items: abiCursor{data: cur.data, base: cur.base + 32, strict: cur.strict}, n: n}, nil
}

func (cur abiCursor) addresses() ([]Address, error) {
	arr, err := cur.array()
	if err != nil {
		return nil, err
	}
	out := make([]Address, arr.n)
	for i := range out {
		w, err := arr.items.scalar(i, tAddress)
		if err != nil {
			return nil, err
		}
		out[i] = wordAddress(w)
	}
	return out, nil
}

var tBytes4 = scalarArg(kBytesN, 4)

func (cur abiCursor) selectorRule() (SelectorRule, error) {
	sel, err := cur.scalar(0, tBytes4)
	if err != nil {
		return SelectorRule{}, err
	}
	rc, err := cur.follow(1)
	if err != nil {
		return SelectorRule{}, err
	}
	recipients, err := rc.addresses()
	return SelectorRule{Selector: [4]byte(sel[:4]), Recipients: recipients}, err
}

func (cur abiCursor) callScope() (CallScope, error) {
	target, err := cur.scalar(0, tAddress)
	if err != nil {
		return CallScope{}, err
	}
	rc, err := cur.follow(1)
	if err != nil {
		return CallScope{}, err
	}
	arr, err := rc.array()
	if err != nil {
		return CallScope{}, err
	}
	scope := CallScope{Target: wordAddress(target), SelectorRules: make([]SelectorRule, arr.n)}
	for i := range scope.SelectorRules {
		el, err := arr.items.follow(i)
		if err != nil {
			return CallScope{}, err
		}
		if scope.SelectorRules[i], err = el.selectorRule(); err != nil {
			return CallScope{}, err
		}
	}
	return scope, nil
}

func (cur abiCursor) callScopes() ([]CallScope, error) {
	arr, err := cur.array()
	if err != nil {
		return nil, err
	}
	out := make([]CallScope, arr.n)
	for i := range out {
		el, err := arr.items.follow(i)
		if err != nil {
			return nil, err
		}
		if out[i], err = el.callScope(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (cur abiCursor) tokenLimits() ([]TokenLimit, error) {
	arr, err := cur.array()
	if err != nil {
		return nil, err
	}
	out := make([]TokenLimit, arr.n)
	for i := range out {
		row := abiCursor{data: cur.data, base: arr.items.base + 96*i, strict: cur.strict}
		token, err := row.scalar(0, tAddress)
		if err != nil {
			return nil, err
		}
		amount, err := row.scalar(1, tU256)
		if err != nil {
			return nil, err
		}
		period, err := row.scalar(2, tU64)
		if err != nil {
			return nil, err
		}
		out[i] = TokenLimit{Token: wordAddress(token), Amount: toWord(amount), Period: toWord(period).Uint64()}
	}
	return out, nil
}

func (cur abiCursor) restrictions() (KeyRestrictions, error) {
	expiry, err := cur.scalar(0, tU64)
	if err != nil {
		return KeyRestrictions{}, err
	}
	enforce, err := cur.scalar(1, tBool)
	if err != nil {
		return KeyRestrictions{}, err
	}
	lc, err := cur.follow(2)
	if err != nil {
		return KeyRestrictions{}, err
	}
	limits, err := lc.tokenLimits()
	if err != nil {
		return KeyRestrictions{}, err
	}
	anyCalls, err := cur.scalar(3, tBool)
	if err != nil {
		return KeyRestrictions{}, err
	}
	sc, err := cur.follow(4)
	if err != nil {
		return KeyRestrictions{}, err
	}
	scopes, err := sc.callScopes()
	return KeyRestrictions{
		Expiry: toWord(expiry).Uint64(), EnforceLimits: !toWord(enforce).IsZero(), Limits: limits,
		AllowAnyCalls: !toWord(anyCalls).IsZero(), AllowedCalls: scopes,
	}, err
}

func rawCursor(c *Context, a []abiValue) abiCursor {
	return abiCursor{data: a[0].data, strict: flag(c.at(hardfork.T11))}
}

func configurableTypeRefused(c *Context, sigType uint8) bool {
	return c.at(hardfork.T11) && !c.at(hardfork.T14) && sigType == SigConfigurableAccount
}

func decodeOrRevert(err error) error {
	if err == errABI {
		return revert(nil)
	}
	return err
}

func authorizeKeyV2(c *Context, a []abiValue, withWitness flag) error {
	cur := rawCursor(c, a)
	keyID, err := cur.scalar(0, tAddress)
	if err != nil {
		return revert(nil)
	}
	sigType, err := cur.scalar(1, tSigType)
	if err != nil || configurableTypeRefused(c, sigType[31]) {
		return revert(nil)
	}
	rc, err := cur.follow(2)
	if err != nil {
		return revert(nil)
	}
	r, err := rc.restrictions()
	if err != nil {
		return revert(nil)
	}
	g := KeyGrant{Account: c.Caller, KeyID: wordAddress(keyID), SigType: sigType[31], Restrictions: r}
	if withWitness {
		w, err := cur.scalar(3, tB32)
		if err != nil {
			return revert(nil)
		}
		g.Witness = &w
	}
	return AuthorizeKey(c, g)
}

func (c *Context) setAllowedCalls(k keyRef, scopes []CallScope) error {
	if !c.at(hardfork.T3) {
		return errSel("InvalidCallScope()")
	}
	if _, err := c.activeNonAdminKey(k); err != nil {
		return err
	}
	if len(scopes) == 0 {
		return errSel("InvalidCallScope()")
	}
	base := c.scopeBase(c.spendingLimitKey(k))
	if err := c.writeCallScopes(base, scopes); err != nil {
		return err
	}
	return c.sstore(AccountKeychainAddress, base, flag(true).word())
}

var tRaw = abiType{kind: argRaw}

var keychainV2Table = extend(keychainTable,
	mutate("authorizeKey(address,uint8,(uint64,bool,(address,uint256,uint64)[],bool,(address,(bytes4,address[])[])[]))", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, authorizeKeyV2(c, a, false)
	}).from(hardfork.T3),
	mutate("authorizeKey(address,uint8,(uint64,bool,(address,uint256,uint64)[],bool,(address,(bytes4,address[])[])[]),bytes32)", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, authorizeKeyV2(c, a, true)
	}).from(hardfork.T5),
	mutate("authorizeAdminKey(address,uint8,bytes32)", []abiType{tAddress, tSigType, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		if configurableTypeRefused(c, a[1].u8()) {
			return nil, revert(nil)
		}
		w := a[2].bytes32()
		g := KeyGrant{Account: c.Caller, KeyID: a[0].addr(), SigType: a[1].u8(), Admin: true, Restrictions: KeyRestrictions{Expiry: ^uint64(0), AllowAnyCalls: true}, Witness: &w}
		return nil, AuthorizeKey(c, g)
	}).from(hardfork.T6).before(hardfork.T14),
	mutate("setAllowedCalls(address,(address,(bytes4,address[])[])[])", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		cur := rawCursor(c, a)
		keyID, err := cur.scalar(0, tAddress)
		if err != nil {
			return nil, revert(nil)
		}
		sc, err := cur.follow(1)
		if err != nil {
			return nil, revert(nil)
		}
		scopes, err := sc.callScopes()
		if err != nil {
			return nil, decodeOrRevert(err)
		}
		return nil, c.setAllowedCalls(keyRef{account: c.Caller, keyID: wordAddress(keyID)}, scopes)
	}).from(hardfork.T3),
)
