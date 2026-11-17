package executor

import (
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
)

func keyGrant(account Address, a *types.KeyAuthorization) precompiles.KeyGrant {
	rs := precompiles.KeyRestrictions{
		Expiry:        a.Expiry,
		EnforceLimits: a.HasLimits,
		AllowAnyCalls: !a.HasAllowedCalls,
	}
	if rs.Expiry == 0 {
		rs.Expiry = ^uint64(0)
	}
	rs.Limits = make([]precompiles.TokenLimit, len(a.Limits))
	for i := range a.Limits {
		l := &a.Limits[i]
		rs.Limits[i] = precompiles.TokenLimit{Token: l.Token, Amount: l.Limit, Period: l.Period}
	}
	rs.AllowedCalls = make([]precompiles.CallScope, len(a.AllowedCalls))
	for i := range a.AllowedCalls {
		sc := &a.AllowedCalls[i]
		scope := precompiles.CallScope{Target: sc.Target, SelectorRules: make([]precompiles.SelectorRule, len(sc.SelectorRules))}
		for j := range sc.SelectorRules {
			rule := &sc.SelectorRules[j]
			scope.SelectorRules[j] = precompiles.SelectorRule{Selector: rule.Selector, Recipients: rule.Recipients}
		}
		rs.AllowedCalls[i] = scope
	}
	g := precompiles.KeyGrant{Account: account, KeyID: a.KeyID, SigType: uint8(a.KeyType), Admin: a.IsAdmin, Restrictions: rs}
	if a.HasWitness {
		w := a.Witness
		g.Witness = &w
	}
	return g
}

func (r *txRun) applyKeyAuthorization() error {
	if !r.view.isAA() || r.view.aa.KeyAuthorization == nil {
		return nil
	}
	a := &r.view.aa.KeyAuthorization.Authorization
	precompiles.SetTxOrigin(r.ctx, r.view.sender)
	k := precompiles.NewContext(r.env.State, r.env.Rules.Fork, protocolCallGas)
	k.Block = r.ctx.Block
	k.Tx = r.ctx.Tx
	k.Env = r.ctx.Env
	k.ChainID = r.ctx.ChainID
	k.Caller = r.view.sender
	k.SkipStorageCredits = true
	if err := precompiles.AuthorizeKey(k, keyGrant(r.view.sender, a)); err != nil {
		return err
	}
	if r.keyID != a.KeyID || r.view.aa.Signature.Keychain == types.KeychainNone {
		return nil
	}
	precompiles.SetTransactionKey(r.ctx, a.KeyID)
	if r.maxFee.IsZero() {
		return nil
	}
	return precompiles.AuthorizeTransfer(r.ctx, precompiles.TokenFlow{Account: r.view.feePayer, Token: r.token, Amount: r.maxFee})
}
