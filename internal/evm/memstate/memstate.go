package memstate

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type Address = primitives.Address

type Hash = primitives.Hash

var emptyCodeHash = keccak.Sum256(nil)

type Account struct {
	Balance u256.Int
	Nonce   uint64
	Code    []byte
	Storage map[Hash]u256.Int
}

type account struct {
	balance  u256.Int
	nonce    uint64
	code     []byte
	codeHash Hash
	storage  map[Hash]u256.Int
	dirty    map[Hash]u256.Int
}

type slotKey struct {
	addr Address
	key  Hash
}

type State struct {
	accounts   map[Address]*account
	transient  map[slotKey]u256.Int
	warmAddrs  map[Address]struct{}
	warmSlots  map[slotKey]struct{}
	created    map[Address]struct{}
	destructed map[Address]struct{}
	touched    map[Address]struct{}
	journal    []func()
	logs       []evm.Log
	refund     int64
}

func New() *State {
	s := &State{accounts: make(map[Address]*account)}
	s.resetTx()
	return s
}

func (s *State) resetTx() {
	s.transient = make(map[slotKey]u256.Int)
	s.warmAddrs = make(map[Address]struct{})
	s.warmSlots = make(map[slotKey]struct{})
	s.created = make(map[Address]struct{})
	s.destructed = make(map[Address]struct{})
	s.touched = make(map[Address]struct{})
	s.journal = s.journal[:0]
	s.logs = nil
	s.refund = 0
}

func (s *State) Put(addr Address, a Account) {
	acc := &account{
		balance:  a.Balance,
		nonce:    a.Nonce,
		code:     a.Code,
		codeHash: keccak.Sum256(a.Code),
		storage:  make(map[Hash]u256.Int, len(a.Storage)),
		dirty:    make(map[Hash]u256.Int),
	}
	for _, k := range sortedSlots(a.Storage) {
		v := a.Storage[k]
		if !v.IsZero() {
			acc.storage[k] = v
		}
	}
	s.accounts[addr] = acc
}

func (s *State) Get(addr Address) (Account, bool) {
	acc, ok := s.accounts[addr]
	if !ok {
		return Account{}, false
	}
	out := Account{Balance: acc.balance, Nonce: acc.nonce, Code: acc.code, Storage: make(map[Hash]u256.Int)}
	for _, k := range sortedSlots(acc.storage) {
		out.Storage[k] = acc.storage[k]
	}
	for _, k := range sortedSlots(acc.dirty) {
		v := acc.dirty[k]
		if v.IsZero() {
			delete(out.Storage, k)
			continue
		}
		out.Storage[k] = v
	}
	return out, true
}

func (s *State) Addresses() []Address {
	return sortedAccounts(s.accounts)
}

func (s *State) Logs() []evm.Log { return s.logs }

func (s *State) FinalizeTx() {
	for _, addr := range sortedSet(s.destructed) {
		delete(s.accounts, addr)
	}
	for _, addr := range sortedSet(s.touched) {
		acc, ok := s.accounts[addr]
		if ok && isEmpty(acc) {
			delete(s.accounts, addr)
		}
	}
	for _, addr := range sortedAccounts(s.accounts) {
		acc := s.accounts[addr]
		for _, k := range sortedSlots(acc.dirty) {
			v := acc.dirty[k]
			if v.IsZero() {
				delete(acc.storage, k)
			} else {
				acc.storage[k] = v
			}
		}
		clear(acc.dirty)
	}
	s.resetTx()
}

func isEmpty(acc *account) bool {
	return acc.nonce == 0 && acc.balance.IsZero() && len(acc.code) == 0
}

func (s *State) record(undo func()) {
	s.journal = append(s.journal, undo)
}

func (s *State) Snapshot() int { return len(s.journal) }

func (s *State) RevertToSnapshot(id int) {
	assert.Ok(id >= 0, "snapshot id is not negative")
	assert.Ok(id <= len(s.journal), "snapshot id is within the journal")
	for i := len(s.journal) - 1; i >= id; i-- {
		s.journal[i]()
	}
	s.journal = s.journal[:id]
}

func (s *State) account(addr Address) *account {
	acc, ok := s.accounts[addr]
	if ok {
		return acc
	}
	acc = &account{codeHash: emptyCodeHash, storage: make(map[Hash]u256.Int), dirty: make(map[Hash]u256.Int)}
	s.accounts[addr] = acc
	s.record(func() { delete(s.accounts, addr) })
	s.touch(addr)
	return acc
}

func (s *State) touch(addr Address) {
	_, ok := s.touched[addr]
	if ok {
		return
	}
	s.touched[addr] = struct{}{}
	s.record(func() { delete(s.touched, addr) })
}

func (s *State) Exist(addr Address) bool {
	_, ok := s.accounts[addr]
	return ok
}

func (s *State) Empty(addr Address) bool {
	acc, ok := s.accounts[addr]
	return !ok || isEmpty(acc)
}

