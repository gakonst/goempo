package executor

import (
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	txBaseGas            = 21_000
	txTokenGas           = 4
	txFloorTokenGas      = 10
	accessListAddressGas = 2_400
	accessListSlotGas    = 1_900
	initCodeWordGas      = 2
	coldAccountGas       = 2_600
	p256VerifyGas        = 5_000
	keychainGas          = 3_000
	expiringNonceGas     = 13_000
	newNonceKeyGasT1     = 22_100
	newNonceKeyGasT2     = 22_300
	nonceKeyGasT1        = 5_000
	nonceKeyGasT2        = 5_200
	nonZeroTokens        = 4
)

type intrinsicGas struct {
	gas   uint64
	floor uint64
}

func calldataTokens(input []byte) uint64 {
	var tokens uint64
	for i := 0; i < len(input); i++ {
		if input[i] == 0 {
			tokens++
		} else {
			tokens += nonZeroTokens
		}
	}
	return tokens
}

func accessListGas(list types.AccessList) uint64 {
	var gas uint64
	for i := 0; i < len(list); i++ {
		gas += accessListAddressGas + uint64(len(list[i].StorageKeys))*accessListSlotGas
	}
	return gas
}

func createGas(g uint64, input []byte) uint64 {
	return g + (uint64(len(input))+31)/32*initCodeWordGas
}

func signatureGas(sig *types.TempoSignature) uint64 {
	var gas uint64
	switch sig.Inner.Kind {
	case types.SignatureP256:
		gas = p256VerifyGas
	case types.SignatureWebAuthn:
		gas = p256VerifyGas + calldataTokens(sig.Inner.WebAuthnData)*txTokenGas
	case types.SignatureSecp256k1, types.SignatureAccount:
		gas = 0
	default:
		assert.Unreachable("SignatureKind: unhandled value")
	}
	if sig.Keychain != types.KeychainNone {
		gas += keychainGas
	}
	return gas
}

func intrinsic(v *txView, rules chain.Rules) intrinsicGas {
	g := rules.GasTable()
	gas := uint64(txBaseGas)
	var tokens uint64
	for i := 0; i < len(v.calls); i++ {
		c := &v.calls[i]
		tokens += calldataTokens(c.input)
		if c.to == nil {
			gas += createGas(g.Create, c.input)
		}
	}
	gas += tokens*txTokenGas + accessListGas(v.accessList)
	if v.isAA() {
		gas += aaExtraGas(v, rules)
	} else {
		gas += uint64(len(v.auths)) * rules.AuthorizationGas()
		gas += ethNewAccountGas(v, rules)
	}
	return intrinsicGas{gas: gas, floor: txBaseGas + tokens*txFloorTokenGas}
}

func ethNewAccountGas(v *txView, rules chain.Rules) uint64 {
	if !rules.AtLeast(hardfork.T1) {
		return 0
	}
	var accounts uint64
	if v.nonce == 0 {
		accounts++
	}
	for i := 0; i < len(v.auths); i++ {
		if v.auths[i].nonce == 0 {
			accounts++
		}
	}
	return accounts * rules.NewAccountNonceGas()
}

func aaExtraGas(v *txView, rules chain.Rules) uint64 {
	gas := signatureGas(&v.aa.Signature)
	if len(v.calls) > 1 {
		gas += uint64(len(v.calls)-1) * coldAccountGas
	}
	for i := 0; i < len(v.aa.AuthorizationList); i++ {
		gas += rules.AuthorizationGas() + signatureGas(&v.aa.AuthorizationList[i].Signature)
		if v.aa.AuthorizationList[i].Nonce == 0 {
			gas += rules.NewAccountNonceGas()
		}
	}
	if v.aa.KeyAuthorization != nil {
		gas += keyAuthorizationGas(v.aa.KeyAuthorization, rules)
	}
	return gas + nonceGas(v, rules)
}

func isExpiring(v *txView, rules chain.Rules) bool {
	return rules.AtLeast(hardfork.T1) && v.nonceKey == maxWord
}

func nonceGas(v *txView, rules chain.Rules) uint64 {
	if isExpiring(v, rules) {
		return expiringNonceGas
	}
	if rules.AtLeast(hardfork.T1) && v.nonce == 0 {
		return rules.NewAccountNonceGas()
	}
	if v.nonceKey.IsZero() {
		return 0
	}
	if v.nonce == 0 {
		if rules.AtLeast(hardfork.T2) {
			return newNonceKeyGasT2
		}
		return newNonceKeyGasT1
	}
	if rules.AtLeast(hardfork.T2) {
		return nonceKeyGasT2
	}
	return nonceKeyGasT1
}

type Intrinsic struct {
	Gas   uint64
	Floor uint64
}

func IntrinsicGas(tx *types.Transaction, rules chain.Rules) (Intrinsic, error) {
	v, err := viewOf(tx)
	if err != nil {
		return Intrinsic{}, err
	}
	need := intrinsic(&v, rules)
	return Intrinsic{Gas: need.gas, Floor: need.floor}, nil
}
