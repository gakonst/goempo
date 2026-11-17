package precompiles

import (
	"github.com/gakonst/goempo/internal/u256"
)

const (
	creditModeRefund uint64 = iota
	creditModePreserve
	creditModeDirect
)

type creditState struct {
	mode    uint64
	budget  uint64
	pending uint64
}

func decodeCreditState(w Word) (creditState, error) {
	if w[0] > creditModeDirect {
		return creditState{}, errFatal
	}
	return creditState{mode: w[0], budget: w[1], pending: w[3]}, nil
}

func (s creditState) word() Word { return Word{s.mode, s.budget, 0, s.pending} }

func creditSlot(owner Address) slot { return slot(addressWord(owner)) }

func (c *Context) storageCredits(owner Address, key slot, v sstoreValues) error {
	if !v.crossesZero() || owner == StorageCreditsAddress {
		return nil
	}
	if err := c.charge(gasWarmRead); err != nil {
		return err
	}
	s := creditSlot(owner)
	if !c.State.SlotWarm(StorageCreditsAddress, Hash(s)) && c.GasLeft() < gasColdSloadExtra {
		return c.outOfGas()
	}
	cold := !c.warmSlot(StorageCreditsAddress, s)
	raw := c.State.Storage(StorageCreditsAddress, Hash(s))
	if cold {
		if err := c.charge(gasColdSloadExtra); err != nil {
			return err
		}
	}
	if !raw.IsUint64() {
		return errFatal
	}
	credit := raw.Uint64()
	changed := false
	if v.next.IsZero() {
		if c.isNonCreditable(owner, key) || !c.creditMinting {
			return nil
		}
		credit = uint64(sat(credit).add(1))
		changed = true
	} else {
		state, err := decodeCreditState(c.State.TransientStorage(StorageCreditsAddress, Hash(s)))
		if err != nil {
			return err
		}
		c.noteCreditOwner(owner)
		spend, err := c.creationCredit(&state, credit)
		if err != nil {
			return err
		}
		if spend {
			credit--
			changed = true
		}
		c.State.SetTransientStorage(StorageCreditsAddress, Hash(s), state.word())
	}
	if !changed {
		return nil
	}
	if c.journalWrite(StorageCreditsAddress, s, u256.FromUint64(credit)).cleanUpdate() {
		return c.charge(gasSstoreReset)
	}
	return nil
}

func (c *Context) creationCredit(state *creditState, credit uint64) (bool, error) {
	direct := state.mode == creditModeDirect
	if direct && credit > 0 && state.budget > 0 {
		if state.budget != ^uint64(0) {
			state.budget--
		}
		return true, nil
	}
	if err := c.charge(gasStorageCredit); err != nil {
		return false, err
	}
	if state.mode == creditModeRefund {
		state.pending = uint64(sat(state.pending).add(1))
	}
	return false, nil
}

type FeeSlots struct {
	FeePayer Address
	FeeToken Address
	KeyID    Address
}

func MarkFeeSlotsNonCreditable(c *Context, f FeeSlots) {
	refs := []SlotRef{{Address: f.FeeToken, Key: Hash(TIP20(c, f.FeeToken).balanceSlot(f.FeePayer))}}
	if f.KeyID != (Address{}) {
		limit := c.limitSlot(keyRef{account: f.FeePayer, keyID: f.KeyID}, f.FeeToken)
		refs = append(refs, SlotRef{Address: AccountKeychainAddress, Key: Hash(limit)})
	}
	c.Env.NonCreditable = refs
}

const maxCreditOwners = 1 << 12

func (c *Context) noteCreditOwner(owner Address) {
	for _, o := range c.Env.CreditOwners {
		if o == owner {
			return
		}
	}
	if len(c.Env.CreditOwners) < maxCreditOwners {
		c.Env.CreditOwners = append(c.Env.CreditOwners, owner)
	}
}

func SettleStorageCreditRefunds(c *Context) (int64, error) {
	settled := int64(0)
	for _, owner := range c.Env.CreditOwners {
		s := creditSlot(owner)
		state, err := decodeCreditState(c.State.TransientStorage(StorageCreditsAddress, Hash(s)))
		if err != nil {
			return 0, err
		}
		c.State.SetTransientStorage(StorageCreditsAddress, Hash(s), Word{})
		if state.pending == 0 {
			continue
		}
		raw := c.State.Storage(StorageCreditsAddress, Hash(s))
		if !raw.IsUint64() {
			return 0, errFatal
		}
		credits := min(state.pending, raw.Uint64())
		if credits == 0 {
			continue
		}
		c.journalWrite(StorageCreditsAddress, s, u256.FromUint64(raw.Uint64()-credits))
		settled += int64(credits)
	}
	c.Env.CreditOwners = nil
	return settled * int64(gasStorageCredit), nil
}
