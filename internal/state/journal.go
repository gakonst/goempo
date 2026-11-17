package state

import (
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type entryKind uint8

const (
	entryReplace entryKind = iota + 1
	entryExist
	entryBalance
	entryNonce
	entryCode
	entryStorage
	entryTransient
	entryTouch
	entryCreated
	entryDestruct
	entryWarmAddr
	entryWarmSlot
	entryRefund
	entryLog
)

type entry struct {
	kind     entryKind
	had      bool
	obj      *object
	prev     *object
	slot     slotKey
	value    u256.Int
	nonce    uint64
	refund   int64
	code     []byte
	codeHash Hash
}

func (s *State) record(e entry) {
	s.journal = append(s.journal, e)
}

func (s *State) Snapshot() int { return len(s.journal) }

func (s *State) RevertToSnapshot(id int) {
	assert.Ok(id >= 0, "snapshot id is not negative")
	assert.Ok(id <= len(s.journal), "snapshot id is within the journal")
	for i := len(s.journal) - 1; i >= id; i-- {
		s.undo(&s.journal[i])
		s.journal[i] = entry{}
	}
	s.journal = s.journal[:id]
}

func (s *State) undo(e *entry) {
	switch e.kind {
	case entryReplace:
		s.accounts[e.slot.addr] = e.prev
	case entryExist:
		e.obj.exists = false
	case entryBalance:
		e.obj.balance = e.value
	case entryNonce:
		e.obj.nonce = e.nonce
	case entryCode:
		e.obj.code = e.code
		e.obj.codeHash = e.codeHash
		e.obj.codeLoaded = true
	case entryStorage:
		s.undoStorage(e)
	case entryTransient:
		s.transient[e.slot] = e.value
	case entryTouch:
		s.touched = popAddress(s.touched, e.slot.addr)
		delete(s.touchedSet, e.slot.addr)
	case entryCreated:
		delete(s.created, e.slot.addr)
	case entryDestruct:
		s.destructed = popAddress(s.destructed, e.slot.addr)
		delete(s.destructedSet, e.slot.addr)
	case entryWarmAddr:
		delete(s.warmAddrs, e.slot.addr)
	case entryWarmSlot:
		delete(s.warmSlots, e.slot)
	case entryRefund:
		s.refund -= e.refund
	case entryLog:
		s.logs = s.logs[:len(s.logs)-1]
	default:
		assert.Unreachable("state journal entry kind")
	}
}

func (s *State) undoStorage(e *entry) {
	if e.had {
		e.obj.dirty[e.slot.key] = e.value
		return
	}
	delete(e.obj.dirty, e.slot.key)
}

func popAddress(list []Address, addr Address) []Address {
	assert.Ok(len(list) > 0, "state journal pops an empty address list")
	assert.Ok(list[len(list)-1] == addr, "state journal pops addresses in order")
	return list[:len(list)-1]
}
