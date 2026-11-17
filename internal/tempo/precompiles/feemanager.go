package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotFeeValidatorTokens = slotOf(0)
	slotFeeUserTokens      = slotOf(1)
	slotFeeCollected       = slotOf(2)
	slotFeePools           = slotOf(3)
	slotFeeTotalSupply     = slotOf(4)
	slotFeeLiquidity       = slotOf(5)
	slotFeeReservation     = slotOf(6)
	slotFeeTwoHop          = slotOf(7)
	fReserveUser           = field{offset: 0, size: 16}
	fReserveValidator      = field{offset: 16, size: 16}
)

var (
	ammM            = u256.FromUint64(9970)
	ammN            = u256.FromUint64(9985)
	ammScale        = u256.FromUint64(10000)
	ammMinLiquidity = u256.FromUint64(1000)
	swapRatio       = ratio{num: ammM, den: ammScale}
)

type feeRoute uint8

const (
	routeNone feeRoute = iota
	routeSameToken
	routeDirect
	routeTwoHop
)

type pool struct {
	reserveUser      Word
	reserveValidator Word
}

type poolKey struct {
	user      Address
	validator Address
}

func computeAmountOut(in Word) (Word, error) { return swapRatio.of(in) }

func (c *Context) poolID(k poolKey) Hash {
	return c.keccak(encodeWords(addressWord(k.user), addressWord(k.validator)))
}

func (c *Context) poolSlot(id Hash) slot { return c.mapKey(id, slotFeePools) }

func (c *Context) readPool(id Hash) (pool, error) {
	w, err := c.sload(FeeManagerAddress, c.poolSlot(id))
	if err != nil {
		return pool{}, err
	}
	return pool{reserveUser: fReserveUser.of(w), reserveValidator: fReserveValidator.of(w)}, nil
}

func (c *Context) writePool(id Hash, p pool) error {
	w := pack(Word{}).with(fReserveUser, p.reserveUser).with(fReserveValidator, p.reserveValidator).w
	return c.storePackedGroup(FeeManagerAddress, c.poolSlot(id), w, fReserveUser, fReserveValidator)
}

func (p pool) hasEnoughValidator(amount Word) bool {
	return !p.reserveValidator.IsZero() && !amount.Gt(p.reserveValidator)
}

func UserToken(c *Context, user Address) (Address, error) {
	return c.loadAddress(FeeManagerAddress, c.mapAddr(user, slotFeeUserTokens))
}

func ValidatorToken(c *Context, beneficiary Address) (Address, error) {
	t, err := c.loadAddress(FeeManagerAddress, c.mapAddr(beneficiary, slotFeeValidatorTokens))
	if err != nil || t != (Address{}) {
		return t, err
	}
	return PathUSDAddress, nil
}

func (c *Context) tip20(addr Address) (Token, error) {
	if !IsTIP20(addr) {
		return Token{}, errSel("InvalidToken()")
	}
	return TIP20(c, addr), nil
}

func (c *Context) validateUSD(addr Address) error {
	t, err := c.tip20(addr)
	if err != nil {
		return err
	}
	cur, err := t.currency()
	if err != nil {
		return err
	}
	if string(cur) != string(usdCurrency) {
		return errSel("InvalidCurrency()")
	}
	return nil
}

type FeeCollection struct {
	FeePayer           Address
	Token              Address
	MaxAmount          Word
	Beneficiary        Address
	SkipLiquidityCheck bool
}

func CollectFeePreTx(c *Context, f FeeCollection) (Address, error) {
	validator, err := ValidatorToken(c, f.Beneficiary)
	if err != nil {
		return Address{}, err
	}
	t, err := c.tip20(f.Token)
	if err != nil {
		return Address{}, err
	}
	if c.at(hardfork.T8) {
		err = t.ensureAuthorizedAs(party{user: f.FeePayer, role: roleSender})
	} else {
		err = t.ensureTransferAuthorized(hop{from: f.FeePayer, to: FeeManagerAddress})
	}
	if err != nil {
		return Address{}, err
	}
	if err := t.transferFeePreTx(f.FeePayer, f.MaxAmount); err != nil {
		return Address{}, err
	}
	if f.SkipLiquidityCheck {
		return f.Token, nil
	}
	plan, err := c.planFeeRoute(poolKey{user: f.Token, validator: validator}, f.MaxAmount)
	if err != nil {
		return Address{}, err
	}
	if plan.route == routeNone {
		return Address{}, errSel("InsufficientLiquidity()")
	}
	return f.Token, c.reserveFeeLiquidity(plan, f.MaxAmount)
}

