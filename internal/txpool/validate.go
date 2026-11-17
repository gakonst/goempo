package txpool

import (
	"fmt"

	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	maxCalls              = 32
	maxCallInput          = 128 * 1024
	maxAccessListAccounts = 256
	maxStorageKeysAccount = 256
	maxStorageKeysTotal   = 2048
	maxTokenLimits        = 256
	maxCallScopes         = 64
	maxSelectorRules      = 64
	maxRecipients         = 64
)

func (p *Pool) stateless(tx *types.Transaction, head *types.Header) error {
	if executor.IsSystemTx(tx) {
		return ErrTxType
	}
	floor := chain.RulesAt(p.cfg.Executor.ChainID, head.Inner.Timestamp).ExpectedBaseFee(chain.ParentGas{})
	if minFee := max(p.cfg.MinFeeCap, floor); maxFeeOf(tx) < minFee {
		return fmt.Errorf("%w: %d < %d", ErrUnderpriced, maxFeeOf(tx), minFee)
	}
	if c, ok := feeCaps(tx); ok && c.fee.Lt(c.tip) {
		return ErrTipAboveFeeCap
	}
	aa, ok := tx.Data.(*types.TempoTx)
	if !ok {
		return nil
	}
	if len(aa.AuthorizationList) > p.cfg.MaxAuthorizations {
		return fmt.Errorf("%w: %d > %d", ErrTooManyAuthorizations, len(aa.AuthorizationList), p.cfg.MaxAuthorizations)
	}
	if err := aaLimits(aa); err != nil {
		return err
	}
	return p.timeBounds(aa, head)
}

func aaLimits(aa *types.TempoTx) error {
	if len(aa.Calls) > maxCalls {
		return fmt.Errorf("%w: %d > %d", ErrTooManyCalls, len(aa.Calls), maxCalls)
	}
	for i := range aa.Calls {
		if len(aa.Calls[i].Input) > maxCallInput {
			return fmt.Errorf("%w: call %d has %d bytes", ErrCallInputTooLarge, i, len(aa.Calls[i].Input))
		}
	}
	if len(aa.AccessList) > maxAccessListAccounts {
		return fmt.Errorf("%w: %d > %d", ErrAccessListAccounts, len(aa.AccessList), maxAccessListAccounts)
	}
	total := 0
	for i := range aa.AccessList {
		n := len(aa.AccessList[i].StorageKeys)
		if n > maxStorageKeysAccount {
			return fmt.Errorf("%w: %d > %d", ErrAccessListKeys, n, maxStorageKeysAccount)
		}
		total += n
	}
	if total > maxStorageKeysTotal {
		return fmt.Errorf("%w: %d > %d", ErrAccessListKeysTotal, total, maxStorageKeysTotal)
	}
	if aa.KeyAuthorization == nil {
		return nil
	}
	return keyAuthLimits(&aa.KeyAuthorization.Authorization)
}

func keyAuthLimits(k *types.KeyAuthorization) error {
	if len(k.Limits) > maxTokenLimits {
		return fmt.Errorf("%w: %d > %d", ErrTokenLimits, len(k.Limits), maxTokenLimits)
	}
	if len(k.AllowedCalls) > maxCallScopes {
		return ErrCallScopes
	}
	for i := range k.AllowedCalls {
		rules := k.AllowedCalls[i].SelectorRules
		if len(rules) > maxSelectorRules {
			return ErrSelectorRules
		}
		for j := range rules {
			if len(rules[j].Recipients) > maxRecipients {
				return ErrRecipients
			}
		}
	}
	return nil
}

func (p *Pool) timeBounds(aa *types.TempoTx, head *types.Header) error {
	minBefore := head.Inner.Timestamp + validBeforeMinSecs
	if aa.ValidBefore != 0 && aa.ValidBefore <= minBefore {
		return fmt.Errorf("%w: %d <= %d", ErrValidBefore, aa.ValidBefore, minBefore)
	}
	if aa.ValidAfter != 0 {
		maxAfter := p.cfg.Now() + p.cfg.ValidAfterMaxSecs
		if aa.ValidAfter > maxAfter {
			return fmt.Errorf("%w: %d > %d", ErrValidAfter, aa.ValidAfter, maxAfter)
		}
	}
	if isSubblockKey(aa.NonceKey) {
		return ErrSubblockNonceKey
	}
	return nil
}

func isSubblockKey(key u256.Int) bool {
	b := key.Bytes32()
	return b[0] == subblockNoncePrefix
}

type caps struct {
	tip u256.Int
	fee u256.Int
}

func feeCaps(tx *types.Transaction) (caps, bool) {
	switch d := tx.Data.(type) {
	case *types.DynamicFeeTx:
		return caps{tip: d.MaxPriorityFeePerGas, fee: d.MaxFeePerGas}, true
	case *types.SetCodeTx:
		return caps{tip: d.MaxPriorityFeePerGas, fee: d.MaxFeePerGas}, true
	case *types.TempoTx:
		return caps{tip: d.MaxPriorityFeePerGas, fee: d.MaxFeePerGas}, true
	}
	return caps{}, false
}
