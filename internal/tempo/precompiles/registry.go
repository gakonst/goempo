package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

func isValidMaster(a Address) bool {
	return a != (Address{}) && !IsVirtual(a) && !IsTIP20(a)
}

func (c *Context) registerVirtualMaster(sender Address, salt Hash) ([4]byte, error) {
	if !isValidMaster(sender) {
		return [4]byte{}, errSel("InvalidMasterAddress()")
	}
	var buf [52]byte
	copy(buf[:20], sender[:])
	copy(buf[20:], salt[:])
	h := c.keccak(buf[:])
	if [4]byte(h[:4]) != ([4]byte{}) {
		return [4]byte{}, errSel("ProofOfWorkFailed()")
	}
	id := [4]byte(h[4:8])
	existing, err := c.registryMaster(id)
	if err != nil {
		return id, err
	}
	if existing != (Address{}) {
		return id, revert(abiError(selector("MasterIdCollision(address)"), addressWord(existing)))
	}
	s := c.mapKey(masterKey(id), slotRegistryData)
	if err := c.storePackedGroup(AddressRegistryAddress, s, toWord(addressWord(sender))); err != nil {
		return id, err
	}
	return id, c.emit(AddressRegistryAddress, []Hash{evMasterRegistered, bytes4Word(id), addressWord(sender)}, nil)
}

func bytes4Word(b [4]byte) Hash {
	var h Hash
	copy(h[:4], b[:])
	return h
}

func (c *Context) resolveVirtual(a Address) (Address, error) {
	if !IsVirtual(a) {
		return Address{}, nil
	}
	return c.registryMaster(virtualMaster(a))
}

var implicitlyApproved = []Address{FeeManagerAddress, StablecoinDEXAddress, ChannelReserveAddress}

func isImplicitlyApproved(a Address, f hardfork.Fork) bool {
	if f < hardfork.T5 {
		return false
	}
	for _, x := range implicitlyApproved {
		if x == a {
			return true
		}
	}
	return false
}

var registryTable = register(AddressRegistryAddress, newTable(
	mutate("registerVirtualMaster(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.registerVirtualMaster(c.Caller, a[0].bytes32())
		return encodeWords(bytes4Word(id)), err
	}),
	view("getMaster(bytes4)", []abiType{scalarArg(kBytesN, 4)}, func(c *Context, a []abiValue) ([]byte, error) {
		m, err := c.registryMaster([4]byte(a[0].word[:4]))
		return retAddr(m), err
	}),
	view("resolveRecipient(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		r, err := c.resolveRecipient(a[0].addr())
		return retAddr(r.target), err
	}),
	view("resolveVirtualAddress(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		m, err := c.resolveVirtual(a[0].addr())
		return retAddr(m), err
	}),
	view("isVirtualAddress(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return retBool(flag(IsVirtual(a[0].addr()))), nil
	}),
	view("decodeVirtualAddress(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		addr := a[0].addr()
		if !IsVirtual(addr) {
			return encodeWords(Hash{}, Hash{}, Hash{}), nil
		}
		var tag Hash
		copy(tag[:6], addr[14:])
		return encodeWords(flag(true).hash(), bytes4Word(virtualMaster(addr)), tag), nil
	}),
	view("isImplicitlyApproved(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return retBool(flag(isImplicitlyApproved(a[0].addr(), c.Fork))), nil
	}).from(hardfork.T5),
))
