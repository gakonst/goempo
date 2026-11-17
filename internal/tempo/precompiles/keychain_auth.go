package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	SigSecp256k1 uint8 = iota
	SigP256
	SigWebAuthn
	SigConfigurableAccount
)

var (
	evKeyAuthorizedV2 = evKeyAuthorized
	fScoped           = field{offset: 0, size: 1}
)

type TokenLimit struct {
	Token  Address
	Amount Word
	Period uint64
}

type SelectorRule struct {
	Selector   [4]byte
	Recipients []Address
}

type CallScope struct {
	Target        Address
	SelectorRules []SelectorRule
}

type KeyRestrictions struct {
	Expiry        uint64
	EnforceLimits bool
	Limits        []TokenLimit
	AllowAnyCalls bool
	AllowedCalls  []CallScope
}

type KeyGrant struct {
	Account      Address
	KeyID        Address
	SigType      uint8
	Admin        bool
	Restrictions KeyRestrictions
	Witness      *Hash
}

func (c *Context) ensureAdminCaller(sender Address) error {
	txKey, err := c.transactionKey()
	if err != nil {
		return err
	}
	if txKey != (Address{}) {
		admin := false
		if c.at(hardfork.T6) {
			admin, err = c.isAdminKey(keyRef{account: sender, keyID: txKey})
			if err != nil {
				return err
			}
		}
		if !admin {
			return errSel("UnauthorizedCaller()")
		}
	}
	if !c.at(hardfork.T2) {
		return nil
	}
	origin, err := c.txOrigin()
	if err != nil {
		return err
	}
	if origin == (Address{}) || origin != sender {
		return errSel("UnauthorizedCaller()")
	}
	return nil
}

func (c *Context) isAdminKey(k keyRef) (bool, error) {
	if k.keyID == k.account {
		return true, nil
	}
	key, err := c.loadActiveKey(k)
	if err != nil {
		if isRevert(err) {
			return false, nil
		}
		return false, err
	}
	return key.admin, nil
}

func (c *Context) witnessSlot(account Address, witness Hash) slot {
	return c.mapKey(witness, c.mapAddr(account, slotKeychainWitness))
}

func (c *Context) checkGrant(g KeyGrant) error {
	if err := c.ensureAdminCaller(g.Account); err != nil {
		return err
	}
	if g.KeyID == (Address{}) {
		return errSel("ZeroPublicKey()")
	}
	if g.Admin && g.KeyID == g.Account {
		return errSel("InvalidKeyId()")
	}
	if g.Restrictions.Expiry <= c.timestamp() {
		return errSel("ExpiryInPast()")
	}
	w, err := c.sload(AccountKeychainAddress, c.keySlot(keyRef{account: g.Account, keyID: g.KeyID}))
	if err != nil {
		return err
	}
	existing := decodeAuthorizedKey(w)
	if existing.expiry > 0 {
		return errSel("KeyAlreadyExists()")
	}
	if existing.revoked {
		return errSel("KeyAlreadyRevoked()")
	}
	if g.SigType > SigConfigurableAccount {
		return errSel("InvalidSignatureType()")
	}
	if g.SigType == SigConfigurableAccount && !c.at(hardfork.T14) {
		return errSel("InvalidSignatureType()")
	}
	return c.checkRestrictions(g.Restrictions)
}

func (c *Context) checkRestrictions(r KeyRestrictions) error {
	if !c.at(hardfork.T3) {
		for _, l := range r.Limits {
			if l.Period != 0 {
				return errSel("InvalidSpendingLimit()")
			}
		}
		if !r.AllowAnyCalls || len(r.AllowedCalls) > 0 {
			return errSel("InvalidCallScope()")
		}
		return nil
	}
	if r.EnforceLimits {
		for i := range r.Limits {
			for j := 0; j < i; j++ {
				if r.Limits[j].Token == r.Limits[i].Token {
					return errSel("InvalidSpendingLimit()")
				}
			}
		}
	}
	if r.AllowAnyCalls && c.at(hardfork.T5) && len(r.AllowedCalls) > 0 {
		return errSel("InvalidCallScope()")
	}
	return nil
}

