package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type ammReq struct {
	sender Address
	key    poolKey
	amount Word
	to     Address
}

type ammPair struct {
	user      Word
	validator Word
}

func (c *Context) liquiditySlot(id Hash, holder Address) slot {
	return c.mapAddr(holder, c.mapKey(id, slotFeeLiquidity))
}

func (c *Context) totalSupplySlot(id Hash) slot { return c.mapKey(id, slotFeeTotalSupply) }

func (c *Context) validatePair(k poolKey) error {
	if k.user == k.validator {
		return errSel("IdenticalAddresses()")
	}
	if err := c.validateUSD(k.user); err != nil {
		return err
	}
	return c.validateUSD(k.validator)
}

func (c *Context) addLiquidity(id Hash, holder Address, amount Word) error {
	s := c.liquiditySlot(id, holder)
	bal, err := c.sload(FeeManagerAddress, s)
	if err != nil {
		return err
	}
	nb, err := num(bal).add(amount)
	if err != nil {
		return err
	}
	return c.sstore(FeeManagerAddress, s, nb)
}

type minted struct {
	liquidity Word
	supply    Word
}

func (c *Context) mintedLiquidity(id Hash, p pool, amount Word) (minted, error) {
	supply, err := c.sload(FeeManagerAddress, c.totalSupplySlot(id))
	if err != nil {
		return minted{}, err
	}
	if p.reserveUser.IsZero() && p.reserveValidator.IsZero() {
		half := amount.Div(u256.FromUint64(2))
		if !half.Gt(ammMinLiquidity) {
			return minted{}, errSel("InsufficientLiquidity()")
		}
		ns, err := num(supply).add(ammMinLiquidity)
		if err != nil {
			return minted{}, err
		}
		if err := c.sstore(FeeManagerAddress, c.totalSupplySlot(id), ns); err != nil {
			return minted{}, err
		}
		return minted{liquidity: half.Sub(ammMinLiquidity), supply: ns}, nil
	}
	product := ammN.Mul(p.reserveUser).Div(ammScale)
	denom := p.reserveValidator.Add(product)
	if denom.IsZero() {
		return minted{}, errSel("DivisionByZero()")
	}
	numerator := amount.Mul(supply)
	if !supply.IsZero() && numerator.Div(supply) != amount {
		return minted{}, errSel("InvalidSwapCalculation()")
	}
	return minted{liquidity: numerator.Div(denom), supply: supply}, nil
}

func ammMint(c *Context, r ammReq) (Word, error) {
	if r.key.user == r.key.validator {
		return Word{}, errSel("IdenticalAddresses()")
	}
	if r.amount.IsZero() {
		return Word{}, errSel("InvalidAmount()")
	}
	if err := c.validatePair(r.key); err != nil {
		return Word{}, err
	}
	ut, vt := TIP20(c, r.key.user), TIP20(c, r.key.validator)
	if c.at(hardfork.T8) {
		err := ut.ensureAuthorizedAs(party{user: r.sender, role: roleSender}, party{user: FeeManagerAddress, role: roleRecipient}, party{user: r.to, role: roleRecipient})
		if err != nil {
			return Word{}, err
		}
		if err := vt.ensureAuthorizedAs(party{user: r.to, role: roleRecipient}); err != nil {
			return Word{}, err
		}
	}
	id := c.poolID(r.key)
	p, err := c.readPool(id)
	if err != nil {
		return Word{}, err
	}
	m, err := c.mintedLiquidity(id, p, r.amount)
	if err != nil {
		return Word{}, err
	}
	liquidity := m.liquidity
	if liquidity.IsZero() {
		return Word{}, errSel("InsufficientLiquidity()")
	}
	if err := vt.systemTransferFrom(systemPull{caller: FeeManagerAddress, from: r.sender, amount: r.amount}); err != nil {
		return Word{}, err
	}
	nv := p.reserveValidator.Add(r.amount)
	if r.amount.Gt(u128Max) || nv.Gt(u128Max) {
		return Word{}, errSel("InvalidAmount()")
	}
	p.reserveValidator = nv
	if err := c.writePool(id, p); err != nil {
		return Word{}, err
	}
	ns, err := num(m.supply).add(liquidity)
	if err != nil {
		return Word{}, err
	}
	if err := c.sstore(FeeManagerAddress, c.totalSupplySlot(id), ns); err != nil {
		return Word{}, err
	}
	if err := c.addLiquidity(id, r.to, liquidity); err != nil {
		return Word{}, err
	}
	data := encodeWords(addressWord(r.sender), toHash(r.amount), toHash(liquidity))
	return liquidity, c.emit(FeeManagerAddress, []Hash{evAMMMint, addressWord(r.to), addressWord(r.key.user), addressWord(r.key.validator)}, data)
}

