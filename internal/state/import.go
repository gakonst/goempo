package state

import "github.com/gakonst/goempo/internal/u256"

// ImportSlot is one non-zero storage slot of an imported account.
type ImportSlot struct {
	Key   Hash
	Value [32]byte
}

// ImportAccount stages one account of a state imported from another client
// (goempo-dev import), with its code and non-zero storage, for the next
// Commit. The account must not exist yet.
func (s *State) ImportAccount(addr Address, nonce uint64, balance [32]byte, codeHash Hash, code []byte, slots []ImportSlot) {
	o := s.load(addr)
	o.exists = true
	o.nonce = nonce
	o.balance = u256.FromBytes32(balance)
	o.codeHash = codeHash
	o.code = code
	o.codeLoaded = true
	o.codeDirty = codeHash != emptyCodeHash
	for _, sl := range slots {
		o.setChanged(sl.Key, u256.FromBytes32(sl.Value))
		s.cachedSlots++
	}
	s.blockDirty[addr] = struct{}{}
}
