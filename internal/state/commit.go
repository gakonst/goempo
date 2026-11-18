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
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// Commit seals the block's changes, computes its state root and writes it,
// on the caller's goroutine.
func (s *State) Commit(number uint64) (Hash, error) {
	p, err := s.sealBlock(number)
	if err != nil {
		return Hash{}, err
	}
	s.db.runCommit(p)
	done, err := s.applyCommit(p)
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
	job        *storageJob
}

type pendingCommit struct {
	number uint64
	snaps  []objectSnap
	slots  []slotValue
	roots  []Hash
	hashed []Hash
	out    []Hash
	missed []int
	msgs   [][]byte
	jobs   []storageJob
	order  []*storageJob
	head   Head
	batch  storage.Batch
	keys   keyArena
	err    error
}

func (p *pendingCommit) reset(number uint64) {
	clear(p.snaps)
	p.snaps = p.snaps[:0]
	p.slots = p.slots[:0]
	p.roots = p.roots[:0]
	p.hashed = p.hashed[:0]
	clear(p.msgs)
	p.msgs = p.msgs[:0]
	for i := range p.jobs {
		p.jobs[i].sn, p.jobs[i].t, p.jobs[i].batch, p.jobs[i].err = nil, nil, nil, nil
		p.jobs[i].own.Reset()
	}
	p.jobs = p.jobs[:0]
	clear(p.order)
	p.order = p.order[:0]
	p.batch.Reset()
	p.number, p.head, p.err = number, Head{}, nil
}

// CommitAsync seals the block's changes and starts computing its state root
// on another goroutine, after applying the previous pipelined commit. The
// next block can execute on this State meanwhile: the accounts it changed stay
// cached until WaitCommit or the next CommitAsync applies the result. At most
// one commit is in flight, and store writes happen only on the caller's
// goroutine while none is.
func (s *State) CommitAsync(number uint64) error {
	p, err := s.sealBlock(number)
	if err != nil {
		return err
	}
	db := s.db
	db.pending = p
	db.commits.Go(func() { db.runCommit(p) })
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
	db.commits.Wait()
	db.pending = nil
	return s.applyCommit(p)
}

func (s *State) sealBlock(number uint64) (*pendingCommit, error) {
	s.FinalizeTx()
	db := s.db
	if _, err := s.WaitCommit(); err != nil && !errors.Is(err, errNothingPending) {
		return nil, err
	}
	if db.err != nil {
		return nil, db.err
	}
	if db.head.Loaded && number <= db.head.Number {
		return nil, ErrBlockOrder
	}
	if s.err != nil {
		db.err = s.err
		return nil, s.err
	}
	p := &db.commit
	p.reset(number)
	db.addrs = appendSortedAddresses(db.addrs[:0], s.blockDirty)
	p.snaps = slices.Grow(p.snaps, len(db.addrs))
	for _, addr := range db.addrs {
		o := s.accounts[addr]
		assert.Ok(o != nil, "state commits an account it never loaded")
		p.snaps = append(p.snaps, s.seal(o, p))
	}
	clear(s.blockDirty)
	return p, nil
}

