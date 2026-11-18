package state

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/u256"
)

type Address = primitives.Address

type Hash = primitives.Hash

var emptyCodeHash = keccak.Sum256(nil)

var errStop = errors.New("state: stop iteration")

type slotKey struct {
	addr Address
	key  Hash
}

type base struct {
	exists      bool
	storageRoot Hash
}

type object struct {
	addr       Address
	exists     bool
	nonce      uint64
	balance    u256.Int
	codeHash   Hash
	code       []byte
	codeLoaded bool
	codeDirty  bool
	base       base
	wiped      bool
	inTx       bool
	// pendingWipe: the account was destructed in a block whose pipelined
	// commit has not deleted its stored slots yet, so they read as zero.
	pendingWipe bool
	committed   map[Hash]u256.Int
	dirty       map[Hash]u256.Int
	changed     map[Hash]struct{}
}

func newObject(addr Address, b base) *object {
	return &object{addr: addr, codeHash: emptyCodeHash, base: b}
}

func (o *object) setCommitted(key Hash, v u256.Int) {
	if o.committed == nil {
		o.committed = make(map[Hash]u256.Int)
	}
	o.committed[key] = v
}

func (o *object) setChanged(key Hash, v u256.Int) {
	o.setCommitted(key, v)
	if o.changed == nil {
		o.changed = make(map[Hash]struct{})
	}
	o.changed[key] = struct{}{}
}

func (o *object) foldDirty() int {
	if len(o.dirty) == 0 {
		return 0
	}
	if o.committed == nil {
		o.committed = make(map[Hash]u256.Int, len(o.dirty))
	}
	if o.changed == nil {
		o.changed = make(map[Hash]struct{}, len(o.dirty))
	}
	before := len(o.committed)
	for k, v := range o.dirty {
		o.committed[k] = v
		o.changed[k] = struct{}{}
	}
	clear(o.dirty)
	return len(o.committed) - before
}

func (o *object) setDirty(key Hash, v u256.Int) {
	if o.dirty == nil {
		o.dirty = make(map[Hash]u256.Int)
	}
	o.dirty[key] = v
}

func (o *object) empty() bool {
	if o.nonce != 0 {
		return false
	}
	return o.balance.IsZero() && o.codeHash == emptyCodeHash
}

type Account struct {
	Nonce       uint64
	Balance     u256.Int
	CodeHash    Hash
	StorageRoot Hash
}

type State struct {
	db            *DB
	accounts      map[Address]*object
	blockDirty    map[Address]struct{}
	txObjs        []*object
	transient     map[slotKey]u256.Int
	warmAddrs     map[Address]struct{}
	warmSlots     map[slotKey]struct{}
	created       map[Address]struct{}
	touched       []Address
	touchedSet    map[Address]struct{}
	destructed    []Address
	destructedSet map[Address]struct{}
	journal       []entry
	logs          []evm.Log
	refund        int64
	cachedSlots   int
	err           error
	spec          *specMode
	track         *parTrack
	trackBuf      *parTrack
}

func newState(db *DB) *State {
	return &State{
		db:            db,
		accounts:      make(map[Address]*object),
		blockDirty:    make(map[Address]struct{}),
		transient:     make(map[slotKey]u256.Int),
		warmAddrs:     make(map[Address]struct{}),
		warmSlots:     make(map[slotKey]struct{}),
		created:       make(map[Address]struct{}),
		touchedSet:    make(map[Address]struct{}),
		destructedSet: make(map[Address]struct{}),
	}
}

func (s *State) Err() error { return s.err }

func (s *State) fail(err error) {
	if s.err == nil {
		s.err = err
	}
}

func (s *State) load(addr Address) *object {
	o, ok := s.accounts[addr]
	if !ok {
		o = s.loadMissing(addr)
		s.accounts[addr] = o
	}
	if s.spec != nil {
		s.spec.noteAccount(o)
	}
	return o
}

