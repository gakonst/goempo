package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	maxSetItems      = 1 << 16
	maxStoredLen     = 1<<32 - 1
	gasDedupPerItem  = 20
	addressesPerSlot = 1
	selectorsPerSlot = 8
	selectorBytes    = 4
)

type storedSet struct {
	c       *Context
	addr    Address
	base    slot
	perSlot int
}

func (c *Context) addressSet(base slot) storedSet {
	return storedSet{c: c, addr: AccountKeychainAddress, base: base, perSlot: addressesPerSlot}
}

func (c *Context) selectorSet(base slot) storedSet {
	return storedSet{c: c, addr: AccountKeychainAddress, base: base, perSlot: selectorsPerSlot}
}

func (s storedSet) dataSlot() slot { return slot(s.c.keccak(s.base[:])) }

func (s storedSet) length() (int, error) {
	w, err := s.c.sload(s.addr, s.base)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() || w.Uint64() > maxStoredLen {
		return 0, underOverflow()
	}
	return int(w.Uint64()), nil
}

func (s storedSet) elemField(i int) field {
	if s.perSlot == addressesPerSlot {
		return field{offset: 0, size: 20}
	}
	return field{offset: uint(i%s.perSlot) * selectorBytes, size: selectorBytes}
}

func (s storedSet) elemSlot(i int) slot { return s.dataSlot().plus(uint64(i / s.perSlot)) }

func (s storedSet) positionSlot(v Hash) slot { return s.c.mapKey(v, s.base.plus(1)) }

func (s storedSet) writeElem(i int, v Hash) error {
	if s.perSlot == addressesPerSlot {
		return s.c.sstore(s.addr, s.elemSlot(i), toWord(v))
	}
	return s.c.storeField(s.addr, s.elemSlot(i), s.elemField(i), toWord(v))
}

func (s storedSet) read() ([]Hash, error) {
	n, err := s.length()
	if err != nil {
		return nil, err
	}
	n = min(n, maxStoredLen)
	out := make([]Hash, 0, min(n, maxSetItems))
	for i := 0; i < n; i++ {
		w, err := s.c.loadField(s.addr, s.elemSlot(i), s.elemField(i))
		if err != nil {
			return nil, err
		}
		out = append(out, toHash(w))
	}
	return out, nil
}

func (s storedSet) insert(v Hash) error {
	pos, err := s.c.sload(s.addr, s.positionSlot(v))
	if err != nil || !pos.IsZero() {
		return err
	}
	n, err := s.length()
	if err != nil {
		return err
	}
	if err := s.c.sstore(s.addr, s.positionSlot(v), u256.FromUint64(uint64(n+1))); err != nil {
		return err
	}
	if _, err := s.length(); err != nil {
		return err
	}
	if err := s.writeElem(n, v); err != nil {
		return err
	}
	return s.c.sstore(s.addr, s.base, u256.FromUint64(uint64(n+1)))
}

func (s storedSet) writeAll(values []Hash) error {
	n, err := s.length()
	if err != nil {
		return err
	}
	n = min(n, maxSetItems)
	if len(values) > maxSetItems {
		return errSel("InvalidCallScope()")
	}
	for i := 0; i < n; i++ {
		w, err := s.c.loadField(s.addr, s.elemSlot(i), s.elemField(i))
		if err != nil {
			return err
		}
		if err := s.c.sstore(s.addr, s.positionSlot(toHash(w)), Word{}); err != nil {
			return err
		}
	}
	for i, v := range values {
		if err := s.c.sstore(s.addr, s.positionSlot(v), u256.FromUint64(uint64(i+1))); err != nil {
			return err
		}
		if err := s.writeElem(i, v); err != nil {
			return err
		}
	}
	if err := s.c.sstore(s.addr, s.base, u256.FromUint64(uint64(len(values)))); err != nil {
		return err
	}
	for i := len(values); i < n; i++ {
		if err := s.c.sstore(s.addr, s.elemSlot(i), Word{}); err != nil {
			return err
		}
	}
	return nil
}

