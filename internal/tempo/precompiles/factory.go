package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const reservedTokenIDs uint64 = 1024

var (
	roleUngrantable = Hash{
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	}
	tokenCodeMarker = []byte{0xef}
)

type tokenSalt struct {
	sender Address
	salt   Hash
}

type tokenSlot struct {
	addr     Address
	reserved bool
}

func (c *Context) tokenAddressFor(s tokenSalt) tokenSlot {
	h := c.keccak(encodeWords(addressWord(s.sender), s.salt))
	id := uint64(0)
	for _, b := range h[:8] {
		id = id<<8 | uint64(b)
	}
	return tokenSlot{addr: TIP20Address(id), reserved: id < reservedTokenIDs}
}

type TokenSpec struct {
	Name       []byte
	Symbol     []byte
	Currency   []byte
	QuoteToken Address
	Admin      Address
	Salt       Hash
}

func (c *Context) checkQuoteToken(spec TokenSpec) error {
	ok, err := IsTIP20Deployed(c, spec.QuoteToken)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("InvalidQuoteToken()")
	}
	if string(spec.Currency) != string(usdCurrency) {
		return nil
	}
	cur, err := TIP20(c, spec.QuoteToken).currency()
	if err != nil {
		return err
	}
	if string(cur) != string(usdCurrency) {
		return errSel("InvalidQuoteToken()")
	}
	return nil
}

func (t Token) initialize(sender Address, spec TokenSpec) error {
	if err := t.c.setCode(t.addr, tokenCodeMarker); err != nil {
		return err
	}
	for _, s := range []struct {
		at    slot
		value []byte
	}{{slotName, spec.Name}, {slotSymbol, spec.Symbol}, {slotCurrency, spec.Currency}} {
		if err := t.c.storeString(t.addr, s.at, s.value); err != nil {
			return err
		}
	}
	if err := t.c.storeAddress(t.addr, slotQuoteToken, spec.QuoteToken); err != nil {
		return err
	}
	if err := t.c.storeField(t.addr, slotNextQuoteToken, fNextQuote, toWord(addressWord(spec.QuoteToken))); err != nil {
		return err
	}
	if err := t.store(slotSupplyCap, u128Max); err != nil {
		return err
	}
	var err error
	if t.c.at(hardfork.T9) {
		err = t.c.setTokenTransferPolicy(t.addr, policyAllowAll)
	} else {
		err = t.c.storeField(t.addr, slotTransferPolicy, fTransferPolicy, u256.FromUint64(policyAllowAll))
	}
	if err != nil {
		return err
	}
	if err := t.store(t.c.mapKey(roleUngrantable, slotRoleAdmins), toWord(roleUngrantable)); err != nil {
		return err
	}
	r := roleChange{sender: sender, role: roleDefaultAdmin, account: spec.Admin, granted: true}
	if err := t.setRole(r); err != nil {
		return err
	}
	return t.emitRoleMembership(r)
}

func createToken(c *Context, sender Address, spec TokenSpec) (Address, error) {
	ts := c.tokenAddressFor(tokenSalt{sender: sender, salt: spec.Salt})
	addr := ts.addr
	exists, err := IsTIP20Deployed(c, addr)
	if err != nil {
		return Address{}, err
	}
	if exists {
		return Address{}, revert(abiError(selector("TokenAlreadyExists(address)"), addressWord(addr)))
	}
	if err := c.checkQuoteToken(spec); err != nil {
		return Address{}, err
	}
	if ts.reserved {
		return Address{}, errSel("AddressReserved()")
	}
	if err := TIP20(c, addr).initialize(sender, spec); err != nil {
		return Address{}, err
	}
	data := newEncoder(6).bytes(spec.Name).bytes(spec.Symbol).bytes(spec.Currency).
		addr(spec.QuoteToken).addr(spec.Admin).word(spec.Salt).out()
	return addr, c.emit(TIP20FactoryAddress, []Hash{evTokenCreated, addressWord(addr)}, data)
}

func tokenSpecArgs(a []abiValue) TokenSpec {
	return TokenSpec{Name: a[0].data, Symbol: a[1].data, Currency: a[2].data, QuoteToken: a[3].addr(), Admin: a[4].addr(), Salt: a[5].bytes32()}
}

var createTokenArgs = []abiType{tString, tString, tString, tAddress, tAddress, tB32}

var factoryTable = register(TIP20FactoryAddress, newTable(
	mutate("createToken(string,string,string,address,address,bytes32)", createTokenArgs, func(c *Context, a []abiValue) ([]byte, error) {
		addr, err := createToken(c, c.Caller, tokenSpecArgs(a))
		return retAddr(addr), err
	}),
	mutate("createToken(string,string,string,address,address,bytes32,string)", append(createTokenArgs, tString), func(c *Context, a []abiValue) ([]byte, error) {
		logo := a[6].data
		if len(logo) > 0 {
			if err := validLogoURI(logo); err != nil {
				return nil, err
			}
		}
		addr, err := createToken(c, c.Caller, tokenSpecArgs(a))
		if err != nil || len(logo) == 0 {
			return retAddr(addr), err
		}
		return retAddr(addr), TIP20(c, addr).writeLogoURI(c.Caller, logo)
	}).from(hardfork.T5),
	view("isTIP20(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := IsTIP20Deployed(c, a[0].addr())
		return retBool(flag(ok)), err
	}),
	view("getTokenAddress(address,bytes32)", []abiType{tAddress, tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		ts := c.tokenAddressFor(tokenSalt{sender: a[0].addr(), salt: a[1].bytes32()})
		if ts.reserved {
			return nil, errSel("AddressReserved()")
		}
		return retAddr(ts.addr), nil
	}),
))
