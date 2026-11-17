package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotKeychainKeys     = slotOf(0)
	slotKeychainLimits   = slotOf(1)
	slotKeychainScopes   = slotOf(2)
	slotKeychainWitness  = slotOf(3)
	slotKeychainTxKey    = slotOf(4)
	slotKeychainTxOrigin = slotOf(5)
)

var (
	fKeySigType  = field{offset: 0, size: 1}
	fKeyExpiry   = field{offset: 1, size: 8}
	fKeyEnforce  = field{offset: 9, size: 1}
	fKeyRevoked  = field{offset: 10, size: 1}
	fKeyAdmin    = field{offset: 11, size: 1}
	fLimitMax    = field{offset: 0, size: 16}
	fLimitPeriod = field{offset: 16, size: 8}
	fLimitPerEnd = field{offset: 24, size: 8}
	zeroPeriodic = u256.Max()
)

type authorizedKey struct {
	sigType       uint8
	expiry        uint64
	enforceLimits bool
	revoked       bool
	admin         bool
}

const maxStoredSigType = SigConfigurableAccount

func (c *Context) readAuthorizedKey(k keyRef) (authorizedKey, error) {
	w, err := c.sload(AccountKeychainAddress, c.keySlot(k))
	if err != nil {
		return authorizedKey{}, err
	}
	key := decodeAuthorizedKey(w)
	if fKeySigType.u64(w) > uint64(maxStoredSigType) {
		return authorizedKey{}, revert(abiError(selPanic, wordOfU64(panicEnumConversion)))
	}
	return key, nil
}

func decodeAuthorizedKey(w Word) authorizedKey {
	return authorizedKey{
		sigType:       uint8(fKeySigType.u64(w)),
		expiry:        fKeyExpiry.u64(w),
		enforceLimits: !fKeyEnforce.of(w).IsZero(),
		revoked:       !fKeyRevoked.of(w).IsZero(),
		admin:         !fKeyAdmin.of(w).IsZero(),
	}
}

func encodeAuthorizedKey(k authorizedKey) Word {
	return pack(Word{}).withU64(fKeySigType, uint64(k.sigType)).withU64(fKeyExpiry, k.expiry).
		with(fKeyEnforce, flag(k.enforceLimits).word()).with(fKeyRevoked, flag(k.revoked).word()).
		with(fKeyAdmin, flag(k.admin).word()).w
}

type spendingLimit struct {
	remaining Word
	max       Word
	period    uint64
	periodEnd uint64
}

type keyRef struct {
	account Address
	keyID   Address
}

func (c *Context) keySlot(k keyRef) slot {
	return c.mapAddr(k.keyID, c.mapAddr(k.account, slotKeychainKeys))
}

func (c *Context) spendingLimitKey(k keyRef) Hash {
	var buf [40]byte
	copy(buf[:20], k.account[:])
	copy(buf[20:], k.keyID[:])
	return c.keccak(buf[:])
}

func (c *Context) limitSlot(k keyRef, token Address) slot {
	return c.mapAddr(token, c.mapKey(c.spendingLimitKey(k), slotKeychainLimits))
}

func (c *Context) readLimit(s slot) (spendingLimit, error) {
	rem, err := c.sload(AccountKeychainAddress, s)
	if err != nil {
		return spendingLimit{}, err
	}
	p, err := c.sload(AccountKeychainAddress, s.plus(1))
	if err != nil {
		return spendingLimit{}, err
	}
	return spendingLimit{remaining: rem, max: fLimitMax.of(p), period: fLimitPeriod.u64(p), periodEnd: fLimitPerEnd.u64(p)}, nil
}

func (c *Context) writeLimit(s slot, l spendingLimit) error {
	if err := c.sstore(AccountKeychainAddress, s, l.remaining); err != nil {
		return err
	}
	w := pack(Word{}).with(fLimitMax, l.max).withU64(fLimitPeriod, l.period).withU64(fLimitPerEnd, l.periodEnd).w
	return c.storePackedGroup(AccountKeychainAddress, s.plus(1), w)
}

func (l spendingLimit) nextPeriodEnd(now uint64) uint64 {
	elapsed := sat(now).sub(l.periodEnd)
	periods := (elapsed / sat(l.period)).add(1)
	return uint64(sat(l.periodEnd).add(uint64(sat(l.period).mul(uint64(periods)))))
}

func (c *Context) timestamp() uint64 { return c.Block.Time }

func (c *Context) transactionKey() (Address, error) {
	w, err := c.tload(AccountKeychainAddress, slotKeychainTxKey)
	return wordAddress(toHash(w)), err
}

func (c *Context) txOrigin() (Address, error) {
	w, err := c.tload(AccountKeychainAddress, slotKeychainTxOrigin)
	return wordAddress(toHash(w)), err
}

func SetTransactionKey(c *Context, keyID Address) {
	c.State.SetTransientStorage(AccountKeychainAddress, Hash(slotKeychainTxKey), toWord(addressWord(keyID)))
}

func SetTxOrigin(c *Context, origin Address) {
	c.State.SetTransientStorage(AccountKeychainAddress, Hash(slotKeychainTxOrigin), toWord(addressWord(origin)))
}

func (c *Context) loadActiveKey(k keyRef) (authorizedKey, error) {
	key, err := c.readAuthorizedKey(k)
	if err != nil {
		return authorizedKey{}, err
	}
	if key.revoked {
		return key, errSel("KeyAlreadyRevoked()")
	}
	if c.at(hardfork.T14) {
		key.admin = false
	}
	if key.expiry == 0 {
		return key, errSel("KeyNotFound()")
	}
	if c.timestamp() >= key.expiry {
		return key, errSel("KeyExpired()")
	}
	return key, nil
}

