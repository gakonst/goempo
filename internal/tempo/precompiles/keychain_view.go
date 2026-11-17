package precompiles

type selectorRuleView struct {
	selector   Hash
	recipients []Hash
}

type callScopeView struct {
	target Hash
	rules  []selectorRuleView
}

func (c *Context) allowedCalls(k keyRef) (bool, []callScopeView, error) {
	if k.keyID == (Address{}) {
		return false, nil, nil
	}
	w, err := c.sload(AccountKeychainAddress, c.keySlot(k))
	if err != nil {
		return false, nil, err
	}
	key := decodeAuthorizedKey(w)
	if key.expiry == 0 || key.revoked || c.timestamp() >= key.expiry {
		return true, nil, nil
	}
	base := c.scopeBase(c.spendingLimitKey(k))
	scoped, err := c.loadField(AccountKeychainAddress, base, fScoped)
	if err != nil || scoped.IsZero() {
		return false, nil, err
	}
	targets, err := c.addressSet(base.plus(1)).read()
	if err != nil {
		return false, nil, err
	}
	scopes := make([]callScopeView, 0, len(targets))
	for _, t := range targets {
		tb := c.targetScopeBase(base, wordAddress(t))
		sels, err := c.selectorSet(tb).read()
		if err != nil {
			return false, nil, err
		}
		rules := make([]selectorRuleView, 0, len(sels))
		for _, sel := range sels {
			recipients, err := c.addressSet(c.recipientsBase(tb, sel)).read()
			if err != nil {
				return false, nil, err
			}
			var left Hash
			copy(left[:selectorBytes], sel[32-selectorBytes:])
			rules = append(rules, selectorRuleView{selector: left, recipients: recipients})
		}
		scopes = append(scopes, callScopeView{target: t, rules: rules})
	}
	return true, scopes, nil
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

func encodeAllowedCalls(scoped bool, scopes []callScopeView) []byte {
	parts := make([][]byte, len(scopes))
	for i, s := range scopes {
		parts[i] = encodeCallScope(s)
	}
	out := encodeWords(flag(scoped).hash(), offsetWord(64))
	return append(out, encodeDynamicArray(parts)...)
}
