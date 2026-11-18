package state

import (
	"errors"

	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type AccountValue struct {
	Exists   bool
	Nonce    uint64
	Balance  u256.Int
	CodeHash Hash
}

func valueOf(o *object) AccountValue {
	if !o.exists {
		return AccountValue{}
	}
	return AccountValue{Exists: true, Nonce: o.nonce, Balance: o.balance, CodeHash: o.codeHash}
}

type view struct {
	s *State
}

type parentAccount struct {
	value   AccountValue
	obj     *object
	noSlots bool
	err     error
}

func (v view) account(addr Address) parentAccount {
	if o, ok := v.s.accounts[addr]; ok {
		return parentAccount{value: valueOf(o), obj: o}
	}
	raw, err := v.s.db.store.Get(accountKey(addr))
	if errors.Is(err, storage.ErrNotFound) {
		return parentAccount{noSlots: true}
	}
	if err != nil {
		return parentAccount{noSlots: true, err: err}
	}
	fa, err := decodeAccount(raw)
	if err != nil {
		return parentAccount{noSlots: true, err: err}
	}
	val := AccountValue{Exists: true, Nonce: fa.nonce, Balance: fa.balance, CodeHash: fa.codeHash}
	return parentAccount{value: val, noSlots: fa.storageRoot == emptyRoot}
}

func (v view) slot(addr Address, key Hash) (u256.Int, error) {
	o, ok := v.s.accounts[addr]
	if !ok {
		return v.s.db.readSlot(addr, key)
	}
	if val, ok := o.committed[key]; ok {
		return val, nil
	}
	if o.wiped || o.pendingWipe || !o.base.exists || o.base.storageRoot == emptyRoot {
		return u256.Zero(), nil
	}
	return v.s.db.readSlot(addr, key)
}

func (v view) code(addr Address, h Hash) ([]byte, error) {
	if o, ok := v.s.accounts[addr]; ok && o.codeLoaded && o.codeHash == h {
		return o.code, nil
	}
	if c, ok := v.s.db.codes[h]; ok {
		return c, nil
	}
	return v.s.db.store.Get(codeKey(h))
}

type specAccount struct {
	addr      Address
	value     AccountValue
	inherited bool
}

type specSlot struct {
	key       slotKey
	r0        u256.Int
	low       u256.Int
	high      u256.Int
	cur       u256.Int
	inherited bool
	affine    bool
	shifted   bool
}

type specSlotWrite struct {
	key  Hash
	v    u256.Int
	read int
}

type specWrite struct {
	addr      Address
	replaced  bool
	exists    bool
	nonce     uint64
	balance   u256.Int
	codeHash  Hash
	code      []byte
	codeSet   bool
	codeDirty bool
	slots     []specSlotWrite
}

type SpecTx struct {
	accounts []specAccount
	slots    []specSlot
	writes   []specWrite
	unsafe   bool
	rebased  bool
}

func (t *SpecTx) Unsafe() bool { return t.unsafe }

func (t *SpecTx) Rebased() bool { return t.rebased }

type specMode struct {
	parent     view
	tx         *SpecTx
	accIdx     map[Address]int
	first      map[Address]*object
	slotIdx    map[slotKey]int
	rangeAccts map[Address]struct{}
	rangeSlots map[slotKey]struct{}
	rangeWiped map[Address]struct{}
	win        [affineSlotsMax]slotKey
	winN       int
	inWin      bool
	excl       map[slotKey]struct{}
	rangeExcl  map[slotKey]struct{}
	exclAddr   Address
	inExcl     bool
}

const affineSlotsMax = 16

type Slot struct {
	Address Address
	Key     Hash
}

func (s *State) NewSpeculator() *State {
	assert.Ok(s.spec == nil, "a speculator comes from the real state")
	w := newState(s.db)
	w.spec = &specMode{
		parent:     view{s: s},
		accIdx:     make(map[Address]int),
		first:      make(map[Address]*object),
		slotIdx:    make(map[slotKey]int),
		rangeAccts: make(map[Address]struct{}),
		rangeSlots: make(map[slotKey]struct{}),
		rangeWiped: make(map[Address]struct{}),
		excl:       make(map[slotKey]struct{}),
		rangeExcl:  make(map[slotKey]struct{}),
	}
	return w
}

func (s *State) ResetSpeculation() {
	m := s.spec
	assert.Ok(m != nil, "only a speculator resets its speculation")
	assert.Ok(m.tx == nil, "speculation resets between transactions")
	assert.Ok(len(m.parent.s.journal) == 0, "speculation reads the real state between transactions")
	s.resetTx()
	clear(s.accounts)
	clear(s.blockDirty)
	clear(m.rangeAccts)
	clear(m.rangeSlots)
	clear(m.rangeWiped)
	clear(m.rangeExcl)
	s.cachedSlots = 0
	s.err = nil
}

func (s *State) BeginSpecTx() {
	m := s.spec
	assert.Ok(m != nil, "only a speculator begins a speculative transaction")
	assert.Ok(m.tx == nil, "speculative transactions do not nest")
	assert.Ok(len(s.journal) == 0, "a speculative transaction starts with an empty journal")
	m.tx = &SpecTx{}
	clear(m.accIdx)
	clear(m.first)
	clear(m.slotIdx)
	clear(m.excl)
	m.inWin = false
	m.inExcl = false
}

func (s *State) EndSpecTx() *SpecTx {
	m := s.spec
	assert.Ok(m != nil, "only a speculator ends a speculative transaction")
	assert.Ok(m.tx != nil, "a speculative transaction is open")
	t := m.tx
	if s.err != nil || len(s.journal) != 0 || m.inWin || m.inExcl {
		t.unsafe = true
	}
	m.tx = nil
	return t
}

func (m *specMode) parentObject(s *State, addr Address) *object {
	a := m.parent.account(addr)
	if a.err != nil {
		s.fail(a.err)
	}
	o := newObject(addr, base{storageRoot: emptyRoot})
	if a.value.Exists {
		o.exists = true
		o.nonce = a.value.Nonce
		o.balance = a.value.Balance
		o.codeHash = a.value.CodeHash
	}
	if a.obj != nil || !a.noSlots {
		o.base = base{exists: true, storageRoot: pendingRoot}
	}
	return o
}

func (m *specMode) noteAccount(o *object) {
	assert.Ok(m.tx != nil, "a speculator reads inside a transaction")
	if _, ok := m.accIdx[o.addr]; ok {
		return
	}
	m.accIdx[o.addr] = len(m.tx.accounts)
	m.first[o.addr] = o
	_, inherited := m.rangeAccts[o.addr]
	m.tx.accounts = append(m.tx.accounts, specAccount{addr: o.addr, value: valueOf(o), inherited: inherited})
}

func (m *specMode) access(s *State, k slotKey, v u256.Int) {
	if m.excluded(k) {
		return
	}
	i, ok := m.slotIdx[k]
	if !ok {
		o0 := m.first[k.addr]
		assert.Ok(o0 != nil, "a slot is read after its account")
		r0 := s.committedValue(o0, k.key)
		_, inherited := m.rangeSlots[k]
		if _, wiped := m.rangeWiped[k.addr]; wiped {
			inherited = true
		}
		i = len(m.tx.slots)
		m.slotIdx[k] = i
		m.tx.slots = append(m.tx.slots, specSlot{key: k, r0: r0, low: r0, high: r0, inherited: inherited, affine: !r0.IsZero()})
	}
	sl := &m.tx.slots[i]
	if !m.inWindow(k) {
		sl.affine = false
		return
	}
	observe(sl, v)
}

// excluded reports whether an access belongs to the exclude window, which
// the executor replays semantically at commit instead of validating.
func (m *specMode) excluded(k slotKey) bool {
	_, inTx := m.excl[k]
	_, inRange := m.rangeExcl[k]
	if m.inExcl && k.addr == m.exclAddr {
		if _, read := m.slotIdx[k]; read {
			m.tx.unsafe = true
		}
		m.excl[k] = struct{}{}
		return true
	}
	if inTx || inRange {
		m.tx.unsafe = true
		return true
	}
	return false
}

func (m *specMode) inWindow(k slotKey) bool {
	if !m.inWin {
		return false
	}
	for i := 0; i < m.winN; i++ {
		if m.win[i] == k {
			return true
		}
	}
	return false
}

func observe(sl *specSlot, v u256.Int) {
	if v.IsZero() {
		sl.affine = false
		return
	}
	if v.Lt(sl.low) {
		sl.low = v
	}
	if v.Gt(sl.high) {
		sl.high = v
	}
}

func (s *State) BeginAffine(slots ...Slot) {
	m := s.spec
	if m == nil || m.tx == nil {
		return
	}
	assert.Ok(!m.inWin, "affine windows do not nest")
	assert.Ok(len(slots) <= affineSlotsMax, "an affine window holds few slots")
	m.winN = 0
	for _, sl := range slots {
		m.win[m.winN] = slotKey{addr: sl.Address, key: sl.Key}
		m.winN++
	}
	m.inWin = true
}

func (s *State) EndAffine() {
	if m := s.spec; m != nil {
		m.inWin = false
	}
}

// AffineFloor records that a transaction required the slot to hold at
// least floor: a rebase must keep it above.
func (s *State) AffineFloor(addr Address, key Hash, floor u256.Int) {
	m := s.spec
	if m == nil || m.tx == nil {
		return
	}
	k := slotKey{addr: addr, key: key}
	i, ok := m.slotIdx[k]
	if !ok {
		return
	}
	sl := &m.tx.slots[i]
	cur := s.storage(addr, key)
	if cur.Lt(floor) {
		sl.affine = false
		return
	}
	observe(sl, cur.Sub(floor))
}

// ClearAffine makes every read of the open speculative transaction exact.
func (s *State) ClearAffine() {
	m := s.spec
	assert.Ok(m != nil && m.tx != nil, "affine reads are cleared inside a speculative transaction")
	for i := range m.tx.slots {
		m.tx.slots[i].affine = false
	}
}

// BeginExclude starts a window whose storage accesses to addr are left out
// of the speculative transaction: the caller replays them at commit. Any
// access to those slots outside the window makes the transaction unsafe.
func (s *State) BeginExclude(addr Address) {
	m := s.spec
	assert.Ok(m != nil && m.tx != nil, "an exclude window opens inside a speculative transaction")
	assert.Ok(!m.inExcl, "exclude windows do not nest")
	m.exclAddr = addr
	m.inExcl = true
}

func (s *State) EndExclude() {
	m := s.spec
	assert.Ok(m != nil && m.inExcl, "an exclude window is open")
	m.inExcl = false
}

// Peek reads a slot the way Storage does, without recording the read.
func (s *State) Peek(addr Address, key Hash) u256.Int {
	return s.storage(addr, key)
}

func (m *specMode) capture(s *State) {
	for addr := range s.blockDirty {
		o := s.accounts[addr]
		first := m.first[addr]
		assert.Ok(o != nil && first != nil, "a speculative write follows a read of its account")
		w := specWrite{
			addr:     addr,
			replaced: o != first,
			exists:   o.exists,
			nonce:    o.nonce,
			balance:  o.balance,
			codeHash: o.codeHash,
		}
		// first is the object the transaction loaded, mutated since: its
		// value when loaded is the one recorded for validation.
		was := m.tx.accounts[m.accIdx[addr]].value
		if w.replaced || o.codeHash != was.CodeHash {
			w.codeSet = true
			w.code = o.code
			w.codeDirty = o.codeDirty
		}
		if len(o.dirty) > 0 {
			w.slots = make([]specSlotWrite, 0, len(o.dirty))
		}
		for k, v := range o.dirty {
			sk := slotKey{addr: addr, key: k}
			if _, ex := m.excl[sk]; ex {
				m.rangeExcl[sk] = struct{}{}
				continue
			}
			i, ok := m.slotIdx[sk]
			if !ok {
				i = -1
			}
			if ok && w.replaced {
				m.tx.slots[i].affine = false
			}
			w.slots = append(w.slots, specSlotWrite{key: k, v: v, read: i})
			m.rangeSlots[sk] = struct{}{}
		}
		m.rangeAccts[addr] = struct{}{}
		if w.replaced {
			m.rangeWiped[addr] = struct{}{}
		}
		m.tx.writes = append(m.tx.writes, w)
	}
	clear(s.blockDirty)
}