func (s *State) loadMissing(addr Address) *object {
	if s.spec != nil {
		return s.spec.parentObject(s, addr)
	}
	o := newObject(addr, base{storageRoot: emptyRoot})
	raw, err := s.db.store.Get(accountKey(addr))
	switch {
	case err == nil:
		fa, derr := decodeAccount(raw)
		if derr != nil {
			s.fail(derr)
			break
		}
		o.exists = true
		o.nonce = fa.nonce
		o.balance = fa.balance
		o.codeHash = fa.codeHash
		o.base = base{exists: true, storageRoot: fa.storageRoot}
	case errors.Is(err, storage.ErrNotFound):
	default:
		s.fail(err)
	}
	return o
}

func (s *State) Account(addr Address) (Account, bool) {
	if err := s.db.settlePending(); err != nil {
		s.fail(err)
	}
	o := s.load(addr)
	if !o.exists {
		return Account{}, false
	}
	return Account{Nonce: o.nonce, Balance: o.balance, CodeHash: o.codeHash, StorageRoot: o.base.storageRoot}, true
}

func (s *State) Logs() []evm.Log { return s.logs }

func (s *State) account(addr Address) *object {
	o := s.load(addr)
	s.markDirty(addr)
	if o.exists {
		return o
	}
	o.exists = true
	o.codeHash = emptyCodeHash
	s.record(entry{kind: entryExist, obj: o})
	s.touch(addr)
	return o
}

func (s *State) touch(addr Address) {
	if _, ok := s.touchedSet[addr]; ok {
		return
	}
	s.touchedSet[addr] = struct{}{}
	s.touched = append(s.touched, addr)
	s.record(entry{kind: entryTouch, slot: slotKey{addr: addr}})
}

func (s *State) Exist(addr Address) bool {
	return s.load(addr).exists
}

func (s *State) Empty(addr Address) bool {
	o := s.load(addr)
	return !o.exists || o.empty()
}

func (s *State) CreateAccount(addr Address) {
	old := s.load(addr)
	o := newObject(addr, old.base)
	o.exists = true
	o.wiped = true
	o.codeLoaded = true
	if old.exists {
		o.balance = old.balance
	}
	s.accounts[addr] = o
	s.markWiped(addr)
	s.record(entry{kind: entryReplace, slot: slotKey{addr: addr}, prev: old})
	s.touch(addr)
	if _, ok := s.created[addr]; !ok {
		s.created[addr] = struct{}{}
		s.record(entry{kind: entryCreated, slot: slotKey{addr: addr}})
	}
}

func (s *State) CreatedInTx(addr Address) bool {
	_, ok := s.created[addr]
	return ok
}

func (s *State) Touch(addr Address) {
	s.touch(addr)
}

func (s *State) Balance(addr Address) u256.Int {
	o := s.load(addr)
	if !o.exists {
		return u256.Zero()
	}
	return o.balance
}

func (s *State) setBalance(addr Address, value u256.Int) {
	o := s.account(addr)
	s.record(entry{kind: entryBalance, obj: o, value: o.balance})
	o.balance = value
	s.touch(addr)
}

func (s *State) AddBalance(addr Address, amount u256.Int) {
	s.setBalance(addr, s.Balance(addr).Add(amount))
}

func (s *State) SubBalance(addr Address, amount u256.Int) {
	s.setBalance(addr, s.Balance(addr).Sub(amount))
}

func (s *State) Nonce(addr Address) uint64 {
	o := s.load(addr)
	if !o.exists {
		return 0
	}
	return o.nonce
}

func (s *State) SetNonce(addr Address, nonce uint64) {
	o := s.account(addr)
	s.record(entry{kind: entryNonce, obj: o, nonce: o.nonce})
	o.nonce = nonce
	s.touch(addr)
}

