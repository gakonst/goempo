package executor

import (
	"fmt"

	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type CheckRequest struct {
	Header      *types.Header
	Tx          *types.Transaction
	Sender      Sender
	FutureNonce bool
}

type TxInfo struct {
	Sender      Address
	FeePayer    Address
	FeeToken    Address
	GasLimit    uint64
	MaxFee      u256.Int
	Priority    u256.Int
	Nonce       uint64
	NonceKey    u256.Int
	ExpiryNonce bool
	StateNonce  uint64
	ValidAfter  uint64
	ValidBefore uint64
}

func (i *TxInfo) EffectiveTip(baseFee uint64) (u256.Int, bool) {
	fee := u256.FromUint64(baseFee)
	if i.MaxFee.Lt(fee) {
		return u256.Int{}, false
	}
	room := i.MaxFee.Sub(fee)
	if i.Priority.Lt(room) {
		return i.Priority, true
	}
	return room, true
}

func (i *TxInfo) Outbids(old *TxInfo, bumpPercent uint64) bool {
	return !i.MaxFee.Lt(bumped(old.MaxFee, bumpPercent)) && !i.Priority.Lt(bumped(old.Priority, bumpPercent))
}

func bumped(v u256.Int, pct uint64) u256.Int {
	const whole = 100
	return v.Mul(u256.FromUint64(whole + pct)).Div(u256.FromUint64(whole))
}

func (x *Executor) Check(st State, req *CheckRequest) (TxInfo, error) {
	assert.Ok(req.Header != nil, "a check needs a header")
	header, tx, sender := req.Header, req.Tx, req.Sender
	if IsSystemTx(tx) {
		return TxInfo{}, ErrSystemTx
	}
	rules := chain.RulesAt(x.ChainID, header.Inner.Timestamp)
	set := precompiles.NewSet(rules.Fork, x.Configs.EthereumPrecompiles(rules))
	env := &TxEnv{
		State:  st,
		Rules:  rules,
		Config: x.Configs.Config(rules, set),
		Block:  blockContext(header, func(uint64) Hash { return Hash{} }),
		Tx:     tx,
		Sender: &sender,
	}
	v, err := viewOf(tx, nil, &sender)
	if err != nil {
		return TxInfo{}, err
	}
	info := TxInfo{
		Sender: v.sender, FeePayer: v.feePayer, GasLimit: v.gasLimit, MaxFee: v.maxFee, Priority: v.priority,
		Nonce: v.nonce, NonceKey: v.nonceKey, ValidAfter: v.validAfter, ValidBefore: v.validBefore,
	}
	info.ExpiryNonce = v.isAA() && isExpiring(&v, rules)
	r := &txRun{env: env, set: set, view: v}
	if err := r.validate(); err != nil {
		return info, err
	}
	snapshot := st.Snapshot()
	defer func() {
		st.RevertToSnapshot(snapshot)
		st.FinalizeTx()
	}()
	r.ctx = r.protocolContext()
	if !info.ExpiryNonce {
		if info.StateNonce, err = r.stateNonce(); err != nil {
			return info, err
		}
		if info.StateNonce > v.nonce {
			return info, fmt.Errorf("nonce: %w: state %d, tx %d", ErrNonce, info.StateNonce, v.nonce)
		}
		if !req.FutureNonce && info.StateNonce != v.nonce {
			return info, fmt.Errorf("nonce: %w: state %d, tx %d", ErrNonce, info.StateNonce, v.nonce)
		}
		env.Sim = &Simulation{Sender: v.sender, SkipNonceCheck: true}
	}
	if err := r.applyNonce(); err != nil {
		return info, fmt.Errorf("nonce: %w", err)
	}
	env.Sim = nil
	if err := r.chargeFee(); err != nil {
		return info, fmt.Errorf("fee: %w", err)
	}
	info.FeeToken = r.token
	if err := r.applyKeyAuthorization(); err != nil {
		return info, fmt.Errorf("key authorization: %w", err)
	}
	return info, nil
}

func (r *txRun) stateNonce() (uint64, error) {
	v := &r.view
	if v.isAA() && !v.nonceKey.IsZero() {
		return precompiles.GetNonce(r.ctx, v.sender, v.nonceKey)
	}
	return r.env.State.Nonce(v.sender), nil
}

func (x *Executor) LaneNonce(st State, fork hardfork.Fork, sender Address, key u256.Int) (uint64, error) {
	if key.IsZero() {
		return st.Nonce(sender), nil
	}
	c := precompiles.NewContext(st, fork, protocolCallGas)
	return precompiles.GetNonce(c, sender, key)
}
