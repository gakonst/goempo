package sim

import (
	"bytes"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/dkg"
)

func (n *node) request(d consensus.Digest) {
	if at, ok := n.requested[d]; ok && n.now-at < requestEvery {
		return
	}
	n.requested[d] = n.now
	n.broadcast(envelope{request: &d})
}

func (n *node) receive(e envelope) {
	stored := e.block != nil && n.store(e.block)
	for _, b := range e.ancestors {
		stored = n.store(b) || stored
	}
	if e.request != nil {
		n.serve(e.from, *e.request)
		return
	}
	if e.epoch > n.epoch {
		if len(n.future) < pendingMax {
			n.future = append(n.future, e)
		}
		return
	}
	if e.epoch < n.epoch {
		n.helpOld(e.from, e.epoch)
	}
	if e.epoch == n.epoch {
		n.dispatch(e)
	}
	if stored && n.voter != nil && n.startAt == nil {
		n.emit(n.voter.Reevaluate(n.now))
	}
	n.settle()
}

func (n *node) serve(peer int, d consensus.Digest) {
	b, ok := n.blocks[d]
	if !ok {
		return
	}
	reply := envelope{to: peer, block: b}
	for range syncBatch {
		p, ok := n.blocks[b.parent()]
		if !ok {
			break
		}
		reply.ancestors = append(reply.ancestors, p)
		b = p
	}
	n.send(reply)
}

func (n *node) dispatch(e envelope) {
	switch {
	case e.deal != nil:
		n.onDeal(*e.deal)
	case e.ack != nil:
		n.onAck(*e.ack)
	case e.log != nil:
		n.onLog(*e.log)
	case e.msg != nil && n.voter != nil:
		n.emit(n.voter.Handle(n.now, e.from, *e.msg))
	}
}

func (n *node) helpOld(peer int, epoch uint64) {
	b, ok := n.boundaries[epoch]
	if !ok {
		return
	}
	if at, ok := n.helpedAt[peer]; ok && n.now-at < helpEvery {
		return
	}
	n.helpedAt[peer] = n.now
	b.from = n.index
	b.to = peer
	n.outbox = append(n.outbox, b)
}

func (n *node) tick(now consensus.Millis) {
	n.now = now
	if n.voter == nil {
		return
	}
	n.emit(n.voter.Tick(now))
	n.maybeFinalizeLog()
	n.resendDeals()
	if n.myLog != nil && !n.logIncluded {
		n.broadcast(envelope{log: n.myLog})
	}
	n.settle()
}

func (n *node) deadline() consensus.Millis {
	if n.voter == nil {
		return 0
	}
	return n.voter.Deadline()
}

func (n *node) settle() {
	for range waitingMax {
		if len(n.waiting) == 0 || n.err != nil {
			return
		}
		f := n.waiting[0]
		if !n.finalize(f) {
			return
		}
		n.waiting = n.waiting[1:]
		last := n.epocher.Last(n.epoch)
		if n.head() >= last {
			n.transition(f, n.blocks[n.finalized[last]])
			return
		}
	}
}

func (n *node) finalize(f consensus.Finalized) bool {
	b, ok := n.blocks[f.Proposal.Payload]
	if !ok {
		n.request(f.Proposal.Payload)
		return false
	}
	if b.number() <= n.head() {
		if n.finalized[b.number()] != b.digest {
			n.err = errSafety
			return false
		}
		n.record(b, f)
		return true
	}
	chain := make([]*block, 0, b.number()-n.head())
	cur := b
	for range n.epocher.Length + 1 {
		chain = append(chain, cur)
		if cur.number() == n.head()+1 {
			break
		}
		next, ok := n.blocks[cur.parent()]
		if !ok {
			n.request(cur.parent())
			return false
		}
		cur = next
	}
	if cur.parent() != n.finalized[n.head()] {
		n.err = errSafety
		return false
	}
	for i := range chain {
		c := chain[len(chain)-1-i]
		n.finalized = append(n.finalized, c.digest)
		if n.myLog != nil && bytes.Equal(c.extra(), n.myLog.Encode()) {
			n.logIncluded = true
		}
	}
	n.record(b, f)
	n.maybeFinalizeLog()
	return true
}

func (n *node) record(b *block, f consensus.Finalized) {
	if _, ok := n.certs[b.number()]; ok {
		return
	}
	n.certs[b.number()] = consensus.Finalization{Proposal: f.Proposal, Certificate: f.Certificate}.Encode()
}

func (n *node) transition(f consensus.Finalized, boundary *block) {
	parent, ok := n.blocks[boundary.parent()]
	if !ok {
		n.err = errOutcome
		return
	}
	r, ok := n.reckon(parent)
	if !ok || !bytes.Equal(r.encoded, boundary.extra()) {
		n.err = errOutcome
		return
	}
	share := n.share
	if !r.failed {
		s, err := n.player.Finalize(r.logs.entries, r.selection)
		if err != nil {
			n.err = err
			return
		}
		share = s
	} else {
		n.failures++
	}
	m := consensus.Message{Kind: consensus.MsgFinalization, Proposal: f.Proposal, Certificate: f.Certificate}
	n.boundaries[n.epoch] = envelope{epoch: n.epoch, msg: &m, block: boundary}
	n.epoch++
	n.output = r.output
	n.share = share
	n.waiting = nil
	n.voter = nil
	n.startAt = boundary
}

func (n *node) outcomeOf(epoch uint64) (dkg.Outcome, bool) {
	if epoch == 0 {
		o, err := dkg.DecodeOutcome(n.blocks[n.finalized[0]].extra())
		return o, err == nil
	}
	last := n.epocher.Last(epoch - 1)
	if last > n.head() {
		return dkg.Outcome{}, false
	}
	o, err := dkg.DecodeOutcome(n.blocks[n.finalized[last]].extra())
	return o, err == nil
}