func (s *State) Code(addr Address) []byte {
	o := s.load(addr)
	if !o.exists {
		return nil
	}
	if o.codeLoaded {
		return o.code
	}
	o.codeLoaded = true
	if o.codeHash == emptyCodeHash {
		return nil
	}
	code, err := s.codeOf(o)
	if err != nil {
		s.fail(err)
		return nil
	}
	o.code = code
	return o.code
}

func (s *State) CodeHash(addr Address) Hash {
	o := s.load(addr)
	if !o.exists {
		return Hash{}
	}
	return o.codeHash
}

func (s *State) CodeSize(addr Address) int {
	return len(s.Code(addr))
}

func (s *State) SetCode(addr Address, code []byte) {
	o := s.account(addr)
	s.Code(addr)
	s.record(entry{kind: entryCode, obj: o, code: o.code, codeHash: o.codeHash})
	o.code = append([]byte(nil), code...)
	o.codeHash = keccak.Sum256(code)
	o.codeLoaded = true
	o.codeDirty = true
	s.touch(addr)
}

func (s *State) committedValue(o *object, key Hash) u256.Int {
	if v, ok := o.committed[key]; ok {
		return v
	}
	if o.wiped || o.pendingWipe {
		return u256.Zero()
	}
	if !o.base.exists {
		return u256.Zero()
	}
	if o.base.storageRoot == emptyRoot {
		return u256.Zero()
	}
	v, err := s.readSlot(o.addr, key)
	if err != nil {
		s.fail(err)
		return u256.Zero()
	}
	o.setCommitted(key, v)
	s.cachedSlots++
	return v
}

func (s *State) codeOf(o *object) ([]byte, error) {
	if s.spec != nil {
		return s.spec.parent.code(o.addr, o.codeHash)
	}
	return s.db.code(o.codeHash)
}

func (s *State) readSlot(addr Address, key Hash) (u256.Int, error) {
	if s.spec != nil {
		return s.spec.parent.slot(addr, key)
	}
	return s.db.readSlot(addr, key)
}

func (s *State) Storage(addr Address, key Hash) u256.Int {
	v := s.storage(addr, key)
	if s.spec != nil {
		s.spec.access(s, slotKey{addr: addr, key: key}, v)
	}
	return v
}

func (s *State) storage(addr Address, key Hash) u256.Int {
	o := s.load(addr)
	if !o.exists {
		return u256.Zero()
	}
	if v, ok := o.dirty[key]; ok {
		return v
	}
	return s.committedValue(o, key)
}

func (s *State) OriginalStorage(addr Address, key Hash) u256.Int {
	o := s.load(addr)
	v := u256.Zero()
	if o.exists {
		v = s.committedValue(o, key)
	}
	if s.spec != nil {
		s.spec.access(s, slotKey{addr: addr, key: key}, v)
	}
	return v
}

func (s *State) SetStorage(addr Address, key Hash, value u256.Int) {
	o := s.account(addr)
	if s.spec != nil {
		s.spec.access(s, slotKey{addr: addr, key: key}, value)
	}
	s.markSlot(slotKey{addr: addr, key: key})
	prev, had := o.dirty[key]
	o.setDirty(key, value)
	s.record(entry{kind: entryStorage, obj: o, slot: slotKey{addr: addr, key: key}, value: prev, had: had})
	if !o.inTx {
		o.inTx = true
		s.txObjs = append(s.txObjs, o)
	}
}

func (s *State) HasStorage(addr Address) bool {
	if s.spec != nil {
		s.spec.tx.unsafe = true
		return true
	}
	o := s.load(addr)
	if !o.exists {
		return false
	}
	for _, v := range o.dirty {
		if v != (u256.Int{}) {
			return true
		}
	}
	for _, k := range sortedHashes(o.committed) {
		if !s.storage(addr, k).IsZero() {
			return true
		}
	}
	if o.wiped || o.pendingWipe {
		return false
	}
	if !o.base.exists {
		return false
	}
	if o.base.storageRoot == emptyRoot {
		return false
	}
	return s.db.anyStoredSlot(s, o)
}

