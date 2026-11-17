package precompiles

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
)

func IsTIP20Deployed(c *Context, token Address) (bool, error) {
	if !IsTIP20(token) {
		return false, nil
	}
	code, err := c.accountCode(token)
	if err != nil {
		return false, err
	}
	return len(code) > 0, nil
}

func addressKey(a Address) []byte {
	w := addressWord(a)
	return w[:]
}

func TIP20BalanceSlot(holder Address) Hash {
	return keccak.Sum256Concat(addressKey(holder), slotBalances[:])
}

func TIP20RoleSlot(account Address, role Hash) Hash {
	inner := keccak.Sum256Concat(addressKey(account), slotRoles[:])
	return keccak.Sum256Concat(role[:], inner[:])
}

func FeeUserTokenSlot(user Address) Hash {
	return keccak.Sum256Concat(addressKey(user), slotFeeUserTokens[:])
}

var (
	RoleIssuer = roleIssuer
	RolePause  = rolePause
)
