package state

import (
	"github.com/gakonst/goempo/internal/trie"
)

func (s *State) PendingRoot() (Hash, error) {
	s.FinalizeTx()
	db := s.db
	if err := db.settlePending(); err != nil {
		return Hash{}, err
	}
	if db.err != nil {
		return Hash{}, db.err
	}
	if s.err != nil {
		return Hash{}, s.err
	}
	accounts := trie.Open(db.head.Root, nodeReader{store: db.store, prefix: []byte{prefixAccountNode}})
	for _, addr := range sortedAddresses(s.blockDirty) {
		if err := db.pendingObject(accounts, s.accounts[addr]); err != nil {
			return Hash{}, err
		}
	}
	return accounts.Hash(), nil
}

func (db *DB) pendingObject(accounts *trie.Trie, o *object) error {
	hashed := db.hasher.Hash(o.addr[:])
	if !o.exists {
		if !o.base.exists {
			return nil
		}
		return accounts.Unset(hashed[:])
	}
	root, err := db.pendingStorage(o)
	if err != nil {
		return err
	}
	fa := flatAccount{nonce: o.nonce, balance: o.balance, codeHash: o.codeHash, storageRoot: root}
	return accounts.Set(hashed[:], db.encodeAccount(&fa))
}

func (db *DB) pendingStorage(o *object) (Hash, error) {
	baseRoot := o.base.storageRoot
	if o.wiped {
		baseRoot = emptyRoot
	}
	if len(o.changed) == 0 {
		return baseRoot, nil
	}
	t := trie.Open(baseRoot, nodeReader{store: db.store, prefix: storageNodePrefix(o.addr)})
	for _, k := range sortedHashes(o.changed) {
		v := o.committed[k]
		hk := db.hasher.Hash(k[:])
		if v.IsZero() {
			if err := t.Unset(hk[:]); err != nil {
				return Hash{}, err
			}
			continue
		}
		db.enc.Reset()
		db.enc.WriteBytes(encodeSlot(v))
		if err := t.Set(hk[:], db.enc.Encoded()); err != nil {
			return Hash{}, err
		}
	}
	return t.Hash(), nil
}
