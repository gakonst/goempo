package state

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// Commit seals the block's changes, computes its state root and writes it.
func (s *State) Commit(number uint64) (Hash, error) {
	if err := s.CommitAsync(number); err != nil {
		return Hash{}, err
	}
	done, err := s.WaitCommit()
	if err != nil {
		return Hash{}, err
	}
	return done.Root, nil
}

// Committed is a block whose state root a pipelined commit computed.
type Committed struct {
	Number uint64
	Root   Hash
}

// ErrCommitPending reports a read of state that a pipelined commit has not
// settled yet.
var ErrCommitPending = errors.New("state: commit still pending")

// pendingRoot marks a storage root that a pipelined commit is computing. It is
// never empty, so reads fall through to stored slots (and the slots the
// block changed are in the account's cache).
var pendingRoot = Hash{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

type slotValue struct {
	key Hash
	v   u256.Int
}

// objectSnap is one account as a block left it, taken when the block is
// sealed, so the next block can change the live object while the root is
// computed.
type objectSnap struct {
	o          *object
	addr       Address
	exists     bool
	baseExists bool
	baseRoot   Hash
	wiped      bool
	codeDirty  bool
	nonce      uint64
	balance    u256.Int
	codeHash   Hash
	code       []byte
	slots      []slotValue
}

type pendingCommit struct {
	number uint64
	snaps  []objectSnap
	roots  []Hash
	head   Head
	batch  storage.Batch
	err    error
	done   chan struct{}
}

// CommitAsync seals the block's changes and starts computing its state root
// on another goroutine, after applying the previous pipelined commit. The
// next block can execute on this State meanwhile: the accounts it changed stay
// cached until WaitCommit or the next CommitAsync applies the result. At most
// one commit is in flight, and store writes happen only on the caller's
// goroutine while none is.
func (s *State) CommitAsync(number uint64) error {
	s.FinalizeTx()
	db := s.db
	if _, err := s.WaitCommit(); err != nil && !errors.Is(err, errNothingPending) {
		return err
	}
	if db.err != nil {
		return db.err
	}
	if db.head.Loaded && number <= db.head.Number {
		return ErrBlockOrder
	}
	if s.err != nil {
		db.err = s.err
		return s.err
	}
	p := &pendingCommit{number: number, done: make(chan struct{})}
	addrs := sortedAddresses(s.blockDirty)
	p.snaps = make([]objectSnap, len(addrs))
	for i, addr := range addrs {
		o := s.accounts[addr]
		assert.Ok(o != nil, "state commits an account it never loaded")
		p.snaps[i] = s.seal(o)
	}
	clear(s.blockDirty)
	db.pending = p
	go db.runCommit(p)
	return nil
}

var errNothingPending = errors.New("state: no commit pending")

// WaitCommit waits for the pipelined commit in flight, writes it and settles
// its storage roots into the cached accounts.
func (s *State) WaitCommit() (Committed, error) {
	db := s.db
	p := db.pending
	if p == nil {
		return Committed{}, errNothingPending
	}
	<-p.done
	db.pending = nil
	if p.err != nil {
		db.err = p.err
		return Committed{}, p.err
	}
	if err := db.store.Write(&p.batch); err != nil {
		db.err = err
		return Committed{}, err
	}
	db.head = p.head
	for i := range p.snaps {
		sn := &p.snaps[i]
		settleRoot(sn.o, p.roots[i])
		if cur := s.accounts[sn.addr]; cur != nil && cur != sn.o && cur.base.storageRoot == pendingRoot {
			settleRoot(cur, p.roots[i])
		}
	}
	s.trimCaches()
	return Committed{Number: p.number, Root: p.head.Root}, nil
}

func settleRoot(o *object, root Hash) {
	if o.base.storageRoot == pendingRoot {
		o.base.storageRoot = root
	}
	o.pendingWipe = false
}

// seal snapshots o for the commit and settles the live object provisionally.
func (s *State) seal(o *object) objectSnap {
	sn := objectSnap{
		o: o, addr: o.addr, exists: o.exists, baseExists: o.base.exists, baseRoot: o.base.storageRoot,
		wiped: o.wiped, codeDirty: o.codeDirty, nonce: o.nonce, balance: o.balance, codeHash: o.codeHash, code: o.code,
	}
	assert.Ok(sn.baseRoot != pendingRoot, "sealed account has an unsettled storage root")
	if len(o.changed) > 0 && o.exists {
		sn.slots = make([]slotValue, 0, len(o.changed))
		for _, k := range sortedHashes(o.changed) {
			sn.slots = append(sn.slots, slotValue{key: k, v: o.committed[k]})
		}
	}
	if o.codeDirty && o.codeHash != emptyCodeHash {
		s.db.cacheCode(o.codeHash, o.code)
	}
	root := emptyRoot
	switch {
	case !o.exists:
	case len(sn.slots) > 0:
		root = pendingRoot
	case !o.wiped:
		root = o.base.storageRoot
	}
	o.pendingWipe = o.wiped && root == pendingRoot
	o.base = base{exists: o.exists, storageRoot: root}
	o.wiped = false
	o.codeDirty = false
	clear(o.changed)
	return sn
}

func (s *State) trimCaches() {
	db := s.db
	if len(s.accounts) > db.opts.CacheAccounts {
		clear(s.accounts)
		s.cachedSlots = 0
	}
	if s.cachedSlots > db.opts.CacheSlots {
		clear(s.accounts)
		s.cachedSlots = 0
	}
}

// runCommit computes a sealed block's storage roots, account trie root and
// write batch. It reads the store and owns the tries; it writes nothing.
func (db *DB) runCommit(p *pendingCommit) {
	defer close(p.done)
	defer func() {
		if r := recover(); r != nil {
			p.err = fmt.Errorf("state commit %d: %v", p.number, r)
		}
	}()
	b := &p.batch
	jobs := make([]*storageJob, len(p.snaps))
	for i := range p.snaps {
		sn := &p.snaps[i]
		if sn.wiped {
			db.forgetStorageTrie(sn.addr)
			if err := db.wipeSlots(sn, b); err != nil {
				p.err = err
				return
			}
		}
		if !sn.exists || len(sn.slots) == 0 {
			continue
		}
		t := db.storageTrie(sn.addr, snapBase(sn))
		jobs[i] = &storageJob{sn: sn, t: t, before: t.Nodes()}
	}
	if err := runStorageJobs(jobs, db.opts.StorageNodes); err != nil {
		p.err = err
		return
	}
	p.roots = make([]Hash, len(p.snaps))
	for i := range p.snaps {
		root, err := db.finishSnap(&p.snaps[i], jobs[i], b)
		if err != nil {
			p.err = err
			return
		}
		p.roots[i] = root
	}
	root := db.accounts.Commit(nodeWriter{batch: b, prefix: []byte{prefixAccountNode}})
	p.head = Head{Number: p.number, Root: root, Loaded: true}
	b.Put(headKey, encodeHead(p.head))
	db.trimTries()
}

func (db *DB) trimTries() {
	if db.accounts.Nodes() > db.opts.AccountNodes {
		db.accounts.Evict(db.opts.TrieKeepDepth)
	}
	if len(db.storageTries) > db.opts.StorageTries {
		db.dropStorageTries()
	}
	if db.storageNodes > db.opts.StorageNodesTotal {
		db.shrinkStorageTries()
	}
}

func snapBase(sn *objectSnap) Hash {
	if sn.wiped {
		return emptyRoot
	}
	return sn.baseRoot
}

// storageJob is one account's storage trie update, run on a worker.
type storageJob struct {
	sn     *objectSnap
	t      *trie.Trie
	before int
	root   Hash
	batch  storage.Batch
	err    error
}

// parallelSlotsMin is the number of changed slots in a block below which
// storage tries are updated on one goroutine.
const parallelSlotsMin = 64

// runStorageJobs updates every job's storage trie and computes its root.
// Each account's trie, slots and batch are its own, so jobs run in parallel;
// the largest start first.
func runStorageJobs(all []*storageJob, nodesMax int) error {
	jobs := make([]*storageJob, 0, len(all))
	slots := 0
	for _, j := range all {
		if j != nil {
			jobs = append(jobs, j)
			slots += len(j.sn.slots)
		}
	}
	workers := min(runtime.GOMAXPROCS(0), len(jobs))
	if workers <= 1 || slots < parallelSlotsMin {
		w := newStorageWorker()
		for _, j := range jobs {
			w.run(j, nodesMax)
			if j.err != nil {
				return j.err
			}
		}
		return nil
	}
	slices.SortFunc(jobs, func(a, b *storageJob) int { return len(b.sn.slots) - len(a.sn.slots) })
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			w := newStorageWorker()
			for {
				k := int(next.Add(1) - 1)
				if k >= len(jobs) {
					return
				}
				w.run(jobs[k], nodesMax)
			}
		})
	}
	wg.Wait()
	for _, j := range jobs {
		if j.err != nil {
			return j.err
		}
	}
	return nil
}