func (s *State) applyCommit(p *pendingCommit) (Committed, error) {
	db := s.db
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
func (s *State) seal(o *object, p *pendingCommit) objectSnap {
	sn := objectSnap{
		o: o, addr: o.addr, exists: o.exists, baseExists: o.base.exists, baseRoot: o.base.storageRoot,
		wiped: o.wiped, codeDirty: o.codeDirty, nonce: o.nonce, balance: o.balance, codeHash: o.codeHash, code: o.code,
	}
	assert.Ok(sn.baseRoot != pendingRoot, "sealed account has an unsettled storage root")
	if len(o.changed) > 0 && o.exists {
		s.db.slotKeys = appendSortedHashes(s.db.slotKeys[:0], o.changed)
		start := len(p.slots)
		for _, k := range s.db.slotKeys {
			p.slots = append(p.slots, slotValue{key: k, v: o.committed[k]})
		}
		sn.slots = p.slots[start:len(p.slots):len(p.slots)]
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
	defer func() {
		if r := recover(); r != nil {
			p.err = fmt.Errorf("state commit %d: %v", p.number, r)
		}
	}()
	p.err = db.computeCommit(p)
}

func (db *DB) computeCommit(p *pendingCommit) error {
	b := &p.batch
	slots := 0
	for i := range p.snaps {
		sn := &p.snaps[i]
		if sn.wiped {
			db.forgetStorageTrie(sn.addr)
			if err := db.wipeSlots(sn, b); err != nil {
				return err
			}
		}
		if !sn.exists || len(sn.slots) == 0 {
			continue
		}
		t := db.storageTrie(sn.addr, snapBase(sn))
		p.jobs = growJobs(p.jobs)
		j := &p.jobs[len(p.jobs)-1]
		j.sn, j.t, j.before = sn, t, t.Nodes()
		slots += len(sn.slots)
	}
	for i := range p.jobs {
		p.jobs[i].sn.job = &p.jobs[i]
	}
	if err := db.runStorageJobs(p, slots); err != nil {
		return err
	}
	db.hashAddresses(p)
	p.roots = slices.Grow(p.roots, len(p.snaps))[:len(p.snaps)]
	for i := range p.snaps {
		root, err := db.finishSnap(&p.snaps[i], p.hashed[i], p)
		if err != nil {
			return err
		}
		p.roots[i] = root
	}
	db.accountWriter.batch, db.accountWriter.keys = b, &p.keys
	root := db.accounts.Commit(&db.accountWriter)
	p.head = Head{Number: p.number, Root: root, Loaded: true}
	b.PutOwned(headKey, encodeHead(p.head))
	db.trimTries()
	return nil
}

func growJobs(jobs []storageJob) []storageJob {
	if len(jobs) < cap(jobs) {
		return jobs[:len(jobs)+1]
	}
	return append(jobs, storageJob{})
}

func (db *DB) hashAddresses(p *pendingCommit) {
	p.hashed = slices.Grow(p.hashed, len(p.snaps))[:len(p.snaps)]
	if len(db.addrHashes) >= keyHashesMax {
		clear(db.addrHashes)
	}
	p.msgs = p.msgs[:0]
	p.missed = p.missed[:0]
	for i := range p.snaps {
		h, ok := db.addrHashes[p.snaps[i].addr]
		if ok {
			p.hashed[i] = h
			continue
		}
		p.msgs = append(p.msgs, p.snaps[i].addr[:])
		p.missed = append(p.missed, i)
	}
	if len(p.msgs) == 0 {
		return
	}
	p.out = slices.Grow(p.out, len(p.msgs))[:len(p.msgs)]
	db.keyHasher.Hash(p.msgs, p.out)
	for k, i := range p.missed {
		p.hashed[i] = p.out[k]
		db.addrHashes[p.snaps[i].addr] = p.out[k]
	}
	clear(p.msgs)
	p.msgs = p.msgs[:0]
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

// storageJob is one account's storage trie update. On one goroutine it writes
// into the block's batch; on a worker into its own, which finishSnap appends
// in address order.
type storageJob struct {
	sn     *objectSnap
	t      *trie.Trie
	before int
	root   Hash
	batch  *storage.Batch
	own    storage.Batch
	err    error
}

// parallelSlotsMin is the number of changed slots in a block below which
// storage tries are updated on one goroutine.
const parallelSlotsMin = 64

const storageWorkersMax = 64

// keyHashesMax bounds each cache of hashed addresses and slot keys; a full
// cache is cleared.
const keyHashesMax = 1 << 14

const _ = uint(keyHashesMax - parallelSlotsMin)

const _ = uint(storageWorkersMax - keccak.BatchLanes)

const _ = uint(parallelSlotsMin - keccak.BatchLanes)

// runStorageJobs updates every job's storage trie and computes its root.
// Each account's trie, slots and batch are its own, so jobs run in parallel;
// the largest start first.
func (db *DB) runStorageJobs(p *pendingCommit, slots int) error {
	jobs := p.jobs
	workers := min(runtime.GOMAXPROCS(0), len(jobs), storageWorkersMax)
	if workers <= 1 || slots < parallelSlotsMin {
		w := db.worker(0)
		for i := range jobs {
			j := &jobs[i]
			j.batch = &p.batch
			w.run(j, &p.keys, db.opts.StorageNodes)
			if j.err != nil {
				return j.err
			}
		}
		return nil
	}
	for i := range jobs {
		jobs[i].batch = &jobs[i].own
		p.order = append(p.order, &jobs[i])
	}
	order := p.order
	slices.SortStableFunc(order, func(a, b *storageJob) int { return len(b.sn.slots) - len(a.sn.slots) })
	var next atomic.Int64
	var wg sync.WaitGroup
	for k := range workers {
		w := db.worker(k)
		wg.Go(func() {
			for range len(order) {
				i := int(next.Add(1) - 1)
				if i >= len(order) {
					return
				}
				w.run(order[i], &w.keys, db.opts.StorageNodes)
			}
		})
	}
	wg.Wait()
	for _, j := range order {
		if j.err != nil {
			return j.err
		}
	}
	return nil
}

func (db *DB) worker(k int) *storageWorker {
	assert.Ok(k < storageWorkersMax, "storage worker index under storageWorkersMax")
	for i := len(db.workers); i <= k; i++ {
		db.workers = append(db.workers, newStorageWorker())
	}
	return db.workers[k]
}

type storageWorker struct {
	hasher     *keccak.Batch
	enc        *rlp.Encoder
	msgs       [][]byte
	hashed     [keccak.BatchLanes]Hash
	out        [keccak.BatchLanes]Hash
	missed     [keccak.BatchLanes]int
	slotHashes map[Hash]Hash
	writer     nodeWriter
	keys       keyArena
}

func newStorageWorker() *storageWorker {
	return &storageWorker{
		hasher: keccak.NewBatch(), enc: rlp.NewEncoder(64), msgs: make([][]byte, 0, keccak.BatchLanes),
		slotHashes: make(map[Hash]Hash, keyHashesMax),
	}
}

func (w *storageWorker) run(j *storageJob, keys *keyArena, nodesMax int) {
	sn := j.sn
	if len(w.slotHashes) >= keyHashesMax {
		clear(w.slotHashes)
	}
	for start := 0; start < len(sn.slots); start += keccak.BatchLanes {
		end := min(start+keccak.BatchLanes, len(sn.slots))
		w.hashGroup(sn.slots[start:end])
		for i := start; i < end; i++ {
			if j.err = w.update(j, keys, &sn.slots[i], &w.hashed[i-start]); j.err != nil {
				return
			}
		}
	}
	w.writer.reset(j.batch, keys, prefixStorageNode, sn.addr[:])
	j.root = j.t.Commit(&w.writer)
	if j.t.Nodes() > nodesMax {
		j.t.Evict(storageKeepDepthDefault)
	}
}

// hashGroup sets w.hashed to the hashes of up to BatchLanes slot keys, from
// the worker's cache where it has them and in one batch for the rest.
func (w *storageWorker) hashGroup(slots []slotValue) {
	assert.Ok(len(slots) <= keccak.BatchLanes, "slot key group over BatchLanes")
	w.msgs = w.msgs[:0]
	misses := 0
	for i := range slots {
		h, ok := w.slotHashes[slots[i].key]
		if ok {
			w.hashed[i] = h
			continue
		}
		w.msgs = append(w.msgs, slots[i].key[:])
		w.missed[misses] = i
		misses++
	}
	if misses == 0 {
		return
	}
	w.hasher.Hash(w.msgs, w.out[:misses])
	for k := range misses {
		i := w.missed[k]
		w.hashed[i] = w.out[k]
		w.slotHashes[slots[i].key] = w.out[k]
	}
	clear(w.msgs)
	w.msgs = w.msgs[:0]
}

func (w *storageWorker) update(j *storageJob, keys *keyArena, sv *slotValue, hk *Hash) error {
	sk := keys.slotKey(j.sn.addr, sv.key)
	if sv.v.IsZero() {
		j.batch.Delete(sk)
		return j.t.Unset(hk[:])
	}
	enc := ownedSlot(sv.v)
	j.batch.PutOwned(sk, enc)
	w.enc.Reset()
	w.enc.WriteBytes(enc)
	return j.t.Set(hk[:], w.enc.Encoded())
}

func (db *DB) finishSnap(sn *objectSnap, hashed Hash, p *pendingCommit) (Hash, error) {
	b := &p.batch
	if !sn.exists {
		if !sn.baseExists {
			return emptyRoot, nil
		}
		b.Delete(p.keys.accountKey(sn.addr))
		return emptyRoot, db.accounts.Unset(hashed[:])
	}
	root := snapBase(sn)
	if j := sn.job; j != nil {
		if j.batch != b {
			b.Append(j.batch)
			j.batch.Reset()
		}
		db.storageNodes += j.t.Nodes() - j.before
		root = j.root
	}
	if sn.codeDirty && sn.codeHash != emptyCodeHash {
		b.PutOwned(p.keys.codeKey(sn.codeHash), sn.code)
	}
	fa := flatAccount{nonce: sn.nonce, balance: sn.balance, codeHash: sn.codeHash, storageRoot: root}
	b.PutOwned(p.keys.accountKey(sn.addr), fa.encode())
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
	return appendSortedAddresses(make([]Address, 0, len(m)), m)
}

func appendSortedAddresses[V any](keys []Address, m map[Address]V) []Address {
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Address) int { return bytes.Compare(a[:], b[:]) })
	return keys
}

func sortedHashes[V any](m map[Hash]V) []Hash {
	return appendSortedHashes(make([]Hash, 0, len(m)), m)
}

func appendSortedHashes[V any](keys []Hash, m map[Hash]V) []Hash {
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Hash) int { return bytes.Compare(a[:], b[:]) })
	return keys
}

func ownedSlot(v u256.Int) []byte {
	b := v.Bytes32()
	trimmed := primitives.TrimLeftZeros(b[:])
	out := make([]byte, len(trimmed))
	copy(out, trimmed)
	return out
}