func (s *State) TransientStorage(addr Address, key Hash) u256.Int {
	return s.transient[slotKey{addr: addr, key: key}]
}

func (s *State) SetTransientStorage(addr Address, key Hash, value u256.Int) {
	k := slotKey{addr: addr, key: key}
	s.record(entry{kind: entryTransient, slot: k, value: s.transient[k]})
	s.transient[k] = value
}

func (s *State) SelfDestruct(addr Address) {
	if _, ok := s.destructedSet[addr]; !ok {
		s.destructedSet[addr] = struct{}{}
		s.destructed = append(s.destructed, addr)
		s.record(entry{kind: entryDestruct, slot: slotKey{addr: addr}})
	}
	o := s.load(addr)
	if !o.exists {
		return
	}
	s.markDirty(addr)
	s.record(entry{kind: entryBalance, obj: o, value: o.balance})
	o.balance = u256.Zero()
}

func (s *State) HasSelfDestructed(addr Address) bool {
	_, ok := s.destructedSet[addr]
	return ok
}

func (s *State) AddressWarm(addr Address) bool {
	_, ok := s.warmAddrs[addr]
	return ok
}

func (s *State) WarmAddress(addr Address) {
	if _, ok := s.warmAddrs[addr]; ok {
		return
	}
	s.warmAddrs[addr] = struct{}{}
	s.record(entry{kind: entryWarmAddr, slot: slotKey{addr: addr}})
}

func (s *State) SlotWarm(addr Address, key Hash) bool {
	_, ok := s.warmSlots[slotKey{addr: addr, key: key}]
	return ok
}

func (s *State) WarmSlot(addr Address, key Hash) {
	k := slotKey{addr: addr, key: key}
	if _, ok := s.warmSlots[k]; ok {
		return
	}
	s.warmSlots[k] = struct{}{}
	s.record(entry{kind: entryWarmSlot, slot: k})
}

func (s *State) AddRefund(delta int64) {
	s.refund += delta
	s.record(entry{kind: entryRefund, refund: delta})
}

func (s *State) Refund() int64 { return s.refund }

func (s *State) AddLog(log evm.Log) {
	s.logs = append(s.logs, log)
	s.record(entry{kind: entryLog})
}

func (s *State) FinalizeTx() {
	for _, addr := range s.destructed {
		s.deleteAccount(addr)
	}
	for _, addr := range s.touched {
		o := s.accounts[addr]
		if o == nil {
			continue
		}
		if o.exists && o.empty() {
			s.deleteAccount(addr)
		}
	}
	if s.spec != nil {
		s.spec.capture(s)
	}
	for _, o := range s.txObjs {
		o.inTx = false
		if s.accounts[o.addr] != o {
			continue
		}
		s.cachedSlots += o.foldDirty()
	}
	s.resetTx()
}

func (s *State) deleteAccount(addr Address) {
	old := s.load(addr)
	o := newObject(addr, old.base)
	o.wiped = true
	o.codeLoaded = true
	s.accounts[addr] = o
	s.markWiped(addr)
}

func (s *State) resetTx() {
	clear(s.txObjs)
	s.txObjs = s.txObjs[:0]
	clear(s.transient)
	clear(s.warmAddrs)
	clear(s.warmSlots)
	clear(s.created)
	clear(s.touchedSet)
	clear(s.destructedSet)
	s.touched = s.touched[:0]
	s.destructed = s.destructed[:0]
	clear(s.journal)
	s.journal = s.journal[:0]
	s.logs = nil
	s.refund = 0
}

func (s *State) Discard() {
	if err := s.db.settlePending(); err != nil {
		s.db.err = err
	}
	s.resetTx()
	clear(s.accounts)
	clear(s.blockDirty)
	s.cachedSlots = 0
}

var _ evm.State = (*State)(nil)
