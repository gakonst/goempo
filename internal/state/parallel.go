package state

import (
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type parTrack struct {
	accounts map[Address]struct{}
	slots    map[slotKey]struct{}
	wiped    map[Address]struct{}
}

func (s *State) BeginParallel() {
	assert.Ok(s.spec == nil, "the real state commits speculation")
	assert.Ok(s.track == nil, "parallel commits do not nest")
	assert.Ok(len(s.journal) == 0, "parallel commits start between transactions")
	if s.trackBuf == nil {
		s.trackBuf = &parTrack{
			accounts: make(map[Address]struct{}),
			slots:    make(map[slotKey]struct{}),
			wiped:    make(map[Address]struct{}),
		}
	}
	clear(s.trackBuf.accounts)
	clear(s.trackBuf.slots)
	clear(s.trackBuf.wiped)
	s.track = s.trackBuf
}

func (s *State) EndParallel() {
	assert.Ok(s.track != nil, "parallel commits were started")
	s.track = nil
}

func (s *State) markDirty(addr Address) {
	s.blockDirty[addr] = struct{}{}
	if s.track != nil {
		s.track.accounts[addr] = struct{}{}
	}
}

func (s *State) markWiped(addr Address) {
	s.markDirty(addr)
	if s.track != nil {
		s.track.wiped[addr] = struct{}{}
	}
}

func (s *State) markSlot(k slotKey) {
	if s.track != nil {
		s.track.slots[k] = struct{}{}
	}
}

func (s *State) ValidateSpec(t *SpecTx) bool {
	assert.Ok(s.track != nil, "speculation is validated inside parallel commits")
	assert.Ok(len(s.journal) == 0, "speculation is validated between transactions")
	if t.unsafe {
		return false
	}
	tr := s.track
	t.rebased = false
	for i := range t.accounts {
		a := &t.accounts[i]
		if _, ok := tr.accounts[a.addr]; !ok && !a.inherited {
			continue
		}
		if valueOf(s.load(a.addr)) != a.value {
			return false
		}
	}
	for i := range t.slots {
		sl := &t.slots[i]
		sl.shifted = false
		_, written := tr.slots[sl.key]
		_, wiped := tr.wiped[sl.key.addr]
		if !written && !wiped && !sl.inherited {
			continue
		}
		cur := s.committedValue(s.load(sl.key.addr), sl.key.key)
		if cur == sl.r0 {
			continue
		}
		if !sl.affine || !rebases(sl, cur) {
			return false
		}
		sl.cur = cur
		sl.shifted = true
		t.rebased = true
	}
	return s.err == nil
}

func rebases(sl *specSlot, cur u256.Int) bool {
	if cur.IsZero() {
		return false
	}
	if cur.Gt(sl.r0) {
		up := cur.Sub(sl.r0)
		return !sl.high.Gt(u256.Max().Sub(up))
	}
	down := sl.r0.Sub(cur)
	return sl.low.Gt(down)
}

func (s *State) ApplySpec(t *SpecTx) {
	assert.Ok(s.track != nil, "speculation is applied inside parallel commits")
	for i := range t.writes {
		w := &t.writes[i]
		old := s.load(w.addr)
		o := old
		if w.replaced {
			o = newObject(w.addr, old.base)
			o.wiped = true
			o.codeLoaded = true
			s.accounts[w.addr] = o
			s.markWiped(w.addr)
		}
		o.exists = w.exists
		o.nonce = w.nonce
		o.balance = w.balance
		o.codeHash = w.codeHash
		if w.codeSet {
			o.code = w.code
			o.codeLoaded = true
			o.codeDirty = o.codeDirty || w.codeDirty
		}
		for j := range w.slots {
			sw := &w.slots[j]
			v := sw.v
			if sw.read >= 0 && t.slots[sw.read].shifted {
				sl := &t.slots[sw.read]
				v = v.Add(sl.cur).Sub(sl.r0)
			}
			before := len(o.committed)
			o.setChanged(sw.key, v)
			s.cachedSlots += len(o.committed) - before
			s.markSlot(slotKey{addr: w.addr, key: sw.key})
		}
		s.markDirty(w.addr)
	}
}

// ApplySlot sets a slot between transactions, as a committed transaction's
// write: the executor's semantic replay of an excluded window.
func (s *State) ApplySlot(addr Address, key Hash, v u256.Int) {
	assert.Ok(s.track != nil, "slots are applied inside parallel commits")
	assert.Ok(len(s.journal) == 0, "slots are applied between transactions")
	o := s.load(addr)
	before := len(o.committed)
	o.setChanged(key, v)
	s.cachedSlots += len(o.committed) - before
	s.markSlot(slotKey{addr: addr, key: key})
	s.markDirty(addr)
}