type storageWorker struct {
	hasher *keccak.Hasher
	enc    *rlp.Encoder
}

func newStorageWorker() storageWorker {
	return storageWorker{hasher: keccak.New(), enc: rlp.NewEncoder(64)}
}

func (w storageWorker) run(j *storageJob, nodesMax int) {
	sn := j.sn
	for _, sv := range sn.slots {
		hk := w.hasher.Hash(sv.key[:])
		sk := slotKeyBytes(sn.addr, sv.key)
		if sv.v.IsZero() {
			j.batch.Delete(sk)
			if j.err = j.t.Unset(hk[:]); j.err != nil {
				return
			}
			continue
		}
		enc := encodeSlot(sv.v)
		j.batch.Put(sk, enc)
		w.enc.Reset()
		w.enc.WriteBytes(enc)
		if j.err = j.t.Set(hk[:], w.enc.Encoded()); j.err != nil {
			return
		}
	}
	j.root = j.t.Commit(nodeWriter{batch: &j.batch, prefix: storageNodePrefix(sn.addr)})
	if j.t.Nodes() > nodesMax {
		j.t.Evict(storageKeepDepthDefault)
	}
}

// finishSnap writes one account after its storage job ran: its storage
// writes, code, flat entry and account trie leaf, in address order.
func (db *DB) finishSnap(sn *objectSnap, j *storageJob, b *storage.Batch) (Hash, error) {
	hashed := db.hasher.Hash(sn.addr[:])
	if !sn.exists {
		if !sn.baseExists {
			return emptyRoot, nil
		}
		b.Delete(accountKey(sn.addr))
		return emptyRoot, db.accounts.Unset(hashed[:])
	}
	root := snapBase(sn)
	if j != nil {
		b.Append(&j.batch)
		db.storageNodes += j.t.Nodes() - j.before
		root = j.root
	}
	if sn.codeDirty && sn.codeHash != emptyCodeHash {
		b.Put(codeKey(sn.codeHash), sn.code)
	}
	fa := flatAccount{nonce: sn.nonce, balance: sn.balance, codeHash: sn.codeHash, storageRoot: root}
	b.Put(accountKey(sn.addr), fa.encode())
	return root, db.accounts.Set(hashed[:], db.encodeAccount(&fa))
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

func (db *DB) wipeSlots(sn *objectSnap, b *storage.Batch) error {
	if !sn.baseExists || sn.baseRoot == emptyRoot {
		return nil
	}
	del := func(e storage.Entry) error {
		b.Delete(e.Key)
		return nil
	}
	if err := db.store.Iterate(slotPrefix(sn.addr), del); err != nil {
		return err
	}
	return db.store.Iterate(storageNodePrefix(sn.addr), del)
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

func (db *DB) storageTrie(addr Address, root Hash) *trie.Trie {
	if t, ok := db.storageTries[addr]; ok {
		assert.Ok(t.Hash() == root, "cached storage trie matches the account's storage root")
		return t
	}
	t := trie.Open(root, nodeReader{store: db.store, prefix: storageNodePrefix(addr)})
	db.storageTries[addr] = t
	db.storageNodes += t.Nodes()
	return t
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
