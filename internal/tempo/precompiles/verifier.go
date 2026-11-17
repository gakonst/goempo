package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

const (
	gasVerifySecp256k1 uint64 = 3_000
	gasVerifyP256      uint64 = 8_000
	maxVerifierInput          = 4 + 32*4 + 2080
)

func verificationGas(k types.SignatureKind) uint64 {
	if k == types.SignatureSecp256k1 {
		return gasVerifySecp256k1
	}
	return gasVerifyP256
}

func (c *Context) recoverPrimitive(hash Hash, sig types.PrimitiveSignature) (Address, error) {
	if err := c.charge(verificationGas(sig.Kind)); err != nil {
		return Address{}, err
	}
	addr, err := sig.Recover(hash)
	if err != nil {
		return Address{}, errSel("InvalidSignature()")
	}
	return addr, nil
}

func (c *Context) recoverSigner(hash Hash, raw []byte) (Address, error) {
	sig, err := types.ParsePrimitiveSignature(raw)
	if err != nil || sig.Kind == types.SignatureAccount {
		return Address{}, errSel("InvalidFormat()")
	}
	return c.recoverPrimitive(hash, sig)
}

type recoveredKey struct {
	account Address
	keyID   Address
	kind    types.SignatureKind
}

func (c *Context) recoverKeychainKey(hash Hash, raw []byte) (recoveredKey, error) {
	sig, err := types.ParseTempoSignature(raw)
	if err != nil || sig.Keychain != types.KeychainV2 || sig.Inner.Kind == types.SignatureAccount {
		return recoveredKey{}, errSel("InvalidFormat()")
	}
	var buf [1 + 32 + 20]byte
	buf[0] = byte(types.KeychainV2)
	copy(buf[1:33], hash[:])
	copy(buf[33:], sig.UserAddress[:])
	h := c.keccak(buf[:])
	key, err := c.recoverPrimitive(h, sig.Inner)
	return recoveredKey{account: sig.UserAddress, keyID: key, kind: sig.Inner.Kind}, err
}

type keyLookup struct {
	key   ActiveKey
	found bool
}

func (c *Context) activeKey(rk recoveredKey) (keyLookup, error) {
	key, err := ValidateKeychainAuthorization(c, KeyUse{Account: rk.account, KeyID: rk.keyID, SigType: uint8(rk.kind), Check: c.at(hardfork.T14)})
	if err != nil {
		if isRevert(err) {
			return keyLookup{}, nil
		}
		return keyLookup{}, err
	}
	return keyLookup{key: key, found: true}, nil
}

type keychainCheck struct {
	account Address
	hash    Hash
	sig     []byte
	admin   flag
}

func (c *Context) verifyKeychain(k keychainCheck) (bool, error) {
	rk, err := c.recoverKeychainKey(k.hash, k.sig)
	if err != nil {
		return false, err
	}
	if rk.account != k.account {
		return false, nil
	}
	retired, err := c.isRetiredRoot(rk.keyID)
	if err != nil || retired {
		return false, err
	}
	if bool(k.admin) && rk.keyID == k.account {
		return true, nil
	}
	l, err := c.activeKey(rk)
	if err != nil || !l.found {
		return false, err
	}
	return !bool(k.admin) || l.key.Admin, nil
}

func keychainArgs(a []abiValue, admin flag) keychainCheck {
	return keychainCheck{account: a[0].addr(), hash: a[1].bytes32(), sig: a[2].data, admin: admin}
}

var verifierTable = register(SignatureVerifierAddress, newTable(
	view("recover(bytes32,bytes)", []abiType{tB32, tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		s, err := c.recoverSigner(a[0].bytes32(), a[1].data)
		return retAddr(s), err
	}),
	view("verify(address,bytes32,bytes)", []abiType{tAddress, tB32, tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		s, err := c.recoverSigner(a[1].bytes32(), a[2].data)
		return retBool(flag(s == a[0].addr())), err
	}),
	view("verifyKeychain(address,bytes32,bytes)", []abiType{tAddress, tB32, tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.verifyKeychain(keychainArgs(a, false))
		return retBool(flag(ok)), err
	}).from(hardfork.T6),
	view("verifyKeychainAdmin(address,bytes32,bytes)", []abiType{tAddress, tB32, tBytes}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.verifyKeychain(keychainArgs(a, true))
		return retBool(flag(ok)), err
	}).from(hardfork.T6),
))

func (c *Context) isRetiredRoot(owner Address) (bool, error) {
	if !c.at(hardfork.T14) {
		return false, nil
	}
	return c.isRetiredRootT14(owner)
}
