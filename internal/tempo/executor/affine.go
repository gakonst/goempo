package executor

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
)

// Storage layout the parallel commit relies on, mirroring the precompiles:
// FeeManager validatorTokens (0) and collected fees (2), TIP-20 global
// reward per token (15) and NonceManager's expiring nonce set: seen (1),
// ring (2) and ring pointer (3).
const (
	feeValidatorTokensSlot = 0
	feeCollectedSlot       = 2
	tip20GlobalRPTSlot     = 15
	expiringSeenSlot       = 1
	expiringRingSlot       = 2
	expiringPtrSlot        = 3
	affineSlotsMax         = 16
	transferArgsLen        = 4 + 2*32
	transferMemoArgsLen    = 4 + 3*32
)

var (
	selTIP20Transfer     = selectorOf("transfer(address,uint256)")
	selTIP20TransferMemo = selectorOf("transferWithMemo(address,uint256,bytes32)")
)

func selectorOf(sig string) [4]byte {
	h := keccak.Sum256([]byte(sig))
	return [4]byte(h[:4])
}

func mapSlot(key Hash, base uint64) Hash { return mapSlotAt(key, primitives.Uint64ToHash(base)) }

func mapSlotAt(key, base Hash) Hash { return keccak.Sum256Concat(key[:], base[:]) }

func addressKey(a Address) Hash {
	var w Hash
	copy(w[12:], a[:])
	return w
}

func (r *txRun) validatorToken() Address {
	coinbase := r.env.Block.Coinbase
	raw := r.env.State.Storage(precompiles.FeeManagerAddress, mapSlot(addressKey(coinbase), feeValidatorTokensSlot))
	word := raw.Bytes32()
	var t Address
	copy(t[:], word[12:])
	if t == (Address{}) {
		return precompiles.PathUSDAddress
	}
	return t
}

// rewardsInert reports whether a TIP-20's balances move only through bound
// checks and adds: before T8 a nonzero global reward per token makes reward
// accrual multiply by a balance. The rewards code reads the slot itself, so
// the commit validates it.
func (r *txRun) rewardsInert(token Address) bool {
	if r.env.Rules.AtLeast(hardfork.T8) {
		return true
	}
	return r.env.Spec.Peek(token, primitives.Uint64ToHash(tip20GlobalRPTSlot)).IsZero()
}

func balanceRef(token, holder Address) state.Slot {
	return state.Slot{Address: token, Key: precompiles.TIP20BalanceSlot(holder)}
}

func (r *txRun) feeSlots(settle bool) []state.Slot {
	slots := make([]state.Slot, 0, affineSlotsMax)
	slots = append(slots, balanceRef(r.token, precompiles.FeeManagerAddress))
	if r.rewardsInert(r.token) {
		slots = append(slots, balanceRef(r.token, r.view.feePayer))
	}
	if settle {
		inner := mapSlot(addressKey(r.env.Block.Coinbase), feeCollectedSlot)
		slots = append(slots, state.Slot{Address: precompiles.FeeManagerAddress, Key: mapSlotAt(addressKey(r.validatorToken()), inner)})
	}
	return slots
}

// transferSlots returns the balances a transaction moves when every call is
// a plain TIP-20 transfer, so its balances can be deltas for the whole
// transaction; ok is false for any other transaction.
func (r *txRun) transferSlots() ([]state.Slot, bool) {
	v := &r.view
	if len(v.calls) == 0 || len(v.auths) > 0 || len(v.calls) > affineSlotsMax/2-2 {
		return nil, false
	}
	slots := make([]state.Slot, 0, 2*len(v.calls))
	for i := range v.calls {
		c := &v.calls[i]
		if c.to == nil || !precompiles.IsTIP20(*c.to) || !c.value.IsZero() || len(c.input) < 4 {
			return nil, false
		}
		sel := [4]byte(c.input[:4])
		okLen := (sel == selTIP20Transfer && len(c.input) == transferArgsLen) ||
			(sel == selTIP20TransferMemo && len(c.input) == transferMemoArgsLen)
		if !okLen || !r.rewardsInert(*c.to) {
			return nil, false
		}
		var to Address
		copy(to[:], c.input[4+12:4+32])
		slots = append(slots, balanceRef(*c.to, v.sender), balanceRef(*c.to, to))
	}
	return slots, true
}

// openSpan starts a whole-transaction affine window when the transaction
// only moves TIP-20 balances.
func (r *txRun) openSpan() {
	sp := r.env.Spec
	if sp == nil || r.maxFee.IsZero() {
		return
	}
	calls, ok := r.transferSlots()
	if !ok {
		return
	}
	slots := append(r.feeSlots(true), calls...)
	if len(slots) > affineSlotsMax {
		return
	}
	sp.BeginAffine(slots...)
	r.span = true
}

func (r *txRun) closeSpan() {
	if r.span {
		r.env.Spec.EndAffine()
		r.span = false
	}
}

func (r *txRun) beginAffine(settle bool) {
	if r.env.Spec == nil || r.span {
		return
	}
	r.env.Spec.BeginAffine(r.feeSlots(settle)...)
}

func (r *txRun) endAffine() {
	if r.env.Spec != nil && !r.span {
		r.env.Spec.EndAffine()
	}
}

// feeFloor records that the fee payer's balance must stay at least limit.
func (r *txRun) feeFloor(limit u256.Int) {
	if sp := r.env.Spec; sp != nil {
		ref := balanceRef(r.token, r.view.feePayer)
		sp.AffineFloor(ref.Address, ref.Key, limit)
	}
}
