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
