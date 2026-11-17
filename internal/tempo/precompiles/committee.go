package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotCommitteeEpoch = slotOf(0)
	slotCommitteeIDs   = slotOf(1)
)

const maxCommitteeMembers = 1 << 16

func (c *Context) committeeLen() (uint64, error) {
	w, err := c.sload(CurrentCommitteeAddress, slotCommitteeIDs)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() || w.Uint64() > maxCommitteeMembers {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

func (c *Context) setCommitteeMembers(epoch uint64, keys []Hash) error {
	if len(keys) > maxCommitteeMembers {
		return underOverflow()
	}
	c.SkipStorageCredits = true
	if err := c.sstore(CurrentCommitteeAddress, slotCommitteeEpoch, u256.FromUint64(epoch)); err != nil {
		return err
	}
	data := slot(c.keccak(slotCommitteeIDs[:]))
	if c.at(hardfork.T5) {
		prev, err := c.committeeLen()
		if err != nil {
			return err
		}
		for i := uint64(len(keys)); i < min(prev, maxCommitteeMembers); i++ {
			if err := c.sstore(CurrentCommitteeAddress, data.plus(i), Word{}); err != nil {
				return err
			}
		}
	}
	if err := c.sstore(CurrentCommitteeAddress, slotCommitteeIDs, u256.FromUint64(uint64(len(keys)))); err != nil {
		return err
	}
	for i, k := range keys {
		if err := c.sstore(CurrentCommitteeAddress, data.plus(uint64(i)), toWord(k)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) committeeMembers() ([]byte, error) {
	epoch, err := c.loadField(CurrentCommitteeAddress, slotCommitteeEpoch, fU64At0)
	if err != nil {
		return nil, err
	}
	n, err := c.committeeLen()
	if err != nil {
		return nil, err
	}
	n = min(n, maxCommitteeMembers)
	data := slot(c.keccak(slotCommitteeIDs[:]))
	out := encodeWords(toHash(epoch), wordOfU64(64), wordOfU64(n))
	for i := uint64(0); i < n; i++ {
		w, err := c.sload(CurrentCommitteeAddress, data.plus(i))
		if err != nil {
			return nil, err
		}
		h := toHash(w)
		out = append(out, h[:]...)
	}
	return out, nil
}

func hashItems(v abiValue) []Hash {
	out := make([]Hash, len(v.items))
	for i, row := range v.items {
		out[i] = row[0]
	}
	return out
}

var committeeTable = register(CurrentCommitteeAddress, newTable(
	view("getCommitteeMembers()", nil, func(c *Context, a []abiValue) ([]byte, error) { return c.committeeMembers() }),
	mutate("setCommitteeMembers(uint64,bytes32[])", []abiType{tU64, tScalarArray(tB32)}, func(c *Context, a []abiValue) ([]byte, error) {
		if c.Caller != (Address{}) {
			return nil, errSel("Unauthorized()")
		}
		return nil, c.setCommitteeMembers(a[0].u64(), hashItems(a[1]))
	}),
))