func (s storedSet) clear() error {
	values, err := s.read()
	if err != nil {
		return err
	}
	for _, v := range values {
		if err := s.c.sstore(s.addr, s.positionSlot(v), Word{}); err != nil {
			return err
		}
	}
	if _, err := s.length(); err != nil {
		return err
	}
	if err := s.c.sstore(s.addr, s.base, Word{}); err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	slots := min((len(values)+s.perSlot-1)/s.perSlot, maxSetItems)
	for i := 0; i < slots; i++ {
		if err := s.c.sstore(s.addr, s.dataSlot().plus(uint64(i)), Word{}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) targetScopeBase(scopeBase slot, target Address) slot {
	return c.mapAddr(target, scopeBase.plus(3))
}

func selectorHash(sel [4]byte) Hash {
	var h Hash
	copy(h[28:], sel[:])
	return h
}

func (c *Context) recipientsBase(targetBase slot, selWord Hash) slot {
	return c.mapKey(selWord, targetBase.plus(2))
}

func (c *Context) clearTargetSelectors(scopeBase slot, target Address) error {
	tb := c.targetScopeBase(scopeBase, target)
	sels, err := c.selectorSet(tb).read()
	if err != nil {
		return err
	}
	for _, sel := range sels {
		if err := c.addressSet(c.recipientsBase(tb, sel)).clear(); err != nil {
			return err
		}
	}
	return c.selectorSet(tb).clear()
}

func (c *Context) clearAllTargetScopes(scopeBase slot) error {
	targets, err := c.addressSet(scopeBase.plus(1)).read()
	if err != nil {
		return err
	}
	for _, t := range targets {
		if err := c.clearTargetSelectors(scopeBase, wordAddress(t)); err != nil {
			return err
		}
	}
	return c.addressSet(scopeBase.plus(1)).clear()
}

func (c *Context) hasDuplicates(values []Hash) (bool, error) {
	if c.at(hardfork.T11) {
		if err := c.charge(gasDedupPerItem * uint64(len(values))); err != nil {
			return false, err
		}
	}
	for i := range values {
		for j := 0; j < i; j++ {
			if values[i] == values[j] {
				return true, nil
			}
		}
	}
	return false, nil
}

var constrainedSelectors = [][4]byte{
	selector("transfer(address,uint256)"),
	selector("approve(address,uint256)"),
	selector("transferWithMemo(address,uint256,bytes32)"),
}

func isConstrainedSelector(sel [4]byte) bool {
	for _, s := range constrainedSelectors {
		if s == sel {
			return true
		}
	}
	return false
}

func (c *Context) validateCallScope(scope CallScope) error {
	if scope.Target == (Address{}) {
		return errSel("InvalidCallScope()")
	}
	if len(scope.SelectorRules) == 0 {
		return nil
	}
	if c.at(hardfork.T11) {
		sels := make([]Hash, len(scope.SelectorRules))
		for i, r := range scope.SelectorRules {
			sels[i] = selectorHash(r.Selector)
		}
		dup, err := c.hasDuplicates(sels)
		if err != nil {
			return err
		}
		if dup {
			return errSel("InvalidCallScope()")
		}
	}
	for i, r := range scope.SelectorRules {
		if !c.at(hardfork.T11) {
			for j := 0; j < i; j++ {
				if scope.SelectorRules[j].Selector == r.Selector {
					return errSel("InvalidCallScope()")
				}
			}
		}
		if err := c.validateRecipients(scope.Target, r); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) validateRecipients(target Address, r SelectorRule) error {
	if len(r.Recipients) == 0 {
		return nil
	}
	tip20 := IsTIP20(target)
	if !c.at(hardfork.T4) {
		ok, err := IsTIP20Deployed(c, target)
		if err != nil {
			return err
		}
		tip20 = ok
	}
	if !isConstrainedSelector(r.Selector) || !tip20 {
		return errSel("InvalidCallScope()")
	}
	words := make([]Hash, len(r.Recipients))
	for i, a := range r.Recipients {
		if a == (Address{}) {
			return errSel("InvalidCallScope()")
		}
		words[i] = addressWord(a)
	}
	dup, err := c.hasDuplicates(words)
	if err != nil {
		return err
	}
	if dup {
		return errSel("InvalidCallScope()")
	}
	return nil
}

func (c *Context) validateCallScopes(scopes []CallScope) error {
	targets := make([]Hash, len(scopes))
	for i, s := range scopes {
		targets[i] = addressWord(s.Target)
	}
	if c.at(hardfork.T11) {
		dup, err := c.hasDuplicates(targets)
		if err != nil {
			return err
		}
		if dup {
			return errSel("InvalidCallScope()")
		}
	}
	for i, s := range scopes {
		if !c.at(hardfork.T11) {
			for j := 0; j < i; j++ {
				if targets[j] == targets[i] {
					return errSel("InvalidCallScope()")
				}
			}
		}
		if c.at(hardfork.T4) {
			if err := c.validateCallScope(s); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Context) writeCallScopes(scopeBase slot, scopes []CallScope) error {
	if err := c.validateCallScopes(scopes); err != nil {
		return err
	}
	for _, s := range scopes {
		if err := c.upsertTargetScope(scopeBase, s); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) upsertTargetScope(scopeBase slot, s CallScope) error {
	if !c.at(hardfork.T4) {
		if err := c.validateCallScope(s); err != nil {
			return err
		}
	}
	if err := c.addressSet(scopeBase.plus(1)).insert(addressWord(s.Target)); err != nil {
		return err
	}
	if err := c.clearTargetSelectors(scopeBase, s.Target); err != nil {
		return err
	}
	tb := c.targetScopeBase(scopeBase, s.Target)
	for _, r := range s.SelectorRules {
		sel := selectorHash(r.Selector)
		if err := c.selectorSet(tb).insert(sel); err != nil {
			return err
		}
		rb := c.recipientsBase(tb, sel)
		if len(r.Recipients) == 0 {
			if !c.at(hardfork.T4) {
				if err := c.addressSet(rb).clear(); err != nil {
					return err
				}
			}
			continue
		}
		words := make([]Hash, len(r.Recipients))
		for i, a := range r.Recipients {
			words[i] = addressWord(a)
		}
		if err := c.addressSet(rb).writeAll(words); err != nil {
			return err
		}
	}
	return nil
}
