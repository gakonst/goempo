package validator

import (
	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/types"
)

type app struct {
	e  *Engine
	st *epochState
}

func (a *app) last() uint64 { return a.e.cfg.Epocher.Last(a.st.epoch) }

func (a *app) Propose(ctx consensus.ProposeContext) (consensus.Digest, bool) {
	e := a.e
	parent, ok := e.block(ctx.ParentDigest)
	if !ok {
		e.request(ctx.ParentDigest)
		return consensus.Digest{}, false
	}
	if parent.Header.Inner.Number == a.last() {
		return ctx.ParentDigest, true
	}
	if parent.Hash() != e.cfg.Chain.Head().Hash() {
		return consensus.Digest{}, false
	}
	extra, ok := a.extraFor(parent.Header.Inner.Number + 1)
	if !ok {
		return consensus.Digest{}, false
	}
	attrs := Attributes{
		TimestampMillis: e.timestampFor(parent),
		ExtraData:       extra,
		Consensus:       types.ConsensusContext{Epoch: a.st.epoch, View: ctx.Round.View, ParentView: ctx.Parent, Proposer: e.proposer(a.st, ctx.Leader)},
	}
	b, err := e.cfg.Chain.Build(parent, attrs)
	if err != nil {
		return consensus.Digest{}, false
	}
	d := e.store(b)
	e.verdicts[d] = consensus.Accept
	e.stats.Proposed++
	e.cfg.Net.Send(wire.ChannelBroadcast, nil, wire.EncodeBlock(b.Encode()))
	return d, true
}

func (a *app) extraFor(height uint64) ([]byte, bool) {
	if height == a.last() {
		r, err := a.st.round.outcome()
		if err != nil {
			return nil, false
		}
		return r.encoded, true
	}
	return a.st.round.logToInclude(), true
}

func (a *app) Verify(ctx consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	e := a.e
	if v, ok := e.verdicts[payload]; ok {
		return v
	}
	v := a.verify(ctx, payload)
	if v != consensus.Undecided {
		e.verdicts[payload] = v
	}
	return v
}

func (a *app) Certify(ctx consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	return a.Verify(ctx, payload)
}

func (a *app) verify(ctx consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	e := a.e
	parent, ok := e.block(ctx.ParentDigest)
	if !ok {
		e.request(ctx.ParentDigest)
		return consensus.Undecided
	}
	if payload == ctx.ParentDigest {
		if parent.Header.Inner.Number != a.last() {
			return consensus.Reject
		}
		return consensus.Accept
	}
	b, ok := e.block(payload)
	if !ok {
		e.request(payload)
		return consensus.Undecided
	}
	if v := a.checkHeader(ctx, link{parent: parent, child: b}); v != consensus.Accept {
		return v
	}
	head := e.cfg.Chain.Head()
	if parent.Hash() != head.Hash() {
		if parent.Header.Inner.Number <= head.Header.Inner.Number {
			return consensus.Reject
		}
		return consensus.Undecided
	}
	if v := a.checkExtra(ctx, b); v != consensus.Accept {
		return v
	}
	if e.cfg.Chain.Execute(b) != nil {
		return consensus.Reject
	}
	return consensus.Accept
}

type link struct {
	parent *types.Block
	child  *types.Block
}

func (a *app) checkHeader(ctx consensus.ProposeContext, l link) consensus.Verdict {
	parent, h := l.parent, l.child.Header
	if h.Inner.ParentHash != parent.Hash() {
		return consensus.Reject
	}
	if h.Inner.Number != parent.Header.Inner.Number+1 {
		return consensus.Reject
	}
	if h.Inner.Number > a.last() {
		return consensus.Reject
	}
	if h.TimestampMillis() <= parent.Header.TimestampMillis() {
		return consensus.Reject
	}
	if h.Consensus == nil {
		return consensus.Reject
	}
	want := types.ConsensusContext{Epoch: a.st.epoch, View: ctx.Round.View, ParentView: ctx.Parent, Proposer: a.e.proposer(a.st, ctx.Leader)}
	if *h.Consensus != want {
		return consensus.Reject
	}
	return consensus.Accept
}

func (a *app) checkExtra(ctx consensus.ProposeContext, b *types.Block) consensus.Verdict {
	extra := b.Header.Inner.ExtraData
	if b.Header.Inner.Number == a.last() {
		r, err := a.st.round.outcome()
		if err != nil {
			return consensus.Undecided
		}
		if string(r.encoded) != string(extra) {
			return consensus.Reject
		}
		return consensus.Accept
	}
	if len(extra) == 0 {
		return consensus.Accept
	}
	l, err := dkg.DecodeSignedDealerLog(extra)
	if err != nil {
		return consensus.Reject
	}
	if !a.st.round.info.CheckSignedDealerLog(l) {
		return consensus.Reject
	}
	return consensus.Accept
}
