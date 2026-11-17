package consensus_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

const (
	netSize      = 4
	netQuorum    = 3
	netEpoch     = 3
	stepsMax     = 4000
	tickMillis   = 50
	targetViews  = 6
	stepsPerView = 400
)

const _ = uint(stepsMax - stepsPerView*(targetViews+4))

type fakeApp struct {
	index   int
	certify map[consensus.Digest]consensus.Verdict
	seen    []consensus.ProposeContext
}

func payloadOf(leader int, view uint64) consensus.Digest {
	return consensus.Digest{0: byte(leader + 1), 1: byte(view)}
}

func (a *fakeApp) Propose(ctx consensus.ProposeContext) (consensus.Digest, bool) {
	assert.Equal(ctx.Leader, a.index, "only the elected leader proposes")
	return payloadOf(a.index, ctx.Round.View), true
}

func (a *fakeApp) Verify(ctx consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	a.seen = append(a.seen, ctx)
	if payload != payloadOf(ctx.Leader, ctx.Round.View) {
		return consensus.Reject
	}
	return consensus.Accept
}

func (a *fakeApp) Certify(_ consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	if v, ok := a.certify[payload]; ok {
		return v
	}
	return consensus.Accept
}

type packet struct {
	from int
	msg  consensus.Message
}

type net struct {
	voters    []*consensus.Voter
	apps      []*fakeApp
	queue     []packet
	now       consensus.Millis
	finalized [netSize][]consensus.Finalized
	sent      [netSize][]consensus.Message
	down      [netSize]bool
}

func newNet(certify map[consensus.Digest]consensus.Verdict) *net {
	poly := make(dkg.Poly, netQuorum)
	for i := range poly {
		poly[i] = bls12381.ScalarFromUint64(uint64(1000 + 17*i))
	}
	commit := poly.Commit()
	publics := make([]bls12381.G2, netSize)
	for i := range publics {
		publics[i] = commit.Eval(i)
	}
	n := &net{}
	ns := consensus.NewNamespace(consensus.TempoNamespace)
	for i := range netSize {
		app := &fakeApp{index: i, certify: certify}
		v := consensus.NewVoter(consensus.VoterConfig{
			Epoch: netEpoch, Signer: consensus.NewSigner(dkg.Share{Index: i, Private: poly.Eval(i)}, ns),
			Identity: consensus.NewIdentity(commit.Constant()), Publics: publics, Namespace: ns,
			Timeouts: consensus.Timeouts{Leader: 1000, Notarization: 2000, Retry: 500}, Elector: consensus.ElectorV1, App: app,
		})
		n.voters = append(n.voters, v)
		n.apps = append(n.apps, app)
	}
	for i, v := range n.voters {
		n.take(i, v.Start(0))
	}
	return n
}

func (n *net) take(i int, o consensus.Output) {
	n.finalized[i] = append(n.finalized[i], o.Finalized...)
	n.sent[i] = append(n.sent[i], o.Broadcast...)
	for _, m := range o.Broadcast {
		n.queue = append(n.queue, packet{from: i, msg: m})
	}
}

func (n *net) step() {
	if len(n.queue) == 0 {
		n.now += tickMillis
		for i, v := range n.voters {
			if !n.down[i] {
				n.take(i, v.Tick(n.now))
			}
		}
		return
	}
	p := n.queue[0]
	n.queue = n.queue[1:]
	if n.down[p.from] {
		return
	}
	for i, v := range n.voters {
		if i != p.from && !n.down[i] {
			n.take(i, v.Handle(n.now, p.from, p.msg))
		}
	}
}

func (n *net) runUntil(done func() bool) {
	for range stepsMax {
		if done() {
			return
		}
		n.step()
	}
	assert.Fail("network did not reach the goal in %d steps", stepsMax)
}

func (n *net) allFinalized(views int) func() bool {
	return func() bool {
		for i := range netSize {
			if !n.down[i] && len(n.finalized[i]) < views {
				return false
			}
		}
		return true
	}
}

func TestVoterLeaderNotarizeCarriesProposal(_ *testing.T) {
	n := newNet(nil)
	n.runUntil(n.allFinalized(targetViews))
	for i := range netSize {
		for j, f := range n.finalized[i][:targetViews] {
			assert.Equal(f, n.finalized[0][j], "every voter finalizes the same chain")
		}
	}
	for _, app := range n.apps {
		for _, ctx := range app.seen {
			assert.Ok(ctx.Leader != consensus.NoLeader, "verified proposals have an elected leader")
		}
	}
	for i := range netSize {
		for _, m := range n.sent[i] {
			assert.Ok(m.Kind != consensus.MsgVote || m.Vote.Kind != consensus.KindNullify, "no view times out on a healthy network")
		}
	}
}

func TestVoterCertifyGatesFinalize(_ *testing.T) {
	certify := map[consensus.Digest]consensus.Verdict{}
	n := newNet(certify)
	for v := range uint64(16) {
		for l := range netSize {
			certify[payloadOf(l, v)] = consensus.Undecided
		}
	}
	n.runUntil(func() bool { return n.now > 3000 })
	for i := range netSize {
		assert.Equal(len(n.finalized[i]), 0, "nothing finalizes while certification is undecided")
		for _, m := range n.sent[i] {
			assert.Ok(m.Kind != consensus.MsgVote || m.Vote.Kind != consensus.KindFinalize, "no finalize vote before certify accepts")
		}
	}
	for k := range certify {
		certify[k] = consensus.Accept
	}
	for i, v := range n.voters {
		n.take(i, v.Reevaluate(n.now))
	}
	n.runUntil(n.allFinalized(1))
}

func TestVoterLeaderDownIsSkipped(_ *testing.T) {
	n := newNet(nil)
	n.runUntil(n.allFinalized(2))
	n.down[netSize-1] = true
	n.runUntil(n.allFinalized(targetViews + 4))
	last := n.finalized[0][len(n.finalized[0])-1]
	assert.Ok(last.Proposal.Round.View > uint64(targetViews), "views past the outage finalize")
}
