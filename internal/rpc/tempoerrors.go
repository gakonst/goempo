package rpc

import (
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type abiKind int

const (
	abiAddress abiKind = iota
	abiBytes4
	abiInt16
	abiString
	abiUint128
	abiUint256
	abiUint32
	abiUint8
)

type abiParam struct {
	kind abiKind
	name string
}

type tempoError struct {
	group  string
	name   string
	params []abiParam
}

var tempoErrors = []tempoError{
	{"Stablecoin DEX error", "Unauthorized", []abiParam{}},
	{"Stablecoin DEX error", "PairDoesNotExist", []abiParam{}},
	{"Stablecoin DEX error", "PairAlreadyExists", []abiParam{}},
	{"Stablecoin DEX error", "OrderDoesNotExist", []abiParam{}},
	{"Stablecoin DEX error", "IdenticalTokens", []abiParam{}},
	{"Stablecoin DEX error", "InvalidToken", []abiParam{}},
	{"Stablecoin DEX error", "TickOutOfBounds", []abiParam{{abiInt16, "tick"}}},
	{"Stablecoin DEX error", "InvalidTick", []abiParam{}},
	{"Stablecoin DEX error", "InvalidFlipTick", []abiParam{}},
	{"Stablecoin DEX error", "InsufficientBalance", []abiParam{}},
	{"Stablecoin DEX error", "InsufficientLiquidity", []abiParam{}},
	{"Stablecoin DEX error", "InsufficientOutput", []abiParam{}},
	{"Stablecoin DEX error", "MaxInputExceeded", []abiParam{}},
	{"Stablecoin DEX error", "BelowMinimumOrderSize", []abiParam{{abiUint128, "amount"}}},
	{"Stablecoin DEX error", "InvalidBaseToken", []abiParam{}},
	{"Stablecoin DEX error", "OrderNotStale", []abiParam{}},
	{"Stablecoin DEX error", "IndexAlreadySet", []abiParam{}},
	{"TIP20 token error", "InsufficientBalance", []abiParam{{abiUint256, "available"}, {abiUint256, "required"}, {abiAddress, "token"}}},
	{"TIP20 token error", "InsufficientAllowance", []abiParam{}},
	{"TIP20 token error", "SupplyCapExceeded", []abiParam{}},
	{"TIP20 token error", "InvalidSupplyCap", []abiParam{}},
	{"TIP20 token error", "InvalidPayload", []abiParam{}},
	{"TIP20 token error", "PolicyForbids", []abiParam{}},
	{"TIP20 token error", "InvalidRecipient", []abiParam{}},
	{"TIP20 token error", "ContractPaused", []abiParam{}},
	{"TIP20 token error", "InvalidCurrency", []abiParam{}},
	{"TIP20 token error", "InvalidQuoteToken", []abiParam{}},
	{"TIP20 token error", "InvalidAmount", []abiParam{}},
	{"TIP20 token error", "NoOptedInSupply", []abiParam{}},
	{"TIP20 token error", "Unauthorized", []abiParam{}},
	{"TIP20 token error", "ProtectedAddress", []abiParam{}},
	{"TIP20 token error", "InvalidToken", []abiParam{}},
	{"TIP20 token error", "Uninitialized", []abiParam{}},
	{"TIP20 token error", "InvalidTransferPolicyId", []abiParam{}},
	{"TIP20 token error", "PermitExpired", []abiParam{}},
	{"TIP20 token error", "InvalidSignature", []abiParam{}},
	{"TIP20 token error", "LogoURITooLong", []abiParam{}},
	{"TIP20 token error", "InvalidLogoURI", []abiParam{}},
	{"TIP20 token error", "RootKeyRetired", []abiParam{{abiAddress, "account"}}},
	{"TIP20 token error", "UnsupportedAddressVersion", []abiParam{{abiUint8, "version"}}},
	{"TIP20 token error", "ZoneUnregistered", []abiParam{{abiUint32, "zoneId"}}},
	{"TIP20 factory error", "AddressReserved", []abiParam{}},
	{"TIP20 factory error", "AddressNotReserved", []abiParam{}},
	{"TIP20 factory error", "InvalidQuoteToken", []abiParam{}},
	{"TIP20 factory error", "TokenAlreadyExists", []abiParam{{abiAddress, "token"}}},
	{"TIP20 channel reserve error", "ChannelAlreadyExists", []abiParam{}},
	{"TIP20 channel reserve error", "ChannelNotFound", []abiParam{}},
	{"TIP20 channel reserve error", "NotPayer", []abiParam{}},
	{"TIP20 channel reserve error", "NotPayeeOrOperator", []abiParam{}},
	{"TIP20 channel reserve error", "InvalidPayee", []abiParam{}},
	{"TIP20 channel reserve error", "ZeroDeposit", []abiParam{}},
	{"TIP20 channel reserve error", "ExpiringNonceHashNotSet", []abiParam{}},
	{"TIP20 channel reserve error", "InvalidSignature", []abiParam{}},
	{"TIP20 channel reserve error", "AmountExceedsDeposit", []abiParam{}},
	{"TIP20 channel reserve error", "AmountNotIncreasing", []abiParam{}},
	{"TIP20 channel reserve error", "CaptureAmountInvalid", []abiParam{}},
	{"TIP20 channel reserve error", "CloseNotReady", []abiParam{}},
	{"TIP20 channel reserve error", "DepositOverflow", []abiParam{}},
	{"TIP20 channel reserve error", "RootKeyRetired", []abiParam{{abiAddress, "account"}}},
	{"Roles auth error", "Unauthorized", []abiParam{}},
	{"TIP20 registry error", "MasterIdCollision", []abiParam{{abiAddress, "master"}}},
	{"TIP20 registry error", "InvalidMasterAddress", []abiParam{}},
	{"TIP20 registry error", "ProofOfWorkFailed", []abiParam{}},
	{"TIP20 registry error", "VirtualAddressUnregistered", []abiParam{}},
	{"TIP403 registry error", "Unauthorized", []abiParam{}},
	{"TIP403 registry error", "PolicyNotFound", []abiParam{}},
	{"TIP403 registry error", "PolicyNotSimple", []abiParam{}},
	{"TIP403 registry error", "InvalidPolicyType", []abiParam{}},
	{"TIP403 registry error", "IncompatiblePolicyType", []abiParam{}},
	{"TIP403 registry error", "VirtualAddressNotAllowed", []abiParam{}},
	{"TIP403 registry error", "InvalidReceivePolicyType", []abiParam{}},
	{"TIP403 registry error", "InvalidRecoveryAuthority", []abiParam{}},
	{"TIP fee manager error", "InvalidToken", []abiParam{}},
	{"TIP fee manager error", "InsufficientFeeTokenBalance", []abiParam{}},
	{"TIP fee manager error", "CannotChangeWithinBlock", []abiParam{}},
	{"TIP fee AMM error", "IdenticalAddresses", []abiParam{}},
	{"TIP fee AMM error", "InvalidToken", []abiParam{}},
	{"TIP fee AMM error", "InsufficientLiquidity", []abiParam{}},
	{"TIP fee AMM error", "InsufficientReserves", []abiParam{}},
	{"TIP fee AMM error", "InvalidAmount", []abiParam{}},
	{"TIP fee AMM error", "DivisionByZero", []abiParam{}},
	{"TIP fee AMM error", "InvalidSwapCalculation", []abiParam{}},
	{"Tempo Transaction nonce error", "ProtocolNonceNotSupported", []abiParam{}},
	{"Tempo Transaction nonce error", "InvalidNonceKey", []abiParam{}},
	{"Tempo Transaction nonce error", "NonceOverflow", []abiParam{}},
	{"Tempo Transaction nonce error", "ExpiringNonceReplay", []abiParam{}},
	{"Tempo Transaction nonce error", "ExpiringNonceSetFull", []abiParam{}},
	{"Tempo Transaction nonce error", "InvalidExpiringNonceExpiry", []abiParam{}},
	{"Validator config error", "Unauthorized", []abiParam{}},
	{"Validator config error", "ValidatorAlreadyExists", []abiParam{}},
	{"Validator config error", "ValidatorNotFound", []abiParam{}},
	{"Validator config error", "InvalidPublicKey", []abiParam{}},
	{"Validator config error", "NotHostPort", []abiParam{{abiString, "field"}, {abiString, "input"}, {abiString, "backtrace"}}},
	{"Validator config error", "NotIpPort", []abiParam{{abiString, "field"}, {abiString, "input"}, {abiString, "backtrace"}}},
	{"Validator config v2 error", "AlreadyInitialized", []abiParam{}},
	{"Validator config v2 error", "IngressAlreadyExists", []abiParam{{abiString, "ingress"}}},
	{"Validator config v2 error", "EmptyV1ValidatorSet", []abiParam{}},
	{"Validator config v2 error", "InvalidMigrationIndex", []abiParam{}},
	{"Validator config v2 error", "InvalidIp", []abiParam{{abiString, "input"}, {abiString, "reason"}}},
	{"Validator config v2 error", "InvalidOwner", []abiParam{}},
	{"Validator config v2 error", "InvalidPublicKey", []abiParam{}},
	{"Validator config v2 error", "InvalidSignature", []abiParam{}},
	{"Validator config v2 error", "InvalidSignatureFormat", []abiParam{}},
	{"Validator config v2 error", "InvalidValidatorAddress", []abiParam{}},
	{"Validator config v2 error", "MigrationNotComplete", []abiParam{}},
	{"Validator config v2 error", "NotInitialized", []abiParam{}},
	{"Validator config v2 error", "NotIp", []abiParam{{abiString, "input"}, {abiString, "backtrace"}}},
	{"Validator config v2 error", "NotIpPort", []abiParam{{abiString, "input"}, {abiString, "backtrace"}}},
	{"Validator config v2 error", "PublicKeyAlreadyExists", []abiParam{}},
	{"Validator config v2 error", "Unauthorized", []abiParam{}},
	{"Validator config v2 error", "AddressAlreadyHasValidator", []abiParam{}},
	{"Validator config v2 error", "ValidatorAlreadyDeactivated", []abiParam{}},
	{"Validator config v2 error", "ValidatorNotFound", []abiParam{}},
	{"Account keychain error", "UnauthorizedCaller", []abiParam{}},
	{"Account keychain error", "KeyAlreadyExists", []abiParam{}},
	{"Account keychain error", "KeyNotFound", []abiParam{}},
	{"Account keychain error", "KeyExpired", []abiParam{}},
	{"Account keychain error", "SpendingLimitExceeded", []abiParam{}},
	{"Account keychain error", "InvalidSpendingLimit", []abiParam{}},
	{"Account keychain error", "InvalidSignatureType", []abiParam{}},
	{"Account keychain error", "ZeroPublicKey", []abiParam{}},
	{"Account keychain error", "ExpiryInPast", []abiParam{}},
	{"Account keychain error", "KeyAlreadyRevoked", []abiParam{}},
	{"Account keychain error", "SignatureTypeMismatch", []abiParam{{abiUint8, "expected"}, {abiUint8, "actual"}}},
	{"Account keychain error", "CallNotAllowed", []abiParam{}},
	{"Account keychain error", "InvalidCallScope", []abiParam{}},
	{"Account keychain error", "InvalidKeyId", []abiParam{}},
	{"Account keychain error", "InvalidKeyAuthorizationWitness", []abiParam{}},
	{"Account keychain error", "KeyAuthorizationWitnessAlreadyBurned", []abiParam{}},
	{"Account keychain error", "LegacyAuthorizeKeySelectorChanged", []abiParam{{abiBytes4, "newSelector"}}},
	{"Signature verifier error", "InvalidFormat", []abiParam{}},
	{"Signature verifier error", "InvalidSignature", []abiParam{}},
	{"TIP1028 blocked transfers error", "InvalidReceipt", []abiParam{}},
	{"TIP1028 blocked transfers error", "InvalidClaimAddress", []abiParam{}},
	{"TIP1028 blocked transfers error", "UnauthorizedClaimer", []abiParam{}},
	{"TIP1028 blocked transfers error", "AddressReserved", []abiParam{}},
	{"TIP1060 storage credits error", "InvalidMode", []abiParam{}},
	{"TIP1060 storage credits error", "OnlyDirectCall", []abiParam{}},
	{"Current committee error", "Unauthorized", []abiParam{}},
	{"ZoneFactory error", "InvalidToken", []abiParam{}},
	{"ZoneFactory error", "TokenTransferPolicyNotSet", []abiParam{}},
	{"ZoneFactory error", "InvalidClosedLoopConfig", []abiParam{}},
	{"ZoneFactory error", "NotOwner", []abiParam{}},
	{"ZoneFactory error", "InvalidAdmin", []abiParam{}},
	{"ZoneFactory error", "InvalidSequencerSet", []abiParam{}},
	{"ZoneFactory error", "AlreadyInitialized", []abiParam{}},
	{"ZoneFactory error", "TokenMetadataTooLong", []abiParam{}},
	{"configurable account error", "InvalidAccount", []abiParam{}},
	{"configurable account error", "InvalidConfig", []abiParam{}},
	{"configurable account error", "InvalidThreshold", []abiParam{}},
	{"configurable account error", "InvalidConfigurableAccountOwner", []abiParam{}},
	{"configurable account error", "InvalidWeight", []abiParam{}},
	{"configurable account error", "TooManyOwners", []abiParam{}},
	{"configurable account error", "DuplicateOwner", []abiParam{}},
	{"configurable account error", "InvalidOwnerOrder", []abiParam{}},
	{"configurable account error", "UnauthorizedConfigurableAccountCaller", []abiParam{}},
	{"configurable account error", "PrimitiveRootRequired", []abiParam{}},
	{"configurable account error", "UpgradeMustBeDirectCall", []abiParam{}},
	{"configurable account error", "AccountAlreadyConfigurable", []abiParam{}},
	{"configurable account error", "AccountHasCode", []abiParam{}},
	{"configurable account error", "UnsupportedOnZone", []abiParam{}},
}

var tempoErrorBySelector = buildTempoErrors()

func buildTempoErrors() map[[4]byte]*tempoError {
	m := make(map[[4]byte]*tempoError, len(tempoErrors))
	for i := range tempoErrors {
		e := &tempoErrors[i]
		sum := keccak.Sum256([]byte(e.signature()))
		m[[4]byte(sum[:4])] = e
	}
	return m
}

func (k abiKind) String() string {
	switch k {
	case abiAddress:
		return "address"
	case abiBytes4:
		return "bytes4"
	case abiInt16:
		return "int16"
	case abiString:
		return "string"
	case abiUint128:
		return "uint128"
	case abiUint256:
		return "uint256"
	case abiUint32:
		return "uint32"
	case abiUint8:
		return "uint8"
	default:
		assert.Unreachable("abiKind: unhandled value")
	}
	return ""
}

func (e *tempoError) signature() string {
	var b strings.Builder
	b.WriteString(e.name)
	b.WriteByte('(')
	for i, p := range e.params {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.kind.String())
	}
	b.WriteByte(')')
	return b.String()
}