func (c *Context) burnShares(id Hash, p pool, liquidity Word) (ammPair, error) {
	supply, err := c.sload(FeeManagerAddress, c.totalSupplySlot(id))
	if err != nil {
		return ammPair{}, err
	}
	if supply.IsZero() {
		return ammPair{}, underOverflow()
	}
	u, err := ratio{num: p.reserveUser, den: supply}.of(liquidity)
	if err != nil {
		return ammPair{}, err
	}
	v, err := ratio{num: p.reserveValidator, den: supply}.of(liquidity)
	if err != nil {
		return ammPair{}, err
	}
	if v.Gt(u128Max) {
		return ammPair{}, errSel("InvalidAmount()")
	}
	if v.Gt(p.reserveValidator) {
		return ammPair{}, errSel("InsufficientReserves()")
	}
	if c.at(hardfork.T1C) {
		reserved, err := c.reservation(id)
		if err != nil {
			return ammPair{}, err
		}
		if p.reserveValidator.Sub(v).Lt(reserved) {
			return ammPair{}, errSel("InsufficientLiquidity()")
		}
	}
	return ammPair{user: u, validator: v}, nil
}

func ammBurn(c *Context, r ammReq) (ammPair, error) {
	if r.key.user == r.key.validator {
		return ammPair{}, errSel("IdenticalAddresses()")
	}
	if r.amount.IsZero() {
		return ammPair{}, errSel("InvalidAmount()")
	}
	if err := c.validatePair(r.key); err != nil {
		return ammPair{}, err
	}
	ut, vt := TIP20(c, r.key.user), TIP20(c, r.key.validator)
	if c.at(hardfork.T8) {
		if err := ut.ensureAuthorizedAs(party{user: r.sender, role: roleSender}); err != nil {
			return ammPair{}, err
		}
		if err := vt.ensureAuthorizedAs(party{user: r.sender, role: roleSender}); err != nil {
			return ammPair{}, err
		}
	}
	id := c.poolID(r.key)
	ls := c.liquiditySlot(id, r.sender)
	bal, err := c.sload(FeeManagerAddress, ls)
	if err != nil {
		return ammPair{}, err
	}
	if bal.Lt(r.amount) {
		return ammPair{}, errSel("InsufficientLiquidity()")
	}
	p, err := c.readPool(id)
	if err != nil {
		return ammPair{}, err
	}
	out, err := c.burnShares(id, p, r.amount)
	if err != nil {
		return ammPair{}, err
	}
	if err := c.sstore(FeeManagerAddress, ls, bal.Sub(r.amount)); err != nil {
		return ammPair{}, err
	}
	supply, err := c.sload(FeeManagerAddress, c.totalSupplySlot(id))
	if err != nil {
		return ammPair{}, err
	}
	ns, err := num(supply).sub(r.amount)
	if err != nil {
		return ammPair{}, err
	}
	if err := c.sstore(FeeManagerAddress, c.totalSupplySlot(id), ns); err != nil {
		return ammPair{}, err
	}
	if out.user.Gt(u128Max) {
		return ammPair{}, errSel("InvalidAmount()")
	}
	if out.user.Gt(p.reserveUser) {
		return ammPair{}, errSel("InsufficientReserves()")
	}
	p.reserveUser = p.reserveUser.Sub(out.user)
	p.reserveValidator = p.reserveValidator.Sub(out.validator)
	if err := c.writePool(id, p); err != nil {
		return ammPair{}, err
	}
	if err := ut.transfer(transferReq{from: FeeManagerAddress, to: r.to, amount: out.user}); err != nil {
		return ammPair{}, err
	}
	if err := vt.transfer(transferReq{from: FeeManagerAddress, to: r.to, amount: out.validator}); err != nil {
		return ammPair{}, err
	}
	data := encodeWords(toHash(out.user), toHash(out.validator), toHash(r.amount), addressWord(r.to))
	return out, c.emit(FeeManagerAddress, []Hash{evAMMBurn, addressWord(r.sender), addressWord(r.key.user), addressWord(r.key.validator)}, data)
}

