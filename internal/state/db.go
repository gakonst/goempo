package state

import (
	"bytes"
	"errors"
	"math"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

var (
	ErrCorrupt    = errors.New("state: stored value is corrupt")
	ErrNotEmpty   = errors.New("state: genesis loaded into a database that already holds a state")
	ErrBlockOrder = errors.New("state: block committed at or below the head")
)

var emptyRoot = trie.EmptyRoot

const (
	cacheAccountsDefault    defaultLimit = 1 << 20
	cacheSlotsDefault       defaultLimit = 4 << 20
	accountNodesDefault     defaultLimit = 1 << 20
	storageTriesDefault     defaultLimit = 1 << 12
	storageNodesDefault     defaultLimit = 1 << 14
	storageTotalDefault     defaultLimit = 1 << 21
	codeCacheBytesDefault   defaultLimit = 64 << 20
	trieKeepDepthDefault    defaultLimit = 5
	storageKeepDepthDefault              = 2
	cacheLimitMax                        = 1 << 32
)

const _ = uint(math.MaxInt - cacheLimitMax)

type defaultLimit int

type Options struct {
	MemtableBytes     int
	CacheAccounts     int
	CacheSlots        int
	AccountNodes      int
	StorageTries      int
	StorageNodes      int
	StorageNodesTotal int
	CodeCacheBytes    int
	TrieKeepDepth     int
}

func (o Options) withDefaults() Options {
	o.CacheAccounts = clampOption(o.CacheAccounts, cacheAccountsDefault)
	o.CacheSlots = clampOption(o.CacheSlots, cacheSlotsDefault)
	o.AccountNodes = clampOption(o.AccountNodes, accountNodesDefault)
	o.StorageTries = clampOption(o.StorageTries, storageTriesDefault)
	o.StorageNodes = clampOption(o.StorageNodes, storageNodesDefault)
	o.StorageNodesTotal = clampOption(o.StorageNodesTotal, storageTotalDefault)
	o.CodeCacheBytes = clampOption(o.CodeCacheBytes, codeCacheBytesDefault)
	o.TrieKeepDepth = clampOption(o.TrieKeepDepth, trieKeepDepthDefault)
	return o
}

func clampOption(v int, fallback defaultLimit) int {
	assert.Ok(fallback < cacheLimitMax, "state option default below cacheLimitMax")
	if v <= 0 {
		return int(fallback)
	}
	return min(v, cacheLimitMax)
}

type Head struct {
	Number uint64
	Root   Hash
	Loaded bool
}

type DB struct {
	store        *storage.DB
	opts         Options
	head         Head
	accounts     *trie.Trie
	storageTries map[Address]*trie.Trie
	storageNodes int
	codes        map[Hash][]byte
	codeBytes    int
	state        *State
	batch        storage.Batch
	enc          *rlp.Encoder
	hasher       *keccak.Hasher
	err          error
}

func Open(fs goio.FS, opts Options) (*DB, error) {
	opts = opts.withDefaults()
	store, err := storage.Open(fs, storage.Options{MemtableBytes: opts.MemtableBytes})
	if err != nil {
		return nil, err
	}
	head := Head{Root: emptyRoot}
	raw, err := store.Get(headKey)
	switch {
	case err == nil:
		head, err = decodeHead(raw)
		if err != nil {
			return nil, errors.Join(err, store.Close())
		}
	case errors.Is(err, storage.ErrNotFound):
	default:
		return nil, errors.Join(err, store.Close())
	}
	db := &DB{
		store:        store,
		opts:         opts,
		head:         head,
		storageTries: make(map[Address]*trie.Trie),
		codes:        make(map[Hash][]byte),
		enc:          rlp.NewEncoder(128),
		hasher:       keccak.New(),
	}
	db.accounts = trie.Open(head.Root, nodeReader{store: store, prefix: []byte{prefixAccountNode}})
	db.state = newState(db)
	return db, nil
}

func (db *DB) Head() Head { return db.head }

func (db *DB) State() *State { return db.state }

func (db *DB) Store() *storage.DB { return db.store }

func (db *DB) Flush() error { return db.store.Flush() }

func (db *DB) Close() error { return db.store.Close() }

func (db *DB) LoadGenesis(g *genesis.Genesis) (Hash, error) {
	if db.head.Loaded {
		return Hash{}, ErrNotEmpty
	}
	s := db.state
	for i := range g.Alloc {
		s.loadGenesisAccount(&g.Alloc[i])
	}
	root, err := s.Commit(0)
	if err != nil {
		return Hash{}, err
	}
	return root, db.store.Flush()
}

func (s *State) loadGenesisAccount(a *genesis.Account) {
	o := s.load(a.Address)
	o.exists = true
	o.nonce = a.Nonce
	o.balance = u256.FromBytes32(a.Balance)
	o.code = bytes.Clone(a.Code)
	o.codeHash = keccak.Sum256(a.Code)
	o.codeLoaded = true
	o.codeDirty = true
	for _, slot := range a.Storage {
		o.setChanged(slot.Key, u256.FromBytes32(slot.Value))
		s.cachedSlots++
	}
	s.blockDirty[a.Address] = struct{}{}
}

func (db *DB) readSlot(addr Address, key Hash) (u256.Int, error) {
	raw, err := db.store.Get(slotKeyBytes(addr, key))
	if errors.Is(err, storage.ErrNotFound) {
		return u256.Zero(), nil
	}
	if err != nil {
		return u256.Zero(), err
	}
	return decodeSlot(raw)
}

func (db *DB) code(h Hash) ([]byte, error) {
	if c, ok := db.codes[h]; ok {
		return c, nil
	}
	c, err := db.store.Get(codeKey(h))
	if err != nil {
		return nil, err
	}
	db.cacheCode(h, c)
	return c, nil
}

func (db *DB) cacheCode(h Hash, c []byte) {
	if db.codeBytes+len(c) > db.opts.CodeCacheBytes {
		clear(db.codes)
		db.codeBytes = 0
	}
	db.codes[h] = c
	db.codeBytes += len(c)
}

func (db *DB) anyStoredSlot(s *State, o *object) bool {
	found := false
	err := db.store.Iterate(slotPrefix(o.addr), func(e storage.Entry) error {
		var key Hash
		copy(key[:], e.Key[1+len(o.addr):])
		v, ok := o.dirty[key]
		if !ok {
			v, ok = o.committed[key]
		}
		if !ok {
			stored, err := decodeSlot(e.Value)
			if err != nil {
				return err
			}
			v = stored
		}
		if v.IsZero() {
			return nil
		}
		found = true
		return errStop
	})
	if err != nil {
		if !errors.Is(err, errStop) {
			s.fail(err)
		}
	}
	return found
}

type nodeReader struct {
	store  *storage.DB
	prefix []byte
}

func (r nodeReader) ReadNode(path []byte) ([]byte, error) {
	key := make([]byte, 0, nodeKeyBytesMax)
	key = append(key, r.prefix...)
	return r.store.Get(appendPath(key, nibbles(path)))
}

type nodeWriter struct {
	batch  *storage.Batch
	prefix []byte
}

func (w nodeWriter) WriteNode(n trie.StoredNode) {
	key := make([]byte, 0, nodeKeyBytesMax)
	key = append(key, w.prefix...)
	w.batch.Put(appendPath(key, nibbles(n.Path)), n.Encoded)
}

func storageNodePrefix(addr Address) []byte {
	p := make([]byte, 0, 1+len(addr))
	p = append(p, prefixStorageNode)
	return append(p, addr[:]...)
}

var ErrCodeNotFound = errors.New("state: code not found")

func (db *DB) CodeByHash(h Hash) ([]byte, error) {
	if h == emptyCodeHash {
		return nil, nil
	}
	c, err := db.code(h)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, ErrCodeNotFound
	}
	return c, err
}
