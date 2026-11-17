package precompiles

import "github.com/gakonst/goempo/internal/tempo/hardfork"

const scopeRecipientEnd = 36

func callNotAllowed() error { return errSel("CallNotAllowed()") }

func (s storedSet) contains(v Hash) (bool, error) {
	pos, err := s.c.sload(s.addr, s.positionSlot(v))
	if err != nil {
		return false, err
	}
	return !pos.IsZero(), nil
}

func (s storedSet) empty() (bool, error) {
	n, err := s.length()
	return n == 0, err
}

type TxCall struct {
	Account Address
	KeyID   Address
	To      *Address
	Input   []byte
}

func ValidateTxCallScope(c *Context, call TxCall) error {
	to, input := call.To, call.Input
	if call.KeyID == (Address{}) || !c.at(hardfork.T3) {
		return nil
	}
	if to == nil {
		return callNotAllowed()
	}
	base := c.scopeBase(c.spendingLimitKey(keyRef{account: call.Account, keyID: call.KeyID}))
	scoped, err := c.loadField(AccountKeychainAddress, base, fScoped)
	if err != nil || scoped.IsZero() {
		return err
	}
	if ok, err := c.addressSet(base.plus(1)).contains(addressWord(*to)); err != nil || !ok {
		return orNotAllowed(err)
	}
	tb := c.targetScopeBase(base, *to)
	if any, err := c.selectorSet(tb).empty(); err != nil || any {
		return err
	}
	if len(input) < selectorBytes {
		return callNotAllowed()
	}
	sel := selectorHash([selectorBytes]byte(input[:selectorBytes]))
	if ok, err := c.selectorSet(tb).contains(sel); err != nil || !ok {
		return orNotAllowed(err)
	}
	rb := c.recipientsBase(tb, sel)
	if any, err := c.addressSet(rb).empty(); err != nil || any {
		return err
	}
	if len(input) < scopeRecipientEnd || !zeroBytes(input[selectorBytes:scopeRecipientEnd-20]) {
		return callNotAllowed()
	}
	var recipient Address
	copy(recipient[:], input[scopeRecipientEnd-20:scopeRecipientEnd])
	ok, err := c.addressSet(rb).contains(addressWord(recipient))
	if err != nil || !ok {
		return orNotAllowed(err)
	}
	return nil
}

func orNotAllowed(err error) error {
	if err != nil {
		return err
	}
	return callNotAllowed()
}

func zeroBytes(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if b[i] != 0 {
			return false
		}
	}
	return true
}
