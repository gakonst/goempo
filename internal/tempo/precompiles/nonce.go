package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotNonces       = slotOf(0)
	slotExpiringSeen = slotOf(1)
	slotExpiringRing = slotOf(2)
	slotExpiringPtr  = slotOf(3)
	fU32At0          = field{offset: 0, size: 4}
)

func (c *Context) nonceSlot(account Address, key Word) slot {
	return c.mapKey(toHash(key), c.mapAddr(account, slotNonces))
}

func GetNonce(c *Context, account Address, key Word) (uint64, error) {
	if key.IsZero() {
		return 0, errSel("ProtocolNonceNotSupported()")
	}
	w, err := c.loadField(NonceManagerAddress, c.nonceSlot(account, key), fU64At0)
	return w.Uint64(), err
}

func IncrementNonce(c *Context, account Address, key Word) (uint64, error) {
	if key.IsZero() {
		return 0, errSel("InvalidNonceKey()")
	}
	s := c.nonceSlot(account, key)
	w, err := c.loadField(NonceManagerAddress, s, fU64At0)
	if err != nil {
		return 0, err
	}
	cur := w.Uint64()
	if cur == ^uint64(0) {
		return 0, errSel("NonceOverflow()")
	}
	next := cur + 1
	if err := c.sstore(NonceManagerAddress, s, u256.FromUint64(next)); err != nil {
		return 0, err
	}
	err = c.emit(NonceManagerAddress, []Hash{evNonceIncremented, addressWord(account), toHash(key)}, encodeWords(wordOfU64(next)))
	return next, err
}

func expiringCapacity(f hardfork.Fork) uint64 {
	if f >= hardfork.T11 {
		return 3_000_000
	}
	return 300_000
}

func expiringMaxSecs(f hardfork.Fork) uint64 {
	if f >= hardfork.T11 {
		return 300
	}
	return 30
}

func (c *Context) expiringSeen(h Hash) (uint64, error) {
	w, err := c.loadField(NonceManagerAddress, c.mapKey(h, slotExpiringSeen), fU64At0)
	return w.Uint64(), err
}

func IsExpiringNonceSeen(c *Context, h Hash, now uint64) (bool, error) {
	exp, err := c.expiringSeen(h)
	return exp != 0 && exp > now, err
}

func CheckAndMarkExpiringNonce(c *Context, h Hash, validBefore uint64) error {
	now := c.timestamp()
	if validBefore <= now || validBefore > uint64(sat(now).add(expiringMaxSecs(c.Fork))) {
		return errSel("InvalidExpiringNonceExpiry()")
	}
	seen, err := c.expiringSeen(h)
	if err != nil {
		return err
	}
	if seen != 0 && seen > now {
		return errSel("ExpiringNonceReplay()")
	}
	ptrW, err := c.loadField(NonceManagerAddress, slotExpiringPtr, fU32At0)
	if err != nil {
		return err
	}
	ptr := ptrW.Uint64()
	ringSlot := c.mapU64(ptr, slotExpiringRing)
	oldW, err := c.sload(NonceManagerAddress, ringSlot)
	if err != nil {
		return err
	}
	if err := c.evictExpiring(toHash(oldW), now); err != nil {
		return err
	}
	if err := c.sstore(NonceManagerAddress, ringSlot, toWord(h)); err != nil {
		return err
	}
	if err := c.sstore(NonceManagerAddress, c.mapKey(h, slotExpiringSeen), u256.FromUint64(validBefore)); err != nil {
		return err
	}
	next := ptr + 1
	if next >= expiringCapacity(c.Fork) {
		next = 0
	}
	return c.sstore(NonceManagerAddress, slotExpiringPtr, u256.FromUint64(next))
}

func (c *Context) evictExpiring(old Hash, now uint64) error {
	if old == (Hash{}) {
		return nil
	}
	oldExp, err := c.expiringSeen(old)
	if err != nil {
		return err
	}
	if oldExp != 0 && oldExp > now {
		return errSel("ExpiringNonceSetFull()")
	}
	return c.sstore(NonceManagerAddress, c.mapKey(old, slotExpiringSeen), Word{})
}

var nonceTable = register(NonceManagerAddress, newTable(
	view("getNonce(address,uint256)", []abiType{tAddress, tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		n, err := GetNonce(c, a[0].addr(), a[1].u256())
		return encodeWords(wordOfU64(n)), err
	}),
))
