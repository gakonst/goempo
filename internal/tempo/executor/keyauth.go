package executor

import (
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	keyAuthBaseGas        = 27_000
	keyAuthPerLimitGas    = 22_000
	keyAuthBuffer         = 2_000
	keyAuthEventBuffer    = 1_500
	coldSloadGas          = 2_100
	secp256k1VerifyGas    = 3_000
	sstoreSetNoLoadT0     = 20_000
	sstoreSetNoLoadT1     = 250_000
	scopeBaseGas          = 5_000
	scopeTargetGas        = 7_000
	scopeSelectorGas      = 7_000
	scopeRecipientGas     = 5_000
	storageCreditValueT7  = 245_000
	sstoreSetNoLoadT7     = 5_000
	keyAuthLimitSlotsT3   = 2
	keyAuthLimitSlotsPreT = 1
)

func primitiveSignatureGas(sig *types.PrimitiveSignature) uint64 {
	gas := uint64(secp256k1VerifyGas)
	switch sig.Kind {
	case types.SignatureSecp256k1, types.SignatureAccount:
		gas += 0
	case types.SignatureP256:
		gas += p256VerifyGas
	case types.SignatureWebAuthn:
		gas += p256VerifyGas + calldataTokens(sig.WebAuthnData)*txTokenGas
	default:
		assert.Unreachable("SignatureKind: unhandled value")
	}
	return gas
}

func sstoreSetNoLoad(rules chain.Rules) uint64 {
	if rules.AtLeast(hardfork.T7) {
		return sstoreSetNoLoadT7 + storageCreditValueT7
	}
	if rules.AtLeast(hardfork.T1) {
		return sstoreSetNoLoadT1
	}
	return sstoreSetNoLoadT0
}

func keyAuthorizationGas(k *types.SignedKeyAuthorization, rules chain.Rules) uint64 {
	a := &k.Authorization
	sig := primitiveSignatureGas(&k.Signature)
	limits := uint64(len(a.Limits))
	if !rules.AtLeast(hardfork.T1B) {
		return keyAuthBaseGas + sig + limits*keyAuthPerLimitGas
	}
	perLimit := uint64(keyAuthLimitSlotsPreT)
	if rules.AtLeast(hardfork.T3) {
		perLimit = keyAuthLimitSlotsT3
	}
	sstores := 1 + limits*perLimit
	if rules.AtLeast(hardfork.T3) {
		sstores += scopeStorageSlots(a, rules)
	}
	gas := sig + coldSloadGas + sstoreSetNoLoad(rules)*sstores + keyAuthBuffer
	if a.HasWitness {
		gas += coldSloadGas + keyAuthEventBuffer
	}
	if rules.AtLeast(hardfork.T6) && a.IsAdmin {
		gas += keyAuthEventBuffer
	}
	if rules.AtLeast(hardfork.T4) {
		gas += scopeExtraGas(a)
	}
	return gas
}

type scopeCounts struct {
	targets      uint64
	selectors    uint64
	selectorSets uint64
	constrained  uint64
	recipients   uint64
}

func countScopes(a *types.KeyAuthorization) scopeCounts {
	var c scopeCounts
	c.targets = uint64(len(a.AllowedCalls))
	for i := 0; i < len(a.AllowedCalls); i++ {
		rules := a.AllowedCalls[i].SelectorRules
		if len(rules) > 0 {
			c.selectorSets++
		}
		c.selectors += uint64(len(rules))
		for j := 0; j < len(rules); j++ {
			if len(rules[j].Recipients) > 0 {
				c.constrained++
				c.recipients += uint64(len(rules[j].Recipients))
			}
		}
	}
	return c
}

func scopeStorageSlots(a *types.KeyAuthorization, rules chain.Rules) uint64 {
	if !a.HasAllowedCalls {
		return 0
	}
	if len(a.AllowedCalls) == 0 {
		return 1
	}
	c := countScopes(a)
	if rules.AtLeast(hardfork.T4) {
		return 1 + c.targets*2 + 1 + c.selectors*2 + c.selectorSets + c.constrained + c.recipients*2
	}
	return 1 + c.targets*3 + c.selectors*3 + c.constrained + c.recipients*2
}

func scopeExtraGas(a *types.KeyAuthorization) uint64 {
	if !a.HasAllowedCalls {
		return scopeBaseGas
	}
	c := countScopes(a)
	return scopeBaseGas + scopeTargetGas*c.targets + scopeSelectorGas*c.selectors + scopeRecipientGas*c.recipients
}
