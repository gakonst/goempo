package consensus

import (
	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

type Millis int64

const (
	viewsAheadMax = 64
	viewsBackMax  = 1 << 16
	tasksMax      = 1 << 16
)

const _ = uint(viewsBackMax - viewsAheadMax)

const _ = uint(viewsBackMax - dkg.ParticipantsMax)

const _ = uint(tasksMax - 16*dkg.ParticipantsMax)

type Timeouts struct {
	Leader       Millis
	Notarization Millis
	Retry        Millis
}

type ProposeContext struct {
	Round        Round
	Parent       uint64
	ParentDigest Digest
	Leader       int
}

type Application interface {
	Propose(ctx ProposeContext) (Digest, bool)
	Verify(ctx ProposeContext, payload Digest) bool
}

type VoterConfig struct {
	Epoch     uint64
	Signer    Signer
	Identity  Identity
	Publics   []bls12381.G2
	Namespace Namespace
	Genesis   Digest
	Timeouts  Timeouts
	App       Application
}

type MessageKind uint8

const (
	MsgProposal MessageKind = iota + 1
	MsgVote
	MsgNotarization
	MsgNullification
	MsgFinalization
)

type Message struct {
	Kind        MessageKind
	Proposal    Proposal
	Vote        Vote
	Certificate Certificate
}

type Finalized struct {
	Proposal    Proposal
	Certificate Certificate
}

type Output struct {
	Broadcast []Message
	Finalized []Finalized
}

type certified struct {
	proposal Proposal
	cert     Certificate
}

type taskKind uint8

const (
	taskVote taskKind = iota + 1
	taskNotarization
	taskNullification
	taskFinalization
	taskEnter
	taskPropose
)

type task struct {
	kind taskKind
	vote Vote
	cert certified
	view uint64
}

type viewState struct {
	proposal      *Proposal
	verified      bool
	proposed      bool
	notarizes     []Vote
	nullifies     []Vote
	finalizes     []Vote
	notarization  *certified
	nullification *certified
	finalization  *certified
	sentNullify   bool
	sentFinalize  bool
	nullifyVote   Vote
	helped        bool
}

func (s *viewState) payload() (Digest, bool) {
	if s.finalization != nil {
		return s.finalization.proposal.Payload, true
	}
	if s.notarization != nil {
		return s.notarization.proposal.Payload, true
	}
	return Digest{}, false
}

type Voter struct {
	cfg       VoterConfig
	quorum    int
	view      uint64
	finalized uint64
	views     map[uint64]*viewState
	leaderAt  Millis
	advanceAt Millis
	retryAt   Millis
	lastCert  *Message
	lastFinal *Message
	tasks     []task
	out       Output
	now       Millis
}

func NewVoter(cfg VoterConfig) *Voter {
	n := len(cfg.Publics)
	assert.Ok(n > 0, "consensus: voter has participants")
	assert.Ok(n <= dkg.ParticipantsMax, "consensus: participants within bound")
	return &Voter{cfg: cfg, quorum: n - (n-1)/3, views: map[uint64]*viewState{}}
}

func (v *Voter) Epoch() uint64 { return v.cfg.Epoch }

func (v *Voter) View() uint64 { return v.view }

func (v *Voter) Leader(view uint64) int {
	return int((v.cfg.Epoch + view) % uint64(len(v.cfg.Publics)))
}

func (v *Voter) push(t task) {
	assert.Ok(len(v.tasks) < tasksMax, "consensus: voter tasks within bound")
	v.tasks = append(v.tasks, t)
}

func (v *Voter) drain() Output {
	for range tasksMax {
		if len(v.tasks) == 0 {
			break
		}
		t := v.tasks[0]
		v.tasks = v.tasks[1:]
		v.run(t)
	}
	assert.Ok(len(v.tasks) == 0, "consensus: voter tasks drained")
	o := v.out
	v.out = Output{}
	return o
}

func (v *Voter) run(t task) {
	switch t.kind {
	case taskVote:
		v.storeVote(t.vote)
	case taskNotarization:
		v.onNotarization(t.cert)
	case taskNullification:
		v.onNullification(t.cert)
	case taskFinalization:
		v.onFinalization(t.cert)
	case taskEnter:
		v.enter(t.view)
	case taskPropose:
		v.tryPropose()
		v.evaluate(v.view)
	default:
		assert.Unreachable("consensus: unknown voter task")
	}
}

func (v *Voter) Start(now Millis) Output {
	v.now = now
	v.push(task{kind: taskEnter, view: 1})
	return v.drain()
}

func (v *Voter) state(view uint64) *viewState {
	s, ok := v.views[view]
	if !ok {
		s = &viewState{}
		v.views[view] = s
	}
	return s
}

func (v *Voter) broadcast(m Message) {
	v.out.Broadcast = append(v.out.Broadcast, m)
}

func (v *Voter) round(view uint64) Round { return Round{Epoch: v.cfg.Epoch, View: view} }

func (v *Voter) enter(view uint64) {
	if view <= v.view {
		return
	}
	v.view = view
	v.leaderAt = v.now + v.cfg.Timeouts.Leader
	v.advanceAt = v.now + v.cfg.Timeouts.Notarization
	v.retryAt = 0
	v.push(task{kind: taskPropose})
}

func (v *Voter) prune() {
	for k := range v.views {
		if k < v.finalized {
			delete(v.views, k)
		}
	}
}

func (v *Voter) Deadline() Millis {
	s := v.views[v.view]
	if s != nil && s.sentNullify {
		return v.retryAt
	}
	if s != nil && s.verified {
		return v.advanceAt
	}
	return min(v.leaderAt, v.advanceAt)
}

type parentRef struct {
	view   uint64
	digest Digest
}

func (v *Voter) parent(view uint64) (parentRef, bool) {
	for k := range min(view, viewsBackMax) {
		p := view - 1 - k
		if p == 0 {
			return parentRef{digest: v.cfg.Genesis}, true
		}
		s := v.views[p]
		if s == nil {
			return parentRef{}, false
		}
		if d, ok := s.payload(); ok {
			return parentRef{view: p, digest: d}, true
		}
		if s.nullification == nil {
			return parentRef{}, false
		}
	}
	return parentRef{}, false
}

func (v *Voter) tryPropose() {
	view := v.view
	if v.Leader(view) != v.cfg.Signer.Index() {
		return
	}
	s := v.state(view)
	if s.proposed || s.sentNullify || s.proposal != nil {
		return
	}
	parent, ok := v.parent(view)
	if !ok {
		return
	}
	ctx := ProposeContext{Round: v.round(view), Parent: parent.view, ParentDigest: parent.digest, Leader: v.Leader(view)}
	payload, ok := v.cfg.App.Propose(ctx)
	if !ok {
		return
	}
	p := Proposal{Round: ctx.Round, Parent: parent.view, Payload: payload}
	s.proposed = true
	s.proposal = &p
	s.verified = true
	v.broadcast(Message{Kind: MsgProposal, Proposal: p})
	v.notarize(p)
}

func (v *Voter) notarize(p Proposal) {
	vote := v.cfg.Signer.Sign(KindNotarize, p)
	v.broadcast(Message{Kind: MsgVote, Vote: vote})
	v.push(task{kind: taskVote, vote: vote})
}

func (v *Voter) gapsNullified(p Proposal) bool {
	view := p.Round.View
	for i := range min(view-p.Parent-1, viewsBackMax) {
		gap := v.views[p.Parent+1+i]
		if gap == nil || gap.nullification == nil {
			return false
		}
	}
	return true
}

func (v *Voter) parentDigest(p Proposal) (Digest, bool) {
	if p.Parent == 0 {
		return v.cfg.Genesis, true
	}
	ps := v.views[p.Parent]
	if ps == nil {
		return Digest{}, false
	}
	return ps.payload()
}

func (v *Voter) evaluate(view uint64) {
	s := v.views[view]
	if s == nil || s.proposal == nil || s.verified || s.sentNullify || view != v.view {
		return
	}
	p := *s.proposal
	if p.Parent >= view || p.Parent < v.finalized || !v.gapsNullified(p) {
		return
	}
	digest, ok := v.parentDigest(p)
	if !ok {
		return
	}
	ctx := ProposeContext{Round: p.Round, Parent: p.Parent, ParentDigest: digest, Leader: v.Leader(view)}
	if !v.cfg.App.Verify(ctx, p.Payload) {
		return
	}
	s.verified = true
	v.notarize(p)
}

func (v *Voter) Reevaluate(now Millis) Output {
	v.now = now
	v.push(task{kind: taskPropose})
	return v.drain()
}

func (v *Voter) finalizedBefore(view uint64) bool {
	return v.finalized > 0 && view <= v.finalized
}

func (v *Voter) known(m Message) bool {
	view := m.Proposal.Round.View
	if m.Proposal.Round.Epoch != v.cfg.Epoch || v.finalizedBefore(view) {
		return true
	}
	s := v.views[view]
	if s == nil {
		return false
	}
	switch m.Kind {
	case MsgNotarization:
		return s.notarization != nil
	case MsgNullification:
		return s.nullification != nil
	case MsgFinalization:
		return s.finalization != nil
	case MsgProposal, MsgVote:
		return false
	default:
		assert.Unreachable("consensus: unknown message kind")
	}
	return false
}

func (k MessageKind) Valid() bool { return k >= MsgProposal && k <= MsgFinalization }

func (v *Voter) Handle(now Millis, from int, m Message) Output {
	v.now = now
	if !m.Kind.Valid() {
		return v.drain()
	}
	switch m.Kind {
	case MsgProposal:
		v.onProposal(from, m.Proposal)
	case MsgVote:
		v.onVoteMessage(from, m.Vote)
	case MsgNotarization, MsgNullification, MsgFinalization:
		if !v.known(m) {
			v.onCertificate(m)
		}
	default:
		assert.Unreachable("consensus: unknown message kind")
	}
	return v.drain()
}

func (v *Voter) onCertificate(m Message) {
	c := certified{proposal: m.Proposal, cert: m.Certificate}
	switch m.Kind {
	case MsgNotarization:
		if VerifyNotarization(v.cfg.Identity, v.cfg.Namespace, m.Proposal, m.Certificate) == nil {
			v.push(task{kind: taskNotarization, cert: c})
		}
	case MsgNullification:
		if VerifyNullification(v.cfg.Identity, v.cfg.Namespace, m.Proposal.Round, m.Certificate) == nil {
			v.push(task{kind: taskNullification, cert: c})
		}
	case MsgFinalization:
		if VerifyFinalization(v.cfg.Identity, v.cfg.Namespace, Finalization{Proposal: m.Proposal, Certificate: m.Certificate}) == nil {
			v.push(task{kind: taskFinalization, cert: c})
		}
	case MsgProposal, MsgVote:
		return
	default:
		assert.Unreachable("consensus: unknown certificate kind")
	}
}

func (v *Voter) inWindow(r Round) bool {
	if r.Epoch != v.cfg.Epoch || r.View <= v.finalized {
		return false
	}
	return r.View <= v.view+viewsAheadMax
}

func (v *Voter) onProposal(from int, p Proposal) {
	view := p.Round.View
	if !v.inWindow(p.Round) || view < v.view || from != v.Leader(view) {
		return
	}
	s := v.state(view)
	if s.proposal != nil {
		return
	}
	s.proposal = &p
	v.evaluate(view)
}

func (v *Voter) onVoteMessage(from int, vote Vote) {
	if !vote.Kind.Valid() || vote.Signer != from || from < 0 || from >= len(v.cfg.Publics) {
		return
	}
	if vote.Proposal.Round.Epoch == v.cfg.Epoch && vote.Proposal.Round.View < v.view {
		v.help(vote.Proposal.Round.View)
	}
	if !v.inWindow(vote.Proposal.Round) {
		return
	}
	v.push(task{kind: taskVote, vote: vote})
}

func (v *Voter) help(view uint64) {
	s := v.views[view]
	if s == nil || s.helped {
		return
	}
	s.helped = true
	v.rebroadcastCertificates()
}

func (v *Voter) rebroadcastCertificates() {
	if v.lastCert != nil {
		v.broadcast(*v.lastCert)
	}
	if v.lastFinal != nil {
		v.broadcast(*v.lastFinal)
	}
}

func (v *Voter) votes(s *viewState, kind VoteKind) *[]Vote {
	switch kind {
	case KindNotarize:
		return &s.notarizes
	case KindNullify:
		return &s.nullifies
	case KindFinalize:
		return &s.finalizes
	default:
		assert.Unreachable("consensus: unknown vote kind")
	}
	return nil
}

func (v *Voter) storeVote(vote Vote) {
	s := v.state(vote.Proposal.Round.View)
	list := v.votes(s, vote.Kind)
	for _, have := range *list {
		if have.Signer == vote.Signer {
			return
		}
	}
	*list = append(*list, vote)
	v.checkQuorum(vote.Proposal.Round.View, vote.Kind)
}

func (v *Voter) matching(list []Vote, p Proposal) []Vote {
	out := make([]Vote, 0, len(list))
	for _, x := range list {
		if x.Proposal == p {
			out = append(out, x)
		}
	}
	return out
}

func (v *Voter) certifiedAs(s *viewState, kind VoteKind) bool {
	switch kind {
	case KindNotarize:
		return s.notarization != nil
	case KindNullify:
		return s.nullification != nil
	case KindFinalize:
		return s.finalization != nil
	default:
		assert.Unreachable("consensus: unknown vote kind")
	}
	return false
}

func (v *Voter) checkQuorum(view uint64, kind VoteKind) {
	s := v.state(view)
	if v.certifiedAs(s, kind) {
		return
	}
	list := *v.votes(s, kind)
	for _, candidate := range list {
		same := v.matching(list, candidate.Proposal)
		if len(same) < v.quorum {
			continue
		}
		c, ok := v.assemble(s, kind, same)
		if !ok {
			return
		}
		v.push(task{kind: certTask(kind), cert: certified{proposal: candidate.Proposal, cert: c}})
		return
	}
}

func certTask(kind VoteKind) taskKind {
	switch kind {
	case KindNotarize:
		return taskNotarization
	case KindNullify:
		return taskNullification
	case KindFinalize:
		return taskFinalization
	default:
		assert.Unreachable("consensus: unknown vote kind")
	}
	return 0
}

func (v *Voter) assemble(s *viewState, kind VoteKind, votes []Vote) (Certificate, bool) {
	c, err := Assemble(votes[:v.quorum])
	if err == nil {
		check := Vote{Kind: kind, Proposal: votes[0].Proposal, Vote: c.Vote, Seed: c.Seed}
		if verifySameSigner(v.cfg.Identity, check.entries(v.cfg.Namespace)) == nil {
			return c, true
		}
	}
	list := v.votes(s, kind)
	kept := (*list)[:0]
	for _, x := range *list {
		if VerifyVote(v.cfg.Publics[x.Signer], v.cfg.Namespace, x) == nil {
			kept = append(kept, x)
		}
	}
	*list = kept
	return Certificate{}, false
}

func (v *Voter) onNotarization(c certified) {
	view := c.proposal.Round.View
	s := v.state(view)
	if v.finalizedBefore(view) || s.notarization != nil {
		return
	}
	s.notarization = &c
	m := Message{Kind: MsgNotarization, Proposal: c.proposal, Certificate: c.cert}
	v.broadcast(m)
	v.lastCert = &m
	if !s.sentNullify && !s.sentFinalize {
		s.sentFinalize = true
		vote := v.cfg.Signer.Sign(KindFinalize, c.proposal)
		v.broadcast(Message{Kind: MsgVote, Vote: vote})
		v.push(task{kind: taskVote, vote: vote})
	}
	v.advance(view)
}

func (v *Voter) advance(view uint64) {
	v.push(task{kind: taskEnter, view: view + 1})
	v.push(task{kind: taskPropose})
}

func (v *Voter) onNullification(c certified) {
	view := c.proposal.Round.View
	s := v.state(view)
	if v.finalizedBefore(view) || s.nullification != nil {
		return
	}
	s.nullification = &c
	m := Message{Kind: MsgNullification, Proposal: c.proposal, Certificate: c.cert}
	v.broadcast(m)
	v.lastCert = &m
	v.advance(view)
}

func (v *Voter) onFinalization(c certified) {
	view := c.proposal.Round.View
	s := v.state(view)
	if s.finalization != nil || v.finalizedBefore(view) {
		return
	}
	s.finalization = &c
	m := Message{Kind: MsgFinalization, Proposal: c.proposal, Certificate: c.cert}
	v.broadcast(m)
	v.lastFinal = &m
	v.finalized = view
	v.out.Finalized = append(v.out.Finalized, Finalized{Proposal: c.proposal, Certificate: c.cert})
	v.advance(view)
	v.prune()
}

func (v *Voter) timedOut(s *viewState) bool {
	if v.now >= v.advanceAt {
		return true
	}
	return v.now >= v.leaderAt && !s.verified
}

func (v *Voter) Tick(now Millis) Output {
	v.now = now
	s := v.state(v.view)
	if !s.sentNullify {
		if v.timedOut(s) {
			v.nullify(s)
		}
		return v.drain()
	}
	if now >= v.retryAt {
		v.retryAt = now + v.cfg.Timeouts.Retry
		v.broadcast(Message{Kind: MsgVote, Vote: s.nullifyVote})
		v.rebroadcastCertificates()
	}
	v.push(task{kind: taskPropose})
	return v.drain()
}

func (v *Voter) nullify(s *viewState) {
	s.sentNullify = true
	s.nullifyVote = v.cfg.Signer.Sign(KindNullify, Proposal{Round: v.round(v.view)})
	v.retryAt = v.now + v.cfg.Timeouts.Retry
	v.broadcast(Message{Kind: MsgVote, Vote: s.nullifyVote})
	v.push(task{kind: taskVote, vote: s.nullifyVote})
}
