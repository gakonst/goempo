package state

import (
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
)

type rebuilder struct {
	db       *DB
	accounts *trie.Trie
	slots    *trie.Trie
	addr     Address
	open     bool
}

func (db *DB) RebuildRoot() (Hash, error) {
	r := &rebuilder{db: db, accounts: trie.New()}
	slotRoots := make(map[Address]Hash)
	err := db.store.Iterate([]byte{prefixSlot}, func(e storage.Entry) error {
		return r.slot(e, slotRoots)
	})
	if err != nil {
		return Hash{}, err
	}
	r.closeSlots(slotRoots)
	err = db.store.Iterate([]byte{prefixAccount}, func(e storage.Entry) error {
		return r.account(e, slotRoots)
	})
	if err != nil {
		return Hash{}, err
	}
	return r.accounts.Hash(), nil
}

func (r *rebuilder) slot(e storage.Entry, roots map[Address]Hash) error {
	if len(e.Key) != 1+len(Address{})+len(Hash{}) {
		return ErrCorrupt
	}
	var addr Address
	copy(addr[:], e.Key[1:])
	if !r.open || addr != r.addr {
		r.closeSlots(roots)
		r.slots = trie.New()
		r.addr = addr
		r.open = true
	}
	if _, err := decodeSlot(e.Value); err != nil {
		return err
	}
	hk := r.db.hasher.Hash(e.Key[1+len(addr):])
	r.db.enc.Reset()
	r.db.enc.WriteBytes(e.Value)
	r.slots.Update(hk[:], r.db.enc.Encoded())
	return nil
}

func (r *rebuilder) closeSlots(roots map[Address]Hash) {
	if !r.open {
		return
	}
	roots[r.addr] = r.slots.Hash()
	r.open = false
}

func (r *rebuilder) account(e storage.Entry, roots map[Address]Hash) error {
	if len(e.Key) != 1+len(Address{}) {
		return ErrCorrupt
	}
	fa, err := decodeAccount(e.Value)
	if err != nil {
		return err
	}
	var addr Address
	copy(addr[:], e.Key[1:])
	want, ok := roots[addr]
	if !ok {
		want = emptyRoot
	}
	if want != fa.storageRoot {
		return ErrCorrupt
	}
	ha := r.db.hasher.Hash(addr[:])
	r.accounts.Update(ha[:], r.db.encodeAccount(&fa))
	return nil
}