func (c *Context) reservePool(id Hash, amount Word) error {
	return c.tstore(FeeManagerAddress, c.mapKey(id, slotFeeReservation), amount)
}

func (c *Context) reservation(id Hash) (Word, error) {
	w, err := c.tload(FeeManagerAddress, c.mapKey(id, slotFeeReservation))
	return fU128.of(w), err
}

func outU128(in Word) (Word, error) {
	out, err := computeAmountOut(in)
	if err != nil {
		return Word{}, err
	}
	if out.Gt(u128Max) {
		return Word{}, underOverflow()
	}
	return out, nil
}

type routePlan struct {
	route feeRoute
	key   poolKey
	mid   Address
}

func (c *Context) reserveFeeLiquidity(p routePlan, maxAmount Word) error {
	if p.route == routeSameToken {
		return nil
	}
	if p.route == routeDirect && !c.at(hardfork.T1C) {
		return nil
	}
	out1, err := outU128(maxAmount)
	if err != nil {
		return err
	}
	if p.route == routeDirect {
		return c.reservePool(c.poolID(p.key), out1)
	}
	out2, err := outU128(out1)
	if err != nil {
		return err
	}
	if err := c.reservePool(c.poolID(poolKey{user: p.key.user, validator: p.mid}), out1); err != nil {
		return err
	}
	if err := c.reservePool(c.poolID(poolKey{user: p.mid, validator: p.key.validator}), out2); err != nil {
		return err
	}
	return c.tstore(FeeManagerAddress, slotFeeTwoHop, toWord(addressWord(p.mid)))
}

func (c *Context) planFeeRoute(k poolKey, maxAmount Word) (routePlan, error) {
	plan := routePlan{route: routeNone, key: k}
	if k.user == k.validator {
		plan.route = routeSameToken
		return plan, nil
	}
	out, err := computeAmountOut(maxAmount)
	if err != nil {
		return plan, err
	}
	direct, err := c.readPool(c.poolID(k))
	if err != nil {
		return plan, err
	}
	if direct.hasEnoughValidator(out) {
		plan.route = routeDirect
		return plan, nil
	}
	if !c.at(hardfork.T5) {
		return plan, nil
	}
	return c.planTwoHop(plan, out)
}

func (c *Context) planTwoHop(plan routePlan, out Word) (routePlan, error) {
	mid, err := TIP20(c, plan.key.user).quoteToken()
	if err != nil || mid == (Address{}) || mid == plan.key.validator {
		return plan, err
	}
	plan.mid = mid
	leg1, err := c.readPool(c.poolID(poolKey{user: plan.key.user, validator: mid}))
	if err != nil || !leg1.hasEnoughValidator(out) {
		return plan, err
	}
	out2, err := computeAmountOut(out)
	if err != nil {
		return plan, err
	}
	leg2, err := c.readPool(c.poolID(poolKey{user: mid, validator: plan.key.validator}))
	if err != nil || !leg2.hasEnoughValidator(out2) {
		return plan, err
	}
	plan.route = routeTwoHop
	return plan, nil
}

func (c *Context) executeFeeSwap(k poolKey, in Word) (Word, error) {
	out, err := computeAmountOut(in)
	if err != nil {
		return Word{}, err
	}
	id := c.poolID(k)
	p, err := c.readPool(id)
	if err != nil {
		return Word{}, err
	}
	if out.Gt(p.reserveValidator) {
		return Word{}, errSel("InsufficientLiquidity()")
	}
	if in.Gt(u128Max) || out.Gt(u128Max) {
		return Word{}, underOverflow()
	}
	nu := p.reserveUser.Add(in)
	if nu.Gt(u128Max) {
		return Word{}, underOverflow()
	}
	p.reserveUser = nu
	p.reserveValidator = p.reserveValidator.Sub(out)
	return out, c.writePool(id, p)
}

type FeeSettlement struct {
	FeePayer    Address
	Actual      Word
	Refund      Word
	FeeToken    Address
	Beneficiary Address
}

