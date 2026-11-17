package types

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

type SignatureKind uint8

const (
	SignatureSecp256k1 SignatureKind = 0
	SignatureP256      SignatureKind = 1
	SignatureWebAuthn  SignatureKind = 2
	SignatureAccount   SignatureKind = 3
)

type KeychainVersion uint8

const (
	KeychainNone KeychainVersion = 0
	KeychainV1   KeychainVersion = 0x03
	KeychainV2   KeychainVersion = 0x04
)

const (
	secpSignatureBytes   = 65
	p256SignatureBytes   = 129
	webAuthnTailBytes    = 128
	webAuthnSignatureMax = 2048
	typeP256             = 0x01
	typeWebAuthn         = 0x02
	typeConfigurable     = 0x05
	keychainHashBytes    = 1 + scalarBytes + primitives.AddressLength
)

const _ = uint(webAuthnSignatureMax - webAuthnTailBytes)

const _ = uint(p256SignatureBytes - 4*scalarBytes - 1)

const _ = uint(secpSignatureBytes - 2*scalarBytes - 1)

type PrimitiveSignature struct {
	Kind         SignatureKind
	R            [scalarBytes]byte
	S            [scalarBytes]byte
	YParity      bool
	PubKeyX      [scalarBytes]byte
	PubKeyY      [scalarBytes]byte
	PreHash      bool
	WebAuthnData []byte
}

type TempoSignature struct {
	Keychain    KeychainVersion
	UserAddress primitives.Address
	Inner       PrimitiveSignature
}

func secpParity(v byte) (bool, error) {
	switch {
	case v == 0, v == vBase:
		return false, nil
	case v == 1, v == vBase+1:
		return true, nil
	case v >= vEIP155Base:
		return (v-vEIP155Base)%vEIP155Steps == 1, nil
	default:
		return false, ErrSignatureV
	}
}

func ParsePrimitiveSignature(b []byte) (PrimitiveSignature, error) {
	var sig PrimitiveSignature
	if len(b) == secpSignatureBytes {
		parity, err := secpParity(b[2*scalarBytes])
		if err != nil {
			return sig, err
		}
		sig.Kind = SignatureSecp256k1
		copy(sig.R[:], b[:scalarBytes])
		copy(sig.S[:], b[scalarBytes:2*scalarBytes])
		sig.YParity = parity
		return sig, nil
	}
	if len(b) < 2 {
		return sig, ErrSignature
	}
	rest := b[1:]
	switch b[0] {
	case typeP256:
		return parseP256(rest)
	case typeWebAuthn:
		return parseWebAuthn(rest)
	case typeConfigurable:
		return sig, ErrUnsupportedSig
	default:
		return sig, ErrSignature
	}
}

func parseP256(rest []byte) (PrimitiveSignature, error) {
	var sig PrimitiveSignature
	if len(rest) != p256SignatureBytes {
		return sig, ErrSignature
	}
	sig.Kind = SignatureP256
	copy(sig.R[:], rest[0:scalarBytes])
	copy(sig.S[:], rest[scalarBytes:2*scalarBytes])
	copy(sig.PubKeyX[:], rest[2*scalarBytes:3*scalarBytes])
	copy(sig.PubKeyY[:], rest[3*scalarBytes:4*scalarBytes])
	sig.PreHash = rest[4*scalarBytes] != 0
	return sig, nil
}

func parseWebAuthn(rest []byte) (PrimitiveSignature, error) {
	var sig PrimitiveSignature
	if len(rest) < webAuthnTailBytes {
		return sig, ErrSignature
	}
	if len(rest) > webAuthnSignatureMax {
		return sig, ErrSignature
	}
	data := len(rest) - webAuthnTailBytes
	sig.Kind = SignatureWebAuthn
	sig.WebAuthnData = append([]byte(nil), rest[:data]...)
	tail := rest[data:]
	copy(sig.R[:], tail[0:scalarBytes])
	copy(sig.S[:], tail[scalarBytes:2*scalarBytes])
	copy(sig.PubKeyX[:], tail[2*scalarBytes:3*scalarBytes])
	copy(sig.PubKeyY[:], tail[3*scalarBytes:4*scalarBytes])
	return sig, nil
}

