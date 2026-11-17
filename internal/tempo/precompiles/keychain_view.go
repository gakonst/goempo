package precompiles

type selectorRuleView struct {
	selector   Hash
	recipients []Hash
}

type callScopeView struct {
	target Hash
	rules  []selectorRuleView
}

type allowedCallsView struct {
	scoped flag
	scopes []callScopeView
}

func (c *Context) allowedCalls(k keyRef) (allowedCallsView, error) {
	if k.keyID == (Address{}) {
		return allowedCallsView{}, nil
	}
	w, err := c.sload(AccountKeychainAddress, c.keySlot(k))
	if err != nil {
		return allowedCallsView{}, err
	}
	key := decodeAuthorizedKey(w)
	if key.expiry == 0 || key.revoked || c.timestamp() >= key.expiry {
		return allowedCallsView{scoped: true}, nil
	}
	base := c.scopeBase(c.spendingLimitKey(k))
	scoped, err := c.loadField(AccountKeychainAddress, base, fScoped)
	if err != nil || scoped.IsZero() {
		return allowedCallsView{}, err
	}
	targets, err := c.addressSet(base.plus(1)).read()
	if err != nil {
		return allowedCallsView{}, err
	}
	scopes := make([]callScopeView, 0, len(targets))
	for _, t := range targets {
		tb := c.targetScopeBase(base, wordAddress(t))
		sels, err := c.selectorSet(tb).read()
		if err != nil {
			return allowedCallsView{}, err
		}
		rules := make([]selectorRuleView, 0, len(sels))
		for _, sel := range sels {
			recipients, err := c.addressSet(c.recipientsBase(tb, sel)).read()
			if err != nil {
				return allowedCallsView{}, err
			}
			var left Hash
			copy(left[:selectorBytes], sel[32-selectorBytes:])
			rules = append(rules, selectorRuleView{selector: left, recipients: recipients})
		}
		scopes = append(scopes, callScopeView{target: t, rules: rules})
	}
	return allowedCallsView{scoped: true, scopes: scopes}, nil
}

func offsetWord(n int) Hash { return wordOfU64(uint64(n)) }

func encodeHashArray(items []Hash) []byte {
	out := encodeWords(offsetWord(len(items)))
	return append(out, encodeWords(items...)...)
}

func encodeDynamicArray(parts [][]byte) []byte {
	head := encodeWords(offsetWord(len(parts)))
	var tail []byte
	offset := 32 * len(parts)
	for _, p := range parts {
		head = append(head, encodeWords(offsetWord(offset))...)
		tail = append(tail, p...)
		offset += len(p)
	}
	return append(head, tail...)
}

func encodeSelectorRule(r selectorRuleView) []byte {
	out := encodeWords(r.selector, offsetWord(64))
	return append(out, encodeHashArray(r.recipients)...)
}

func encodeCallScope(s callScopeView) []byte {
	rules := make([][]byte, len(s.rules))
	for i, r := range s.rules {
		rules[i] = encodeSelectorRule(r)
	}
	out := encodeWords(s.target, offsetWord(64))
	return append(out, encodeDynamicArray(rules)...)
}

func encodeAllowedCalls(v allowedCallsView) []byte {
	parts := make([][]byte, len(v.scopes))
	for i, s := range v.scopes {
		parts[i] = encodeCallScope(s)
	}
	out := encodeWords(v.scoped.hash(), offsetWord(64))
	return append(out, encodeDynamicArray(parts)...)
}
