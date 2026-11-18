package executor

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// expiringSpec is a speculative transaction's insertion into the expiring
// nonce set. Every insertion moves the shared ring pointer, so the commit
// replays it on the real state (as Tempo's builder does) instead of
// validating the ring's slots, and accepts it only when each store falls in
// the same gas class (zero or not) it did speculatively.
type expiringSpec struct {
	hash        Hash
	validBefore uint64
	ptrZero     bool
	ptrWraps    bool
	ringZero    bool
	oldSeenZero bool
	seen        u256.Int
	seenKey     Hash
	// The ring slot predicted from the block's starting pointer and the
	// transaction's rank among expiring nonce transactions, and the seen
	// slot of the hash it held, so the commit hashes nothing when the
	// prediction holds. check uses them only after matching real state.
	pred        bool
	predPtr     uint64
	predRingKey Hash
	predRing    Hash
	predOldKey  Hash
	// Filled by check for apply.
	ring    Hash
	oldSeen Hash
	clear   bool
	next    uint64
}

func expiringCapacity(rules chain.Rules) uint64 {
	if rules.AtLeast(hardfork.T11) {
		return 3_000_000
	}
	return 300_000
}

func expiringPtr() Hash            { return primitives.Uint64ToHash(expiringPtrSlot) }
func expiringRing(ptr uint64) Hash { return mapSlot(primitives.Uint64ToHash(ptr), expiringRingSlot) }
func expiringSeen(h Hash) Hash     { return mapSlot(h, expiringSeenSlot) }

type slotReader func(addr Address, key Hash) u256.Int

// expiringView reads the set's slots for an insertion of h.
type expiringView struct {
	ptr     uint64
	ring    Hash
	oldSeen u256.Int
	seen    u256.Int
}

func readExpiring(read slotReader, h Hash) (expiringView, bool) {
	nm := precompiles.NonceManagerAddress
	p := read(nm, expiringPtr())
	if !p.IsUint64() || p.Uint64() > 0xffff_ffff {
		return expiringView{}, false
	}
	v := expiringView{ptr: p.Uint64(), seen: read(nm, expiringSeen(h))}
	v.ring = Hash(read(nm, expiringRing(v.ptr)).Bytes32())
	if v.ring != (Hash{}) {
		v.oldSeen = read(nm, expiringSeen(v.ring))
	}
	return v, true
}

// speculateExpiring runs the insertion inside an exclude window, recording
// the gas classes it saw.
func (r *txRun) speculateExpiring(h Hash) error {
	sp := r.env.Spec
	pre, ok := readExpiring(sp.Peek, h)
	if !ok {
		return precompiles.CheckAndMarkExpiringNonce(r.ctx, h, r.view.validBefore)
	}
	sp.BeginExclude(precompiles.NonceManagerAddress)
	err := precompiles.CheckAndMarkExpiringNonce(r.ctx, h, r.view.validBefore)
	sp.EndExclude()
	if err != nil {
		return err
	}
	capacity := expiringCapacity(r.env.Rules)
	e := &expiringSpec{
		hash:        h,
		validBefore: r.view.validBefore,
		ptrZero:     pre.ptr == 0,
		ptrWraps:    pre.ptr+1 >= capacity,
		ringZero:    pre.ring == (Hash{}),
		oldSeenZero: pre.oldSeen.IsZero(),
		seen:        pre.seen,
		seenKey:     expiringSeen(h),
	}
	if p := r.env.expiring; p.ok {
		e.pred = true
		e.predPtr = (p.base + uint64(p.rank)) % capacity
		e.predRingKey = expiringRing(e.predPtr)
		e.predRing = Hash(sp.Peek(precompiles.NonceManagerAddress, e.predRingKey).Bytes32())
		if e.predRing != (Hash{}) {
			e.predOldKey = expiringSeen(e.predRing)
		}
	}
	r.expiring = e
	return nil
}

// expiringPlan places a transaction in its block's expiring nonce set
// insertions, for expiringSpec's prediction.
type expiringPlan struct {
	ok   bool
	base uint64
	rank int
}

// check reports whether the insertion replays on the real state with the
// same outcome and gas, and prepares apply.
func (e *expiringSpec) check(ps *state.State, now uint64, capacity uint64) bool {
	nm := precompiles.NonceManagerAddress
	p := ps.Storage(nm, expiringPtr())
	if !p.IsUint64() || p.Uint64() >= capacity {
		return false
	}
	ptr := p.Uint64()
	if ps.Storage(nm, e.seenKey) != e.seen {
		return false
	}
	if (ptr == 0) != e.ptrZero || (ptr+1 >= capacity) != e.ptrWraps {
		return false
	}
	ringKey := e.predRingKey
	if !e.pred || ptr != e.predPtr {
		ringKey = expiringRing(ptr)
	}
	ring := Hash(ps.Storage(nm, ringKey).Bytes32())
	if (ring == (Hash{})) != e.ringZero || ring == e.hash {
		return false
	}
	var oldKey Hash
	oldSeen := u256.Zero()
	if ring != (Hash{}) {
		oldKey = e.predOldKey
		if !e.pred || ring != e.predRing {
			oldKey = expiringSeen(ring)
		}
		oldSeen = ps.Storage(nm, oldKey)
	}
	if !oldSeen.IsZero() && (!oldSeen.IsUint64() || oldSeen.Uint64() > now) {
		return false
	}
	if oldSeen.IsZero() != e.oldSeenZero {
		return false
	}
	e.ring = ringKey
	e.clear = ring != (Hash{})
	e.oldSeen = oldKey
	e.next = ptr + 1
	if e.next >= capacity {
		e.next = 0
	}
	return true
}

func (e *expiringSpec) apply(ps *state.State) {
	assert.Ok(e.ring != (Hash{}), "an expiring insertion is checked before it applies")
	nm := precompiles.NonceManagerAddress
	if e.clear {
		ps.ApplySlot(nm, e.oldSeen, u256.Zero())
	}
	ps.ApplySlot(nm, e.ring, u256.FromBytes(e.hash[:]))
	ps.ApplySlot(nm, e.seenKey, u256.FromUint64(e.validBefore))
	ps.ApplySlot(nm, expiringPtr(), u256.FromUint64(e.next))
}