func ParseTempoSignature(b []byte) (TempoSignature, error) {
	var sig TempoSignature
	if len(b) != secpSignatureBytes && len(b) >= 2 {
		version := KeychainVersion(b[0])
		if version == KeychainV1 || version == KeychainV2 {
			return parseKeychain(version, b[1:])
		}
	}
	inner, err := ParsePrimitiveSignature(b)
	if err != nil {
		return sig, err
	}
	sig.Inner = inner
	return sig, nil
}

func parseKeychain(version KeychainVersion, rest []byte) (TempoSignature, error) {
	var sig TempoSignature
	if len(rest) < primitives.AddressLength {
		return sig, ErrSignature
	}
	inner, err := ParsePrimitiveSignature(rest[primitives.AddressLength:])
	if err != nil {
		return sig, err
	}
	sig.Keychain = version
	sig.UserAddress = primitives.BytesToAddress(rest[:primitives.AddressLength])
	sig.Inner = inner
	return sig, nil
}

func (sig *PrimitiveSignature) AppendBytes(dst []byte) []byte {
	switch sig.Kind {
	case SignatureSecp256k1:
		dst = append(dst, sig.R[:]...)
		dst = append(dst, sig.S[:]...)
		v := byte(vBase)
		if sig.YParity {
			v++
		}
		return append(dst, v)
	case SignatureP256:
		dst = append(dst, typeP256)
		dst = sig.appendKeyTail(dst)
		pre := byte(0)
		if sig.PreHash {
			pre = 1
		}
		return append(dst, pre)
	case SignatureWebAuthn:
		dst = append(dst, typeWebAuthn)
		dst = append(dst, sig.WebAuthnData...)
		return sig.appendKeyTail(dst)
	default:
		assert.Unreachable("SignatureKind: unhandled value")
	}
	return dst
}

func (sig *PrimitiveSignature) appendKeyTail(dst []byte) []byte {
	dst = append(dst, sig.R[:]...)
	dst = append(dst, sig.S[:]...)
	dst = append(dst, sig.PubKeyX[:]...)
	return append(dst, sig.PubKeyY[:]...)
}

func (sig *TempoSignature) Bytes() []byte {
	var dst []byte
	if sig.Keychain != KeychainNone {
		dst = append(dst, byte(sig.Keychain))
		dst = append(dst, sig.UserAddress[:]...)
	}
	return sig.Inner.AppendBytes(dst)
}

func (sig *PrimitiveSignature) Recover(hash primitives.Hash) (primitives.Address, error) {
	switch sig.Kind {
	case SignatureSecp256k1:
		return recoverSecp256k1(hash, Signature{R: sig.R, S: sig.S, YParity: sig.YParity})
	case SignatureP256:
		return sig.recoverP256(hash)
	case SignatureWebAuthn:
		return sig.recoverWebAuthn(hash)
	default:
		assert.Unreachable("SignatureKind: unhandled value")
	}
	return primitives.Address{}, ErrUnsupportedSig
}

func (sig *TempoSignature) effectiveHash(hash primitives.Hash) primitives.Hash {
	if sig.Keychain != KeychainV2 {
		return hash
	}
	var buf [keychainHashBytes]byte
	buf[0] = byte(KeychainV2)
	copy(buf[1:1+scalarBytes], hash[:])
	copy(buf[1+scalarBytes:], sig.UserAddress[:])
	return keccak.Sum256(buf[:])
}

func (sig *TempoSignature) KeyID(hash primitives.Hash) (primitives.Address, error) {
	return sig.Inner.Recover(sig.effectiveHash(hash))
}

func (sig *TempoSignature) Recover(hash primitives.Hash) (primitives.Address, error) {
	key, err := sig.KeyID(hash)
	if err != nil {
		return primitives.Address{}, err
	}
	if sig.Keychain == KeychainNone {
		return key, nil
	}
	return sig.UserAddress, nil
}