type spend struct {
	key    keyRef
	token  Address
	amount Word
}

func (c *Context) verifyAndUpdateSpending(sp spend) error {
	if sp.key.keyID == (Address{}) {
		return nil
	}
	key, err := c.loadActiveKey(sp.key)
	if err != nil || !key.enforceLimits {
		return err
	}
	s := c.limitSlot(sp.key, sp.token)
	if !c.at(hardfork.T3) {
		remaining, err := c.sload(AccountKeychainAddress, s)
		if err != nil {
			return err
		}
		if sp.amount.Gt(remaining) {
			return errSel("SpendingLimitExceeded()")
		}
		return c.sstore(AccountKeychainAddress, s, remaining.Sub(sp.amount))
	}
	limit, err := c.readLimit(s)
	if err != nil {
		return err
	}
	next, err := c.consumeLimit(&limit, sp.amount)
	if err != nil {
		return err
	}
	if limit.period != 0 {
		err = c.writeLimit(s, limit)
	} else {
		err = c.sstore(AccountKeychainAddress, s, next)
	}
	if err != nil {
		return err
	}
	return c.emit(AccountKeychainAddress, []Hash{evAccessKeySpend, addressWord(sp.key.account), addressWord(sp.key.keyID), addressWord(sp.token)},
		encodeWords(toHash(sp.amount), toHash(next)))
}

func (c *Context) consumeLimit(limit *spendingLimit, amount Word) (Word, error) {
	remaining := limit.remaining
	periodic := limit.period != 0
	if periodic {
		if c.at(hardfork.T7) && remaining == zeroPeriodic {
			remaining = Word{}
		}
		if now := c.timestamp(); now >= limit.periodEnd {
			limit.periodEnd = limit.nextPeriodEnd(now)
			remaining = limit.max
		}
	}
	if amount.Gt(remaining) {
		return Word{}, errSel("SpendingLimitExceeded()")
	}
	next := remaining.Sub(amount)
	limit.remaining = next
	if periodic && c.at(hardfork.T7) && next.IsZero() {
		limit.remaining = zeroPeriodic
	}
	return next, nil
}

func (c *Context) activeTxKeyFor(account Address) (keyRef, error) {
	key, err := c.transactionKey()
	if err != nil || key == (Address{}) {
		return keyRef{}, err
	}
	origin, err := c.txOrigin()
	if err != nil || account != origin {
		return keyRef{}, err
	}
	return keyRef{account: account, keyID: key}, nil
}

type tokenFlow struct {
	account Address
	token   Address
	amount  Word
}

func (c *Context) authorizeTransfer(f tokenFlow) error {
	k, err := c.activeTxKeyFor(f.account)
	if err != nil || k.keyID == (Address{}) {
		return err
	}
	return c.verifyAndUpdateSpending(spend{key: k, token: f.token, amount: f.amount})
}

type approvalChange struct {
	owner Address
	token Address
	old   Word
	next  Word
}

func (c *Context) authorizeApprove(a approvalChange) error {
	k, err := c.activeTxKeyFor(a.owner)
	if err != nil || k.keyID == (Address{}) {
		return err
	}
	if !a.next.Gt(a.old) {
		return nil
	}
	return c.verifyAndUpdateSpending(spend{key: k, token: a.token, amount: a.next.Sub(a.old)})
}

func isRevert(err error) bool {
	_, ok := err.(*Revert)
	return ok
}

func (c *Context) refundSpendingLimit(f tokenFlow) error {
	amount := f.amount
	k, err := c.activeTxKeyFor(f.account)
	if err != nil || k.keyID == (Address{}) {
		return err
	}
	key, err := c.loadActiveKey(k)
	if err != nil {
		if !isSystemError(err) {
			return nil
		}
		return err
	}
	if !key.enforceLimits {
		return nil
	}
	s := c.limitSlot(k, f.token)
	if !c.at(hardfork.T3) {
		remaining, err := c.sload(AccountKeychainAddress, s)
		if err != nil {
			return err
		}
		return c.sstore(AccountKeychainAddress, s, num(remaining).satAdd(amount))
	}
	limit, err := c.readLimit(s)
	if err != nil {
		return err
	}
	refunded := num(limit.remaining).satAdd(amount)
	if c.at(hardfork.T7) && limit.period != 0 && limit.remaining == zeroPeriodic {
		refunded = amount
	}
	if !limit.max.IsZero() && refunded.Gt(limit.max) {
		refunded = limit.max
	}
	limit.remaining = refunded
	return c.writeLimit(s, limit)
}

type KeyUse struct {
	Account Address
	KeyID   Address
	SigType uint8
	Check   bool
}

type ActiveKey struct {
	Expiry        uint64
	EnforceLimits bool
	Admin         bool
}

func ValidateKeychainAuthorization(c *Context, u KeyUse) (ActiveKey, error) {
	key, err := c.loadActiveKey(keyRef{account: u.Account, keyID: u.KeyID})
	if err != nil {
		return ActiveKey{}, err
	}
	if u.Check && key.sigType != u.SigType {
		return ActiveKey{}, revert(abiError(selector("SignatureTypeMismatch(uint8,uint8)"), wordOfU64(uint64(key.sigType)), wordOfU64(uint64(u.SigType))))
	}
	return ActiveKey{Expiry: key.expiry, EnforceLimits: key.enforceLimits, Admin: key.admin}, nil
}
