package executor

import (
	"errors"
	"fmt"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const protocolCallGas = 1 << 62

var (
	maxWord      = u256.Max()
	feeScale     = u256.FromUint64(1_000_000_000_000)
	feeScaleLess = u256.FromUint64(999_999_999_999)
)

type txRun struct {
	env        *TxEnv
	set        *precompiles.Set
	view       txView
	price      u256.Int
	need       intrinsicGas
	ctx        *precompiles.Context
	token      Address
	maxFee     u256.Int
	keyID      Address
	keyAuthOOG bool
	machine    *evm.EVM
}

func feeAmount(gas uint64, price u256.Int) u256.Int {
	return u256.FromUint64(gas).Mul(price).Add(feeScaleLess).Div(feeScale)
}

func applyTx(env *TxEnv, set *precompiles.Set) (TxOutcome, error) {
	v, err := viewOf(env.Tx, env.Sim)
	if err != nil {
		return TxOutcome{}, err
	}
	r := &txRun{env: env, set: set, view: v}
	if err := r.validate(); err != nil {
		return TxOutcome{}, err
	}
	r.ctx = r.protocolContext()
	if err := r.applyNonce(); err != nil {
		return TxOutcome{}, fmt.Errorf("nonce: %w", err)
	}
	if err := r.chargeFee(); err != nil {
		return TxOutcome{}, fmt.Errorf("fee: %w", err)
	}
	if err := r.applyKeyAuthorization(); err != nil {
		return TxOutcome{}, fmt.Errorf("key authorization: %w", err)
	}
	if r.keyAuthOOG {
		return r.finishOutOfGas()
	}
	r.warm()
	r.applyAuthorizations()
	ran := r.execute()
	if ran.success && env.Rules.AtLeast(hardfork.T7) {
		credits, err := precompiles.SettleStorageCreditRefunds(r.ctx)
		if err != nil {
			return TxOutcome{}, fmt.Errorf("storage credits: %w", err)
		}
		env.State.AddRefund(credits)
	}
	gasUsed := r.settleGas(ran.gasLeft)
	if err := r.refundFee(gasUsed); err != nil {
		return TxOutcome{}, fmt.Errorf("fee refund: %w", err)
	}
	logs := env.State.Logs()
	if env.Sim == nil {
		env.State.FinalizeTx()
	}
	return TxOutcome{GasUsed: gasUsed, Success: ran.success, Logs: logs}, nil
}

func (r *txRun) finishOutOfGas() (TxOutcome, error) {
	gasUsed := r.view.gasLimit
	if err := r.refundFee(gasUsed); err != nil {
		return TxOutcome{}, fmt.Errorf("fee refund: %w", err)
	}
	logs := r.env.State.Logs()
	if r.env.Sim == nil {
		r.env.State.FinalizeTx()
	}
	return TxOutcome{GasUsed: gasUsed, Success: false, Logs: logs}, nil
}

func (r *txRun) skipNonce() bool {
	return r.env.Sim != nil && r.env.Sim.SkipNonceCheck
}

func (r *txRun) skipBalance() bool {
	return r.env.Sim != nil && r.env.Sim.SkipBalanceCheck
}

func (r *txRun) zeroPriceCall() bool {
	return r.env.Sim != nil && r.view.maxFee.IsZero()
}

func (r *txRun) validate() error {
	v := &r.view
	rules := r.env.Rules
	for i := 0; i < len(v.calls); i++ {
		if !v.calls[i].value.IsZero() {
			return ErrValue
		}
	}
	if v.gasLimit > rules.TxGasLimitCap() {
		return ErrGasCap
	}
	baseFee := r.env.Block.BaseFee
	if v.maxFee.Lt(baseFee) && !r.zeroPriceCall() {
		return ErrFeeCap
	}
	if v.isAA() {
		now := r.env.Block.Time
		if v.validAfter > now {
			return ErrValidWindow
		}
		if v.validBefore != 0 && now >= v.validBefore {
			return ErrValidWindow
		}
	}
	r.price = v.effectiveGasPrice(baseFee)
	r.need = intrinsic(v, rules)
	if v.gasLimit < max(r.need.gas, r.need.floor) {
		return ErrIntrinsic
	}
	return nil
}

func (r *txRun) protocolContext() *precompiles.Context {
	c := precompiles.NewContext(r.env.State, r.env.Rules.Fork, protocolCallGas)
	c.Block = r.env.Block
	c.Tx = &evm.TxContext{Origin: r.view.sender, GasPrice: r.price}
	c.Env = r.set.Env
	c.ChainID = r.env.Config.ChainID
	c.Caller = r.view.sender
	return c
}

func (r *txRun) applyNonce() error {
	v := &r.view
	st := r.env.State
	if v.isAA() && isExpiring(v, r.env.Rules) {
		h := r.env.Tx.Hash()
		if r.env.Rules.AtLeast(hardfork.T1B) {
			h = r.env.Tx.UniqueID(v.sender)
		}
		return precompiles.CheckAndMarkExpiringNonce(r.ctx, h, v.validBefore)
	}
	if v.isAA() && !v.nonceKey.IsZero() {
		got, err := precompiles.GetNonce(r.ctx, v.sender, v.nonceKey)
		if err != nil {
			return err
		}
		if got != v.nonce && !r.skipNonce() {
			return fmt.Errorf("%w: key nonce %d, tx %d", ErrNonce, got, v.nonce)
		}
		_, err = precompiles.IncrementNonce(r.ctx, v.sender, v.nonceKey)
		return err
	}
	if got := st.Nonce(v.sender); got != v.nonce && !r.skipNonce() {
		return fmt.Errorf("%w: account nonce %d, tx %d", ErrNonce, got, v.nonce)
	}
	if !v.isCreate() {
		st.SetNonce(v.sender, v.nonce+1)
	}
	return nil
}

func (r *txRun) chargeFee() error {
	token, err := r.resolveFeeToken()
	if err != nil {
		return err
	}
	r.token = token
	r.maxFee = feeAmount(r.view.gasLimit, r.price)
	if err := r.checkFeeToken(); err != nil {
		return err
	}
	r.set.Env.FeePayer = r.view.feePayer
	r.set.Env.FeeToken = token
	r.set.Env.NonCreditable = nil
	precompiles.SetTxOrigin(r.ctx, r.view.sender)
	if err := r.useAccessKey(); err != nil {
		return err
	}
	if r.maxFee.IsZero() {
		return nil
	}
	_, err = precompiles.CollectFeePreTx(r.ctx, precompiles.FeeCollection{
		FeePayer:    r.view.feePayer,
		Token:       token,
		MaxAmount:   r.maxFee,
		Beneficiary: r.env.Block.Coinbase,
	})
	if err != nil {
		return err
	}
	if r.env.Rules.AtLeast(hardfork.T7) {
		precompiles.MarkFeeSlotsNonCreditable(r.ctx, precompiles.FeeSlots{
			FeePayer: r.view.feePayer,
			FeeToken: token,
			KeyID:    r.keyID,
		})
	}
	return nil
}

func (r *txRun) useAccessKey() error {
	if !r.view.isAA() || r.view.aa.Signature.Keychain == types.KeychainNone {
		return nil
	}
	sig := &r.view.aa.Signature
	key, err := sig.KeyID(r.env.Tx.SigningHash())
	if err != nil {
		return err
	}
	r.keyID = key
	if auth := r.view.aa.KeyAuthorization; auth != nil && auth.Authorization.KeyID == key {
		return nil
	}
	_, err = precompiles.ValidateKeychainAuthorization(r.ctx, precompiles.KeyUse{
		Account: r.view.sender,
		KeyID:   key,
		SigType: uint8(sig.Inner.Kind),
		Check:   r.env.Rules.AtLeast(hardfork.T1),
	})
	if err != nil {
		return err
	}
	precompiles.SetTransactionKey(r.ctx, key)
	return nil
}

func (r *txRun) checkFeeToken() error {
	if !precompiles.IsTIP20(r.token) {
		return ErrFeeToken
	}
	limit := feeAmount(r.view.gasLimit, r.view.maxFee)
	if limit.IsZero() {
		_, err := precompiles.TIP20(r.ctx, r.token).BalanceOf(r.view.feePayer)
		return err
	}
	valid, err := precompiles.IsValidFeeToken(r.ctx, r.token)
	if err != nil {
		return err
	}
	if !valid {
		return ErrFeeToken
	}
	balance, err := precompiles.TIP20(r.ctx, r.token).BalanceOf(r.view.feePayer)
	if err != nil {
		return err
	}
	if balance.Lt(limit) && !r.skipBalance() {
		return ErrFeeBalance
	}
	return nil
}

func (r *txRun) warm() {
	st := r.env.State
	v := &r.view
	st.WarmAddress(v.sender)
	for i := 0; i < len(v.calls); i++ {
		if v.calls[i].to != nil {
			st.WarmAddress(*v.calls[i].to)
		}
	}
	base := r.env.Rules.EthereumPrecompiles().Addresses()
	for i := 0; i < len(base); i++ {
		st.WarmAddress(base[i])
	}
	st.WarmAddress(r.env.Block.Coinbase)
	for i := 0; i < len(v.accessList); i++ {
		t := &v.accessList[i]
		st.WarmAddress(t.Address)
		for j := 0; j < len(t.StorageKeys); j++ {
			st.WarmSlot(t.Address, t.StorageKeys[j])
		}
	}
}

func (r *txRun) applyAuthorizations() {
	st := r.env.State
	chainID := r.env.Config.ChainID
	for i := 0; i < len(r.view.auths); i++ {
		a := &r.view.auths[i]
		if !authorizationApplies(a, chainID) {
			continue
		}
		st.WarmAddress(a.signer)
		code := st.Code(a.signer)
		if _, delegated := evm.DelegationTarget(code); len(code) != 0 && !delegated {
			continue
		}
		if st.Nonce(a.signer) != a.nonce {
			continue
		}
		if st.Exist(a.signer) {
			st.AddRefund(int64(r.env.Rules.AuthorizationRefund()))
		}
		if a.address.IsZero() {
			st.SetCode(a.signer, nil)
		} else {
			st.SetCode(a.signer, evm.DelegationCode(a.address))
		}
		st.SetNonce(a.signer, a.nonce+1)
	}
}

func authorizationApplies(a *authorization, chainID u256.Int) bool {
	if !a.valid || a.nonce == ^uint64(0) {
		return false
	}
	return a.chainID.IsZero() || a.chainID == chainID
}

type execution struct {
	success bool
	gasLeft uint64
}

func (r *txRun) execute() execution {
	st := r.env.State
	v := &r.view
	machine := evm.New(evm.Context{
		State:  st,
		Block:  r.env.Block,
		Tx:     &evm.TxContext{Origin: v.sender, GasPrice: r.price},
		Config: r.env.Config,
	})
	gasLeft := v.gasLimit - r.need.gas
	snapshot := st.Snapshot()
	for i := 0; i < len(v.calls); i++ {
		c := &v.calls[i]
		msg := evm.Message{Kind: evm.KindCall, Caller: v.sender, Value: c.value, Input: c.input, Gas: gasLeft}
		if c.to == nil {
			msg.Kind = evm.KindCreate
		} else {
			msg.To = *c.to
		}
		res := machine.Execute(msg)
		gasLeft = res.GasLeft
		if !res.Status.Success() {
			if len(v.calls) > 1 {
				st.RevertToSnapshot(snapshot)
			}
			return execution{success: false, gasLeft: gasLeft}
		}
	}
	return execution{success: true, gasLeft: gasLeft}
}

func (r *txRun) settleGas(gasLeft uint64) uint64 {
	gasUsed := r.view.gasLimit - gasLeft
	refund := r.env.State.Refund()
	capped := gasUsed / r.env.Config.Gas.MaxRefundQuotient
	if refund < 0 || uint64(refund) > capped {
		gasUsed -= capped
	} else {
		gasUsed -= uint64(refund)
	}
	return max(gasUsed, r.need.floor)
}

func (r *txRun) refundFee(gasUsed uint64) error {
	actual := feeAmount(gasUsed, r.price)
	if r.maxFee.Lt(actual) {
		return errors.New("actual fee exceeds upfront fee")
	}
	refund := r.maxFee.Sub(actual)
	if r.maxFee.IsZero() {
		return nil
	}
	if actual.IsZero() && refund.IsZero() {
		return nil
	}
	_, err := precompiles.CollectFeePostTx(r.ctx, precompiles.FeeSettlement{
		FeePayer:    r.view.feePayer,
		Actual:      actual,
		Refund:      refund,
		FeeToken:    r.token,
		Beneficiary: r.env.Block.Coinbase,
	})
	return err
}