func decodeTempoError(ret []byte) (string, bool) {
	if len(ret) < revertSelectorLength {
		return "", false
	}
	e, ok := tempoErrorBySelector[[4]byte(ret[:revertSelectorLength])]
	if !ok {
		return "", false
	}
	body := ret[revertSelectorLength:]
	if len(body) < len(e.params)*abiWord {
		return "", false
	}
	var b strings.Builder
	b.WriteString(e.group)
	b.WriteString(": ")
	b.WriteString(e.name)
	b.WriteByte('(')
	b.WriteString(e.name)
	if len(e.params) > 0 {
		b.WriteString(" { ")
		for i, p := range e.params {
			if i > 0 {
				b.WriteString(", ")
			}
			v, ok := abiValue(body, i, p.kind)
			if !ok {
				return "", false
			}
			b.WriteString(p.name)
			b.WriteString(": ")
			b.WriteString(v)
		}
		b.WriteString(" }")
	}
	b.WriteByte(')')
	return b.String(), true
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

func allOnes(b []byte) bool {
	for _, c := range b {
		if c != 0xff {
			return false
		}
	}
	return true
}

func abiValue(body []byte, i int, kind abiKind) (string, bool) {
	w := body[i*abiWord : (i+1)*abiWord]
	switch kind {
	case abiAddress:
		if !allZero(w[:12]) {
			return "", false
		}
		return "0x" + hex.EncodeToString(w[12:]), true
	case abiBytes4:
		if !allZero(w[4:]) {
			return "", false
		}
		return "0x" + hex.EncodeToString(w[:4]), true
	case abiInt16:
		if allZero(w[:30]) && w[30] < 0x80 {
			return strconv.Itoa(int(w[30])<<8 | int(w[31])), true
		}
		if allOnes(w[:30]) && w[30] >= 0x80 {
			return strconv.Itoa(int(int16(uint16(w[30])<<8 | uint16(w[31])))), true
		}
		return "", false
	case abiString:
		return abiDebugString(body, [abiWord]byte(w))
	case abiUint128:
		return uintValue(w, 16)
	case abiUint256:
		return uintValue(w, 0)
	case abiUint32:
		return uintValue(w, 28)
	case abiUint8:
		return uintValue(w, 31)
	default:
		assert.Unreachable("abiKind: unhandled value")
	}
	return "", false
}

func uintValue(w []byte, start int) (string, bool) {
	if !allZero(w[:start]) {
		return "", false
	}
	return new(big.Int).SetBytes(w).String(), true
}

func abiDebugString(body []byte, head [abiWord]byte) (string, bool) {
	off := u256.FromBytes(head[:])
	if !off.IsUint64() || off.Uint64() > uint64(len(body)-abiWord) {
		return "", false
	}
	o := off.Uint64()
	size := u256.FromBytes(body[o : o+abiWord])
	if !size.IsUint64() || size.Uint64() > uint64(len(body))-o-abiWord {
		return "", false
	}
	s := body[o+abiWord : o+abiWord+size.Uint64()]
	if !utf8.Valid(s) {
		return "", false
	}
	return rustDebugString(string(s)), true
}

func rustDebugString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case 0:
			b.WriteString("\\0")
		default:
			if unicode.IsPrint(r) {
				b.WriteRune(r)
			} else {
				b.WriteString("\\u{" + strconv.FormatInt(int64(r), 16) + "}")
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func RevertDebugString(ret []byte) string {
	e := RevertError(ret)
	data := "None"
	if len(ret) > 0 {
		data = "Some(RawValue(\"0x" + hex.EncodeToString(ret) + "\"))"
	}
	return "ErrorObject { code: ServerError(" + strconv.Itoa(int(e.Code)) + "), message: " + rustDebugString(e.Message) + ", data: " + data + " }"
}