func (s *State) CreateAccount(addr Address) {
	old, existed := s.accounts[addr]
	acc := &account{codeHash: emptyCodeHash, storage: make(map[Hash]u256.Int), dirty: make(map[Hash]u256.Int)}
	if existed {
		acc.balance = old.balance
	}
	s.accounts[addr] = acc
	s.record(func() {
		if existed {
			s.accounts[addr] = old
			return
		}
		delete(s.accounts, addr)
	})
	s.touch(addr)
	_, wasCreated := s.created[addr]
	if !wasCreated {
		s.created[addr] = struct{}{}
		s.record(func() { delete(s.created, addr) })
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
	acc, ok := s.accounts[addr]
	if !ok {
		return u256.Zero()
	}
	return acc.balance
}

func (s *State) setBalance(addr Address, value u256.Int) {
	acc := s.account(addr)
	prev := acc.balance
	acc.balance = value
	s.record(func() { acc.balance = prev })
	s.touch(addr)
}

func (s *State) AddBalance(addr Address, amount u256.Int) {
	s.setBalance(addr, s.Balance(addr).Add(amount))
}

func (s *State) SubBalance(addr Address, amount u256.Int) {
	s.setBalance(addr, s.Balance(addr).Sub(amount))
}

func (s *State) Nonce(addr Address) uint64 {
	acc, ok := s.accounts[addr]
	if !ok {
		return 0
	}
	return acc.nonce
}

func (s *State) SetNonce(addr Address, nonce uint64) {
	acc := s.account(addr)
	prev := acc.nonce
	acc.nonce = nonce
	s.record(func() { acc.nonce = prev })
	s.touch(addr)
}

func (s *State) Code(addr Address) []byte {
	acc, ok := s.accounts[addr]
	if !ok {
		return nil
	}
	return acc.code
}

func (s *State) CodeHash(addr Address) Hash {
	acc, ok := s.accounts[addr]
	if !ok {
		return Hash{}
	}
	return acc.codeHash
}

func (s *State) CodeSize(addr Address) int {
	return len(s.Code(addr))
}

func (s *State) SetCode(addr Address, code []byte) {
	acc := s.account(addr)
	prevCode := acc.code
	prevHash := acc.codeHash
	acc.code = code
	acc.codeHash = keccak.Sum256(code)
	s.record(func() {
		acc.code = prevCode
		acc.codeHash = prevHash
	})
	s.touch(addr)
}

func (s *State) Storage(addr Address, key Hash) u256.Int {
	acc, ok := s.accounts[addr]
	if !ok {
		return u256.Zero()
	}
	v, dirty := acc.dirty[key]
	if dirty {
		return v
	}
	return acc.storage[key]
}

func (s *State) OriginalStorage(addr Address, key Hash) u256.Int {
	acc, ok := s.accounts[addr]
	if !ok {
		return u256.Zero()
	}
	return acc.storage[key]
}

func (s *State) SetStorage(addr Address, key Hash, value u256.Int) {
	acc := s.account(addr)
	prev, had := acc.dirty[key]
	acc.dirty[key] = value
	s.record(func() {
		if had {
			acc.dirty[key] = prev
			return
		}
		delete(acc.dirty, key)
	})
}

func (s *State) HasStorage(addr Address) bool {
	acc, ok := s.accounts[addr]
	if !ok {
		return false
	}
	for _, k := range sortedSlots(acc.dirty) {
		v := acc.dirty[k]
		if !v.IsZero() {
			return true
		}
	}
	for _, k := range sortedSlots(acc.storage) {
		v, dirty := acc.dirty[k]
		if !dirty || !v.IsZero() {
			return true
		}
	}
	return false
}

func (s *State) TransientStorage(addr Address, key Hash) u256.Int {
	return s.transient[slotKey{addr: addr, key: key}]
}

func (s *State) SetTransientStorage(addr Address, key Hash, value u256.Int) {
	k := slotKey{addr: addr, key: key}
	prev := s.transient[k]
	s.transient[k] = value
	s.record(func() { s.transient[k] = prev })
}

func (s *State) SelfDestruct(addr Address) {
	_, already := s.destructed[addr]
	if !already {
		s.destructed[addr] = struct{}{}
		s.record(func() { delete(s.destructed, addr) })
	}
	acc, ok := s.accounts[addr]
	if !ok {
		return
	}
	prev := acc.balance
	acc.balance = u256.Zero()
	s.record(func() { acc.balance = prev })
}

func (s *State) HasSelfDestructed(addr Address) bool {
	_, ok := s.destructed[addr]
	return ok
}

func (s *State) AddressWarm(addr Address) bool {
	_, ok := s.warmAddrs[addr]
	return ok
}

func (s *State) WarmAddress(addr Address) {
	_, ok := s.warmAddrs[addr]
	if ok {
		return
	}
	s.warmAddrs[addr] = struct{}{}
	s.record(func() { delete(s.warmAddrs, addr) })
}

func (s *State) SlotWarm(addr Address, key Hash) bool {
	_, ok := s.warmSlots[slotKey{addr: addr, key: key}]
	return ok
}

func (s *State) WarmSlot(addr Address, key Hash) {
	k := slotKey{addr: addr, key: key}
	_, ok := s.warmSlots[k]
	if ok {
		return
	}
	s.warmSlots[k] = struct{}{}
	s.record(func() { delete(s.warmSlots, k) })
}

func (s *State) AddRefund(delta int64) {
	s.refund += delta
	s.record(func() { s.refund -= delta })
}

func (s *State) Refund() int64 { return s.refund }

func (s *State) AddLog(log evm.Log) {
	s.logs = append(s.logs, log)
	n := len(s.logs) - 1
	s.record(func() { s.logs = s.logs[:n] })
}

var _ evm.State = (*State)(nil)