func rebalanceSwap(c *Context, r ammReq) (Word, error) {
	if r.amount.IsZero() {
		return Word{}, errSel("InvalidAmount()")
	}
	id := c.poolID(r.key)
	p, err := c.readPool(id)
	if err != nil {
		return Word{}, err
	}
	in, err := ratio{num: ammN, den: ammScale}.of(r.amount)
	if err != nil {
		return Word{}, err
	}
	in = in.Add(u256.One())
	if in.Gt(u128Max) || r.amount.Gt(u128Max) {
		return Word{}, errSel("InvalidAmount()")
	}
	nv := p.reserveValidator.Add(in)
	if nv.Gt(u128Max) {
		return Word{}, errSel("InsufficientReserves()")
	}
	if r.amount.Gt(p.reserveUser) {
		return Word{}, errSel("InvalidAmount()")
	}
	p.reserveValidator = nv
	p.reserveUser = p.reserveUser.Sub(r.amount)
	if c.at(hardfork.T1C) {
		reserved, err := c.reservation(id)
		if err != nil {
			return Word{}, err
		}
		if p.reserveValidator.Lt(reserved) {
			return Word{}, errSel("InsufficientLiquidity()")
		}
	}
	if err := c.writePool(id, p); err != nil {
		return Word{}, err
	}
	vt, err := c.tip20(r.key.validator)
	if err != nil {
		return Word{}, err
	}
	if err := vt.systemTransferFrom(systemPull{caller: FeeManagerAddress, from: r.sender, amount: in}); err != nil {
		return Word{}, err
	}
	c.creditMinting = false
	ut, err := c.tip20(r.key.user)
	if err != nil {
		return Word{}, err
	}
	if err := ut.transfer(transferReq{from: FeeManagerAddress, to: r.to, amount: r.amount}); err != nil {
		return Word{}, err
	}
	data := encodeWords(toHash(in), toHash(r.amount))
	return in, c.emit(FeeManagerAddress, []Hash{evRebalanceSwap, addressWord(r.key.user), addressWord(r.key.validator), addressWord(r.sender)}, data)
}

func poolWords(p pool) []byte { return encodeWords(toHash(p.reserveUser), toHash(p.reserveValidator)) }

func ammArgs(c *Context, a []abiValue) ammReq {
	return ammReq{sender: c.Caller, key: poolKey{user: a[0].addr(), validator: a[1].addr()}, amount: a[2].u256(), to: a[3].addr()}
}

func retWord(w Word) []byte    { return encodeWords(toHash(w)) }
func retAddr(a Address) []byte { return encodeWords(addressWord(a)) }

var ammArgTypes = []abiType{tAddress, tAddress, tU256, tAddress}

var feeManagerTable = register(FeeManagerAddress, newTable(
	view("userTokens(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		t, err := UserToken(c, a[0].addr())
		return retAddr(t), err
	}),
	view("validatorTokens(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		t, err := ValidatorToken(c, a[0].addr())
		return retAddr(t), err
	}),
	view("collectedFees(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(FeeManagerAddress, c.mapAddr(a[1].addr(), c.mapAddr(a[0].addr(), slotFeeCollected)))
		return retWord(w), err
	}),
	mutate("setValidatorToken(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, setValidatorToken(c, tokenChoice{account: c.Caller, token: a[0].addr()})
	}),
	mutate("setUserToken(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, setUserToken(c, tokenChoice{account: c.Caller, token: a[0].addr()})
	}),
	mutate("distributeFees(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, DistributeFees(c, FeeClaim{Validator: a[0].addr(), Token: a[1].addr()})
	}),
	view("M()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retWord(ammM), nil }),
	view("N()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retWord(ammN), nil }),
	view("SCALE()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retWord(ammScale), nil }),
	view("MIN_LIQUIDITY()", nil, func(c *Context, a []abiValue) ([]byte, error) { return retWord(ammMinLiquidity), nil }),
	view("getPoolId(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return encodeWords(c.poolID(poolKey{user: a[0].addr(), validator: a[1].addr()})), nil
	}),
	view("getPool(address,address)", []abiType{tAddress, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		p, err := c.readPool(c.poolID(poolKey{user: a[0].addr(), validator: a[1].addr()}))
		return poolWords(p), err
	}),
	view("pools(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		p, err := c.readPool(a[0].bytes32())
		return poolWords(p), err
	}),
	view("totalSupply(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(FeeManagerAddress, c.totalSupplySlot(a[0].bytes32()))
		return retWord(w), err
	}),
	view("liquidityBalances(bytes32,address)", []abiType{tB32, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.sload(FeeManagerAddress, c.liquiditySlot(a[0].bytes32(), a[1].addr()))
		return retWord(w), err
	}),
	mutate("mint(address,address,uint256,address)", ammArgTypes, func(c *Context, a []abiValue) ([]byte, error) {
		l, err := ammMint(c, ammArgs(c, a))
		return retWord(l), err
	}),
	mutate("burn(address,address,uint256,address)", ammArgTypes, func(c *Context, a []abiValue) ([]byte, error) {
		out, err := ammBurn(c, ammArgs(c, a))
		return encodeWords(toHash(out.user), toHash(out.validator)), err
	}),
	mutate("rebalanceSwap(address,address,uint256,address)", ammArgTypes, func(c *Context, a []abiValue) ([]byte, error) {
		in, err := rebalanceSwap(c, ammArgs(c, a))
		return retWord(in), err
	}),
))
