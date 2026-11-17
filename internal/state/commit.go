package state

import (
	"bytes"
	"slices"

	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/kapetan-io/tiger/assert"
)

func (s *State) Commit(number uint64) (Hash, error) {
	s.FinalizeTx()
	db := s.db
	if db.err != nil {
		return Hash{}, db.err
	}
	if db.head.Loaded && number <= db.head.Number {
		return Hash{}, ErrBlockOrder
	}
	if s.err != nil {
		db.err = s.err
		return Hash{}, s.err
	}
	addrs := sortedAddresses(s.blockDirty)
	b := &db.batch
	b.Reset()
	roots := make([]Hash, len(addrs))
	for i, addr := range addrs {
		root, err := db.commitObject(s.accounts[addr], b)
		if err != nil {
			db.err = err
			return Hash{}, err
		}
		roots[i] = root
	}
	root := db.accounts.Commit(nodeWriter{batch: b, prefix: []byte{prefixAccountNode}})
	head := Head{Number: number, Root: root, Loaded: true}
	b.Put(headKey, encodeHead(head))
	if err := db.store.Write(b); err != nil {
		db.err = err
		return Hash{}, err
	}
	b.Reset()
	db.head = head
	for i, addr := range addrs {
		s.settle(s.accounts[addr], roots[i])
	}
	clear(s.blockDirty)
	s.trimCaches()
	return root, nil
}

func (s *State) settle(o *object, root Hash) {
	o.base = base{exists: o.exists, storageRoot: root}
	o.wiped = false
	o.codeDirty = false
	clear(o.changed)
}

func (s *State) trimCaches() {
	db := s.db
	if db.accounts.Nodes() > db.opts.AccountNodes {
		db.accounts.Evict(db.opts.TrieKeepDepth)
	}
	if len(db.storageTries) > db.opts.StorageTries {
		db.dropStorageTries()
	}
	if db.storageNodes > db.opts.StorageNodesTotal {
		db.shrinkStorageTries()
	}
	if len(s.accounts) > db.opts.CacheAccounts {
		clear(s.accounts)
		s.cachedSlots = 0
	}
	if s.cachedSlots > db.opts.CacheSlots {
		clear(s.accounts)
		s.cachedSlots = 0
	}
}

func (db *DB) commitObject(o *object, b *storage.Batch) (Hash, error) {
	assert.Ok(o != nil, "state commits an account it never loaded")
	hashed := db.hasher.Hash(o.addr[:])
	if o.wiped {
		db.forgetStorageTrie(o.addr)
		if err := db.wipeSlots(o, b); err != nil {
			return Hash{}, err
		}
	}
	if !o.exists {
		if !o.base.exists {
			return emptyRoot, nil
		}
		b.Delete(accountKey(o.addr))
		return emptyRoot, db.accounts.Unset(hashed[:])
	}
	root, err := db.commitStorage(o, b)
	if err != nil {
		return Hash{}, err
	}
	if o.codeDirty {
		db.putCode(o, b)
	}
	fa := flatAccount{nonce: o.nonce, balance: o.balance, codeHash: o.codeHash, storageRoot: root}
	b.Put(accountKey(o.addr), fa.encode())
	return root, db.accounts.Set(hashed[:], db.encodeAccount(&fa))
}

func (db *DB) putCode(o *object, b *storage.Batch) {
	if o.codeHash == emptyCodeHash {
		return
	}
	b.Put(codeKey(o.codeHash), o.code)
	db.cacheCode(o.codeHash, o.code)
}

func (db *DB) encodeAccount(fa *flatAccount) []byte {
	db.enc.Reset()
	db.enc.ListStart()
	db.enc.WriteUint64(fa.nonce)
	bal := fa.balance.Bytes32()
	db.enc.WriteUintBytes(bal[:])
	db.enc.WriteBytes(fa.storageRoot[:])
	db.enc.WriteBytes(fa.codeHash[:])
	db.enc.ListEnd()
	return db.enc.Encoded()
}

func (db *DB) wipeSlots(o *object, b *storage.Batch) error {
	if !o.base.exists {
		return nil
	}
	if o.base.storageRoot == emptyRoot {
		return nil
	}
	del := func(e storage.Entry) error {
		b.Delete(e.Key)
		return nil
	}
	if err := db.store.Iterate(slotPrefix(o.addr), del); err != nil {
		return err
	}
	return db.store.Iterate(storageNodePrefix(o.addr), del)
}

func (db *DB) forgetStorageTrie(addr Address) {
	t, ok := db.storageTries[addr]
	if !ok {
		return
	}
	db.storageNodes -= t.Nodes()
	assert.Ok(db.storageNodes >= 0, "storage trie node count went negative")
	delete(db.storageTries, addr)
}

func (db *DB) dropStorageTries() {
	clear(db.storageTries)
	db.storageNodes = 0
}

func (db *DB) shrinkStorageTries() {
	total := 0
	for _, addr := range sortedAddresses(db.storageTries) {
		t := db.storageTries[addr]
		t.Evict(storageKeepDepthDefault)
		total += t.Nodes()
	}
	db.storageNodes = total
	if total > db.opts.StorageNodesTotal/2 {
		db.dropStorageTries()
	}
}

func (db *DB) storageTrie(o *object, root Hash) *trie.Trie {
	if t, ok := db.storageTries[o.addr]; ok {
		assert.Ok(t.Hash() == root, "cached storage trie matches the account's storage root")
		return t
	}
	t := trie.Open(root, nodeReader{store: db.store, prefix: storageNodePrefix(o.addr)})
	db.storageTries[o.addr] = t
	db.storageNodes += t.Nodes()
	return t
}

func (db *DB) commitStorage(o *object, b *storage.Batch) (Hash, error) {
	baseRoot := o.base.storageRoot
	if o.wiped {
		baseRoot = emptyRoot
	}
	if len(o.changed) == 0 {
		return baseRoot, nil
	}
	t := db.storageTrie(o, baseRoot)
	before := t.Nodes()
	keys := sortedHashes(o.changed)
	for _, k := range keys {
		if err := db.commitSlot(slotWrite{t: t, o: o, key: k}, b); err != nil {
			return Hash{}, err
		}
	}
	root := t.Commit(nodeWriter{batch: b, prefix: storageNodePrefix(o.addr)})
	if t.Nodes() > db.opts.StorageNodes {
		t.Evict(storageKeepDepthDefault)
	}
	db.storageNodes += t.Nodes() - before
	return root, nil
}

type slotWrite struct {
	t   *trie.Trie
	o   *object
	key Hash
}

func (db *DB) commitSlot(w slotWrite, b *storage.Batch) error {
	v := w.o.committed[w.key]
	hk := db.hasher.Hash(w.key[:])
	sk := slotKeyBytes(w.o.addr, w.key)
	if v.IsZero() {
		b.Delete(sk)
		return w.t.Unset(hk[:])
	}
	enc := encodeSlot(v)
	b.Put(sk, enc)
	db.enc.Reset()
	db.enc.WriteBytes(enc)
	return w.t.Set(hk[:], db.enc.Encoded())
}

func sortedAddresses[V any](m map[Address]V) []Address {
	keys := make([]Address, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Address) int { return bytes.Compare(a[:], b[:]) })
	return keys
}

func sortedHashes[V any](m map[Hash]V) []Hash {
	keys := make([]Hash, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Hash) int { return bytes.Compare(a[:], b[:]) })
	return keys
}
