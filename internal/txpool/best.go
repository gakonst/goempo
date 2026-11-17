package txpool

import (
	"maps"
	"math/bits"
	"slices"

	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type ready struct {
	tx  *Tx
	tip u256.Int
}

func (r ready) before(o ready) bool {
	if !r.tip.Eq(o.tip) {
		return o.tip.Lt(r.tip)
	}
	return r.tx.seq < o.tx.seq
}

const heapDepthMax = bits.UintSize + 1

const _ = uint(heapDepthMax - bits.UintSize)

type readyHeap struct {
	items []ready
}

func (h *readyHeap) push(r ready) {
	h.items = append(h.items, r)
	i := len(h.items) - 1
	depth := 0
	for ; depth < heapDepthMax; depth++ {
		if i == 0 {
			break
		}
		parent := (i - 1) / 2
		if !h.items[i].before(h.items[parent]) {
			break
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
	assert.Ok(depth < heapDepthMax, "heap push stays within the depth cap")
}

func (h *readyHeap) pop() ready {
	top := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	i := 0
	depth := 0
	for ; depth < heapDepthMax; depth++ {
		if i >= last {
			break
		}
		least := i
		left, right := 2*i+1, 2*i+2
		if left < last && h.items[left].before(h.items[least]) {
			least = left
		}
		if right < last && h.items[right].before(h.items[least]) {
			least = right
		}
		if least == i {
			break
		}
		h.items[i], h.items[least] = h.items[least], h.items[i]
		i = least
	}
	assert.Ok(depth < heapDepthMax, "heap pop stays within the depth cap")
	return top
}

type laneView struct {
	next uint64
	txs  map[uint64]*Tx
}

type BlockEnv struct {
	BaseFee   uint64
	Timestamp uint64
}

type Best struct {
	env     BlockEnv
	heap    readyHeap
	lanes   map[laneKey]*laneView
	invalid map[laneKey]bool
}

func (p *Pool) Best(env BlockEnv) *Best {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := &Best{env: env, lanes: map[laneKey]*laneView{}, invalid: map[laneKey]bool{}}
	for _, id := range slices.Sorted(maps.Keys(p.laneIDs)) {
		l := p.laneIDs[id]
		v := &laneView{next: l.base, txs: maps.Clone(l.txs)}
		b.lanes[l.key] = v
		b.offer(l.key, v)
	}
	for _, seq := range slices.Sorted(maps.Keys(p.bySeq)) {
		if t := p.bySeq[seq]; t.Info.ExpiryNonce {
			b.push(t)
		}
	}
	return b
}

func (b *Best) push(t *Tx) bool {
	tip, ok := t.Info.EffectiveTip(b.env.BaseFee)
	if !ok || !t.IncludableAt(b.env.Timestamp) {
		return false
	}
	b.heap.push(ready{tx: t, tip: tip})
	return true
}

func (b *Best) offer(key laneKey, v *laneView) {
	t := v.txs[v.next]
	if t == nil {
		return
	}
	if !b.push(t) {
		b.invalid[key] = true
	}
}

func (b *Best) Next() (*Tx, bool) {
	pending := len(b.heap.items)
	for range pending {
		if len(b.heap.items) == 0 {
			break
		}
		t := b.heap.pop().tx
		if t.Info.ExpiryNonce {
			return t, true
		}
		if b.invalid[t.lane] {
			continue
		}
		v := b.lanes[t.lane]
		v.next++
		b.offer(t.lane, v)
		return t, true
	}
	return nil, false
}

func (b *Best) MarkInvalid(t *Tx) {
	if t.Info.ExpiryNonce {
		return
	}
	b.invalid[t.lane] = true
}