func AuthorizeKey(c *Context, g KeyGrant) error {
	if g.Admin && c.at(hardfork.T14) {
		return errSel("InvalidKeyId()")
	}
	if err := c.checkGrant(g); err != nil {
		return err
	}
	if g.Witness != nil {
		burned, err := c.loadBool(AccountKeychainAddress, c.witnessSlot(g.Account, *g.Witness))
		if err != nil {
			return err
		}
		if burned {
			return errSel("KeyAuthorizationWitnessAlreadyBurned()")
		}
	}
	r := g.Restrictions
	if g.Admin {
		r = KeyRestrictions{Expiry: ^uint64(0), AllowAnyCalls: true}
	}
	k := keyRef{account: g.Account, keyID: g.KeyID}
	key := authorizedKey{sigType: g.SigType, expiry: r.Expiry, enforceLimits: r.EnforceLimits, admin: g.Admin}
	if err := c.sstore(AccountKeychainAddress, c.keySlot(k), encodeAuthorizedKey(key)); err != nil {
		return err
	}
	if !g.Admin {
		if err := c.applyRestrictions(k, r); err != nil {
			return err
		}
	}
	if g.Witness != nil {
		if err := c.emit(AccountKeychainAddress, []Hash{evKeyAuthWitness, addressWord(g.Account), *g.Witness}, nil); err != nil {
			return err
		}
	}
	data := encodeWords(wordOfU64(uint64(g.SigType)), wordOfU64(r.Expiry))
	if err := c.emit(AccountKeychainAddress, []Hash{evKeyAuthorizedV2, addressWord(g.Account), addressWord(g.KeyID)}, data); err != nil {
		return err
	}
	if !g.Admin {
		return nil
	}
	return c.emit(AccountKeychainAddress, []Hash{evAdminKeyAuthorized, addressWord(g.Account), addressWord(g.KeyID)}, nil)
}

func (c *Context) applyRestrictions(k keyRef, r KeyRestrictions) error {
	t3 := c.at(hardfork.T3)
	if r.EnforceLimits {
		for _, l := range r.Limits {
			if err := c.writeTokenLimit(k, l); err != nil {
				return err
			}
		}
	}
	if !t3 {
		return nil
	}
	if r.AllowAnyCalls {
		return c.replaceAllowedCalls(c.spendingLimitKey(k), nil)
	}
	scopes := r.AllowedCalls
	if scopes == nil {
		scopes = []CallScope{}
	}
	return c.replaceAllowedCalls(c.spendingLimitKey(k), scopes)
}

func (c *Context) writeTokenLimit(k keyRef, l TokenLimit) error {
	s := c.limitSlot(k, l.Token)
	if !c.at(hardfork.T3) {
		return c.sstore(AccountKeychainAddress, s, l.Amount)
	}
	if l.Amount.Gt(u128Max) {
		return errSel("InvalidSpendingLimit()")
	}
	end := uint64(0)
	if l.Period != 0 {
		end = uint64(sat(c.timestamp()).add(l.Period))
	}
	return c.writeLimit(s, spendingLimit{remaining: l.Amount, max: l.Amount, period: l.Period, periodEnd: end})
}

func (c *Context) scopeBase(accountKey Hash) slot { return c.mapKey(accountKey, slotKeychainScopes) }

func (c *Context) replaceAllowedCalls(accountKey Hash, scopes []CallScope) error {
	base := c.scopeBase(accountKey)
	if err := c.clearAllTargetScopes(base); err != nil {
		return err
	}
	if err := c.sstore(AccountKeychainAddress, base, flag(scopes != nil).word()); err != nil {
		return err
	}
	if len(scopes) == 0 {
		return nil
	}
	return c.writeCallScopes(base, scopes)
}

var _ = u256.One

type TokenFlow struct {
	Account Address
	Token   Address
	Amount  Word
}

func AuthorizeTransfer(c *Context, f TokenFlow) error {
	return c.authorizeTransfer(tokenFlow{account: f.Account, token: f.Token, amount: f.Amount})
}