func CollectFeePostTx(c *Context, f FeeSettlement) (Word, error) {
	t, err := c.tip20(f.FeeToken)
	if err != nil {
		return Word{}, err
	}
	if err := t.transferFeePostTx(feeReturn{to: f.FeePayer, refund: f.Refund, actual: f.Actual}); err != nil {
		return Word{}, err
	}
	hopW, err := c.tload(FeeManagerAddress, slotFeeTwoHop)
	if err != nil {
		return Word{}, err
	}
	validator, err := ValidatorToken(c, f.Beneficiary)
	if err != nil {
		return Word{}, err
	}
	amount, err := c.swapFees(routePlan{key: poolKey{user: f.FeeToken, validator: validator}, mid: wordAddress(toHash(hopW))}, f.Actual)
	if err != nil || amount.IsZero() {
		return amount, err
	}
	s := c.mapAddr(validator, c.mapAddr(f.Beneficiary, slotFeeCollected))
	cur, err := c.sload(FeeManagerAddress, s)
	if err != nil {
		return Word{}, err
	}
	next, err := num(cur).add(amount)
	if err != nil {
		return Word{}, err
	}
	return amount, c.sstore(FeeManagerAddress, s, next)
}

func (c *Context) swapFees(p routePlan, actual Word) (Word, error) {
	if p.key.user == p.key.validator {
		return actual, nil
	}
	if p.mid == (Address{}) {
		if !actual.IsZero() {
			if _, err := c.executeFeeSwap(p.key, actual); err != nil {
				return Word{}, err
			}
		}
		return computeAmountOut(actual)
	}
	if !actual.IsZero() {
		out1, err := c.executeFeeSwap(poolKey{user: p.key.user, validator: p.mid}, actual)
		if err != nil {
			return Word{}, err
		}
		if _, err := c.executeFeeSwap(poolKey{user: p.mid, validator: p.key.validator}, out1); err != nil {
			return Word{}, err
		}
	}
	o1, err := computeAmountOut(actual)
	if err != nil {
		return Word{}, err
	}
	return computeAmountOut(o1)
}

type FeeClaim struct {
	Validator Address
	Token     Address
}

func DistributeFees(c *Context, f FeeClaim) error {
	c.creditMinting = false
	s := c.mapAddr(f.Token, c.mapAddr(f.Validator, slotFeeCollected))
	amount, err := c.sload(FeeManagerAddress, s)
	if err != nil || amount.IsZero() {
		return err
	}
	if err := c.sstore(FeeManagerAddress, s, Word{}); err != nil {
		return err
	}
	t, err := c.tip20(f.Token)
	if err != nil {
		return err
	}
	if err := t.transfer(transferReq{from: FeeManagerAddress, to: f.Validator, amount: amount}); err != nil {
		return err
	}
	return c.emit(FeeManagerAddress, []Hash{evFeesDistributed, addressWord(f.Validator), addressWord(f.Token)}, encodeWords(toHash(amount)))
}

func IsValidFeeToken(c *Context, addr Address) (bool, error) {
	ok, err := IsTIP20Deployed(c, addr)
	if err != nil || !ok {
		return false, err
	}
	cur, err := TIP20(c, addr).currency()
	return string(cur) == string(usdCurrency), err
}

func (c *Context) requireDeployed(addr Address) error {
	ok, err := IsTIP20Deployed(c, addr)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("InvalidToken()")
	}
	return nil
}

type tokenChoice struct {
	account Address
	token   Address
}

func setValidatorToken(c *Context, ch tokenChoice) error {
	if err := c.requireDeployed(ch.token); err != nil {
		return err
	}
	if ch.account == c.Block.Coinbase {
		return errSel("CannotChangeWithinBlock()")
	}
	if err := c.validateUSD(ch.token); err != nil {
		return err
	}
	if err := c.storeAddress(FeeManagerAddress, c.mapAddr(ch.account, slotFeeValidatorTokens), ch.token); err != nil {
		return err
	}
	return c.emit(FeeManagerAddress, []Hash{evValidatorTokenSet, addressWord(ch.account), addressWord(ch.token)}, nil)
}

func setUserToken(c *Context, ch tokenChoice) error {
	if err := c.requireDeployed(ch.token); err != nil {
		return err
	}
	if err := c.validateUSD(ch.token); err != nil {
		return err
	}
	s := c.mapAddr(ch.account, slotFeeUserTokens)
	if c.at(hardfork.T3) {
		cur, err := c.loadAddress(FeeManagerAddress, s)
		if err != nil || cur == ch.token {
			return err
		}
	}
	if err := c.storeAddress(FeeManagerAddress, s, ch.token); err != nil {
		return err
	}
	return c.emit(FeeManagerAddress, []Hash{evUserTokenSet, addressWord(ch.account), addressWord(ch.token)}, nil)
}
