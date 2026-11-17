package precompiles

import "github.com/gakonst/goempo/internal/tempo/hardfork"

const creditModeCount = creditModeDirect + 1

func (c *Context) setCreditModeABI(mode uint64) error {
	if mode >= creditModeCount {
		if c.at(hardfork.T11) {
			return revert(nil)
		}
		return errSel("InvalidMode()")
	}
	budget := uint64(0)
	if mode == creditModeDirect {
		budget = ^uint64(0)
	}
	return c.setCreditMode(c.Caller, creditMode{mode: mode, budget: budget})
}

var storageCreditsTable = register(StorageCreditsAddress, newTable(
	view("balanceOf(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		b, err := c.creditBalance(a[0].addr())
		return retU64(b), err
	}),
	view("modeOf(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		s, err := c.creditStateOf(a[0].addr())
		return retU64(s.mode), err
	}),
	view("budgetOf(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		s, err := c.creditStateOf(a[0].addr())
		return retU64(s.budget), err
	}),
	mutate("setMode(uint8)", []abiType{tU8}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.setCreditModeABI(a[0].u64())
	}),
	mutate("setBudget(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.setCreditMode(c.Caller, creditMode{mode: creditModeDirect, budget: a[0].u64()})
	}),
))
