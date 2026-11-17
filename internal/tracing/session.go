package tracing

import (
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	accountsMax = 1 << 20
	slotsMax    = 1 << 22
	noClaim     = -1
)

const _ = uint(slotsMax - accountsMax)

type Inner interface {
	evm.State
	Logs() []evm.Log
	FinalizeTx()
}

type Account struct {
	Address     evm.Address
	Balance     u256.Int
	Nonce       uint64
	Code        []byte
	EmptyBefore bool
	Slots       []evm.Hash
	Pre         map[evm.Hash]u256.Int
	Post        map[evm.Hash]u256.Int
	PostBalance u256.Int
	PostNonce   uint64
	PostCode    []byte
	Destroyed   bool
}

type TxTrace struct {
	Recorder *Recorder
	Accounts []*Account
}

type Session struct {
	Inner
	senders []evm.Address
	opts    StepOptions
	traces  []*TxTrace
	next    int
	claimed int
	cur     *TxTrace
	index   map[evm.Address]*Account
	slots   int
	logs    int
}

func NewSession(inner Inner, senders []evm.Address, opts StepOptions) *Session {
	s := &Session{Inner: inner, senders: senders, opts: opts, traces: make([]*TxTrace, len(senders)), claimed: noClaim}
	s.reset()
	return s
}

func (s *Session) Traces() []*TxTrace { return s.traces }

func (s *Session) reset() {
	s.cur = &TxTrace{}
	s.index = make(map[evm.Address]*Account)
	s.slots = 0
	s.logs = 0
	s.claimed = noClaim
}

func (s *Session) EVMTracer(tx *evm.TxContext) evm.Tracer {
	if tx == nil {
		return nil
	}
	if s.claimed != noClaim {
		if tx.Origin == s.senders[s.claimed] {
			return s.cur.Recorder
		}
		return nil
	}
	if s.next >= len(s.senders) || tx.Origin != s.senders[s.next] {
		return nil
	}
	s.claimed = s.next
	s.cur.Recorder = NewRecorder(s.Inner, s.opts)
	s.touch(tx.Origin)
	return s.cur.Recorder
}

func (s *Session) Close() *TxTrace {
	t := s.cur
	if s.claimed != noClaim {
		for _, a := range t.Accounts {
			s.capturePost(a)
		}
		s.traces[s.claimed] = t
		s.next++
	}
	if t.Recorder == nil {
		t.Recorder = NewRecorder(nil, StepOptions{})
	}
	s.reset()
	return t
}

func (s *Session) FinalizeTx() {
	if s.claimed != noClaim {
		for _, a := range s.cur.Accounts {
			s.capturePost(a)
		}
		s.traces[s.claimed] = s.cur
		s.next++
	}
	s.reset()
	s.Inner.FinalizeTx()
}

func (s *Session) capturePost(a *Account) {
	a.Destroyed = s.Inner.HasSelfDestructed(a.Address)
	a.PostBalance = s.Inner.Balance(a.Address)
	a.PostNonce = s.Inner.Nonce(a.Address)
	a.PostCode = s.Inner.Code(a.Address)
	a.Post = make(map[evm.Hash]u256.Int, len(a.Slots))
	for _, k := range a.Slots {
		a.Post[k] = s.Inner.Storage(a.Address, k)
	}
}

func (s *Session) touch(addr evm.Address) *Account {
	if a, ok := s.index[addr]; ok {
		return a
	}
	assert.Ok(len(s.cur.Accounts) < accountsMax, "traced accounts within bound")
	a := &Account{
		Address:     addr,
		Balance:     s.Inner.Balance(addr),
		Nonce:       s.Inner.Nonce(addr),
		Code:        s.Inner.Code(addr),
		EmptyBefore: s.Inner.Empty(addr),
		Pre:         make(map[evm.Hash]u256.Int),
	}
	s.index[addr] = a
	s.cur.Accounts = append(s.cur.Accounts, a)
	return a
}

func (s *Session) touchSlot(addr evm.Address, key evm.Hash) {
	a := s.touch(addr)
	if _, ok := a.Pre[key]; ok {
		return
	}
	assert.Ok(s.slots < slotsMax, "traced slots within bound")
	s.slots++
	a.Pre[key] = s.Inner.OriginalStorage(addr, key)
	a.Slots = append(a.Slots, key)
}

func (s *Session) Exist(addr evm.Address) bool {
	s.touch(addr)
	return s.Inner.Exist(addr)
}

func (s *Session) Empty(addr evm.Address) bool {
	s.touch(addr)
	return s.Inner.Empty(addr)
}

func (s *Session) CreateAccount(addr evm.Address) {
	s.touch(addr)
	s.Inner.CreateAccount(addr)
}

func (s *Session) Touch(addr evm.Address) {
	s.touch(addr)
	s.Inner.Touch(addr)
}

func (s *Session) Balance(addr evm.Address) u256.Int {
	s.touch(addr)
	return s.Inner.Balance(addr)
}

func (s *Session) AddBalance(addr evm.Address, amount u256.Int) {
	s.touch(addr)
	s.Inner.AddBalance(addr, amount)
}

func (s *Session) SubBalance(addr evm.Address, amount u256.Int) {
	s.touch(addr)
	s.Inner.SubBalance(addr, amount)
}

func (s *Session) Nonce(addr evm.Address) uint64 {
	s.touch(addr)
	return s.Inner.Nonce(addr)
}

func (s *Session) SetNonce(addr evm.Address, nonce uint64) {
	s.touch(addr)
	s.Inner.SetNonce(addr, nonce)
}

func (s *Session) Code(addr evm.Address) []byte {
	s.touch(addr)
	return s.Inner.Code(addr)
}

func (s *Session) CodeHash(addr evm.Address) evm.Hash {
	s.touch(addr)
	return s.Inner.CodeHash(addr)
}

func (s *Session) CodeSize(addr evm.Address) int {
	s.touch(addr)
	return s.Inner.CodeSize(addr)
}

func (s *Session) SetCode(addr evm.Address, code []byte) {
	s.touch(addr)
	s.Inner.SetCode(addr, code)
}

func (s *Session) Storage(addr evm.Address, key evm.Hash) u256.Int {
	s.touchSlot(addr, key)
	return s.Inner.Storage(addr, key)
}

func (s *Session) OriginalStorage(addr evm.Address, key evm.Hash) u256.Int {
	s.touchSlot(addr, key)
	return s.Inner.OriginalStorage(addr, key)
}

func (s *Session) SetStorage(addr evm.Address, key evm.Hash, value u256.Int) {
	s.touchSlot(addr, key)
	s.Inner.SetStorage(addr, key, value)
}

func (s *Session) SelfDestruct(addr evm.Address) {
	s.touch(addr)
	s.Inner.SelfDestruct(addr)
}

func (s *Session) AddLog(l evm.Log) {
	if s.claimed != noClaim {
		s.cur.Recorder.AddLog(l, s.logs)
	}
	s.logs++
	s.Inner.AddLog(l)
}
