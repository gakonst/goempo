package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

func (c *Context) creditBalance(owner Address) (uint64, error) {
	w, err := c.sload(StorageCreditsAddress, creditSlot(owner))
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

func (c *Context) creditStateOf(owner Address) (creditState, error) {
	w, err := c.tload(StorageCreditsAddress, creditSlot(owner))
	if err != nil {
		return creditState{}, err
	}
	return decodeCreditState(w)
}

func (c *Context) writeCreditState(owner Address, s creditState) error {
	return c.tstore(StorageCreditsAddress, creditSlot(owner), s.word())
}

type creditMode struct {
	mode   uint64
	budget uint64
}

func (c *Context) setCreditMode(owner Address, m creditMode) error {
	s, err := c.creditStateOf(owner)
	if err != nil {
		return err
	}
	s.mode = m.mode
	s.budget = m.budget
	return c.writeCreditState(owner, s)
}

func (c *Context) preserveCredits(owner Address) error {
	if !c.at(hardfork.T7) {
		return nil
	}
	return c.setCreditMode(owner, creditMode{mode: creditModePreserve})
}

func (c *Context) mintedSince(owner Address, before uint64) (uint64, error) {
	if !c.at(hardfork.T7) {
		return 0, nil
	}
	after, err := c.creditBalance(owner)
	if err != nil {
		return 0, err
	}
	if after < before {
		return 0, errFatal
	}
	return after - before, nil
}

type creditBudget struct {
	owner  Address
	limit  uint64
	before uint64
}

func (c *Context) budgetBegin(owner Address, limit uint64) (creditBudget, error) {
	b := creditBudget{owner: owner, limit: limit}
	if !c.at(hardfork.T7) {
		return b, nil
	}
	if limit != 0 {
		if err := c.setCreditMode(owner, creditMode{mode: creditModeDirect, budget: limit}); err != nil {
			return b, err
		}
	}
	before, err := c.creditBalance(owner)
	b.before = before
	return b, err
}

type creditDelta struct {
	spent  uint64
	minted uint64
}

func (d creditDelta) spentOne() bool { return d.spent == 1 && d.minted == 0 }

func (c *Context) budgetEnd(b creditBudget) (creditDelta, error) {
	if !c.at(hardfork.T7) {
		return creditDelta{}, nil
	}
	after, err := c.creditBalance(b.owner)
	if err != nil {
		return creditDelta{}, err
	}
	if b.limit != 0 {
		s, err := c.creditStateOf(b.owner)
		if err != nil {
			return creditDelta{}, err
		}
		s.budget = 0
		s.mode = creditModePreserve
		if err := c.writeCreditState(b.owner, s); err != nil {
			return creditDelta{}, err
		}
	}
	if after < b.before {
		return creditDelta{spent: b.before - after}, nil
	}
	return creditDelta{minted: after - b.before}, nil
}
