package memstate

import (
	"bytes"
	"slices"

	"github.com/gakonst/goempo/internal/u256"
)

func sortedSlots(m map[Hash]u256.Int) []Hash {
	keys := make([]Hash, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Hash) int { return bytes.Compare(a[:], b[:]) })
	return keys
}

func sortedAccounts(m map[Address]*account) []Address {
	keys := make([]Address, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Address) int { return bytes.Compare(a[:], b[:]) })
	return keys
}

func sortedSet(m map[Address]struct{}) []Address {
	keys := make([]Address, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Address) int { return bytes.Compare(a[:], b[:]) })
	return keys
}
