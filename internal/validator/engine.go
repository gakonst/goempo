package validator

import (
	"errors"
	"io"
	"maps"
	"slices"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	blocksMax      = 8192
	finalsKept     = 1024
	futureMax      = 1024
	requestEvery   = 500
	backfillAfter  = 3000
	settleMax      = 64
	pendingWalkMax = 4096
	RecoverMax     = 1 << 20
)

const _ = uint(pendingWalkMax - settleMax)

const _ = uint(RecoverMax - blocksMax)

const _ = uint(blocksMax - pendingWalkMax)

const _ = uint(futureMax - finalsKept)

var (
	ErrNoOutcome   = errors.New("validator: boundary block has no DKG outcome")
	ErrShareKey    = errors.New("validator: share does not match the epoch's public polynomial")
	ErrBoundary    = errors.New("validator: finalized boundary outcome differs from ours")
	ErrChainLinked = errors.New("validator: finalized block does not extend the head")
)

type PeerKey = dkg.PublicKey

type Transport interface {
	Send(channel uint64, to []PeerKey, msg []byte)
}

type Attributes struct {
	TimestampMillis uint64
	ExtraData       []byte
	Consensus       types.ConsensusContext
}

type OutcomeInputs struct {
	NextPlayers dkg.KeySet
	FullDKG     bool
}

type Chain interface {
	Head() *types.Block
	BlockByNumber(number uint64) (*types.Block, bool)
	Build(parent *types.Block, attrs Attributes) (*types.Block, error)
	Execute(block *types.Block) error
	Commit(block *types.Block) error
	OutcomeInputs() (OutcomeInputs, error)
}

type Rules struct {
	Elector consensus.ElectorVersion
	Reveal  byte
}

type Config struct {
	Key       SigningKey
	Share     *dkg.Share
	Epocher   consensus.Epocher
	Timeouts  consensus.Timeouts
	Chain     Chain
	Net       Transport
	Rand      io.Reader
	WallClock func() uint64
	RulesAt   func(timestampSeconds uint64) Rules
	SaveShare func(epoch uint64, s dkg.Share) error
}

type Stats struct {
	Proposed     uint64
	Committed    uint64
	Epochs       uint64
	Dealt        uint64
	Acks         uint64
	MissedShares uint64
}

type Engine struct {
	cfg       Config
	me        PeerKey
	ns        consensus.Namespace
	now       consensus.Millis
	blocks    map[consensus.Digest]*types.Block
	order     []consensus.Digest
	pending   []consensus.Digest
	finalOf   map[consensus.Digest]consensus.Finalized
	finals    map[uint64]consensus.Finalized
	requested map[consensus.Digest]consensus.Millis
	asked     map[uint64]consensus.Digest
	backfill  map[uint64]uint64
	progress  consensus.Millis
	askedAt   consensus.Millis
	verdicts  map[consensus.Digest]consensus.Verdict
	nextID    uint64
	cur       *epochState
	prev      *epochState
	future    [][]byte
	replay    [][]byte
	events    uint64
	err       error
	stats     Stats
}

type epochState struct {
	epoch        uint64
	outcome      dkg.Outcome
	boundary     *types.Block
	participants dkg.KeySet
	identity     consensus.Identity
	voter        *consensus.Voter
	share        *dkg.Share
	rules        Rules
	round        *ceremony
}

func New(cfg Config) (*Engine, error) {
	e := &Engine{
		cfg: cfg, me: dkg.KeyOf(cfg.Key.Private()), ns: consensus.NewNamespace(consensus.TempoNamespace),
		blocks: map[consensus.Digest]*types.Block{}, finalOf: map[consensus.Digest]consensus.Finalized{},
		finals: map[uint64]consensus.Finalized{}, requested: map[consensus.Digest]consensus.Millis{},
		asked: map[uint64]consensus.Digest{}, backfill: map[uint64]uint64{}, verdicts: map[consensus.Digest]consensus.Verdict{},
	}
	head := cfg.Chain.Head()
	epoch := cfg.Epocher.Epoch(head.Header.Inner.Number + 1)
	boundaryHeight := uint64(0)
	if epoch > 0 {
		boundaryHeight = cfg.Epocher.Last(epoch - 1)
	}
	boundary, ok := cfg.Chain.BlockByNumber(boundaryHeight)
	if !ok {
		return nil, ErrNoOutcome
	}
	outcome, err := dkg.DecodeOutcome(boundary.Header.Inner.ExtraData)
	if err != nil {
		return nil, ErrNoOutcome
	}
	st, err := e.newEpoch(epoch, outcome, boundary, cfg.Share)
	if err != nil {
		return nil, err
	}
	e.cur = st
	return e, nil
}

func (e *Engine) Err() error { return e.err }

func (e *Engine) Stats() Stats { return e.stats }

func (e *Engine) Epoch() uint64 { return e.cur.epoch }

func (e *Engine) Share() *dkg.Share { return e.cur.share }

func (e *Engine) Member() bool { return e.cur.voter != nil }

func (e *Engine) newEpoch(epoch uint64, outcome dkg.Outcome, boundary *types.Block, share *dkg.Share) (*epochState, error) {
	st := &epochState{
		epoch: epoch, outcome: outcome, boundary: boundary, participants: outcome.Output.Players,
		identity: consensus.NewIdentity(outcome.Output.Public.Poly.Constant()),
		rules:    e.cfg.RulesAt(boundary.Header.Inner.Timestamp),
	}
	index, member := st.participants.Index(e.me)
	if share != nil && member {
		if share.Index != index || !share.Public().Equal(outcome.Output.Public.Poly.Eval(index)) {
			return nil, ErrShareKey
		}
		s := *share
		st.share = &s
	}
	round, err := newCeremony(e, st)
	if err != nil {
		return nil, err
	}
	st.round = round
	if st.share == nil {
		return st, nil
	}
	pubs := make([]bls12381.G2, len(st.participants))
	for i := range pubs {
		pubs[i] = outcome.Output.Public.Poly.Eval(i)
	}
	st.voter = consensus.NewVoter(consensus.VoterConfig{
		Epoch: epoch, Signer: consensus.NewSigner(*st.share, e.ns), Identity: st.identity, Publics: pubs,
		Namespace: e.ns, Genesis: consensus.Digest(boundary.Hash()), Timeouts: e.cfg.Timeouts,
		Elector: st.rules.Elector, App: &app{e: e, st: st},
	})
	return st, nil
}

func (e *Engine) Start(now consensus.Millis) {
	e.now = now
	e.enterEpoch()
	e.settle()
}

func (e *Engine) enterEpoch() {
	st := e.cur
	e.stats.Epochs++
	st.round.start()
	if st.voter != nil {
		e.emit(st, st.voter.Start(e.now))
	}
	e.replay = append(e.replay, e.future...)
	e.future = nil
}

func (e *Engine) Tick(now consensus.Millis) {
	e.now = now
	if e.err != nil {
		return
	}
	if e.cur.voter != nil {
		e.emit(e.cur, e.cur.voter.Tick(now))
	}
	e.retryRequests()
	e.requestBackfill()
	e.settle()
}

func (e *Engine) requestBackfill() {
	if e.now < e.progress+backfillAfter || e.now < e.askedAt+requestEvery {
		return
	}
	e.askedAt = e.now
	clear(e.backfill)
	height := e.cfg.Chain.Head().Header.Inner.Number + 1
	e.nextID++
	e.backfill[e.nextID] = height
	q := wire.MarshalRequest{ID: e.nextID, Key: wire.MarshalKey{Kind: wire.KeyFinalized, Height: height}}
	e.cfg.Net.Send(wire.ChannelMarshal, nil, q.Encode())
}

func (e *Engine) onBackfill(height uint64, body []byte) {
	if height != e.cfg.Chain.Head().Header.Inner.Number+1 {
		return
	}
	cb, err := wire.DecodeCertifiedBlock(body, consensus.KindFinalize)
	if err != nil {
		return
	}
	b, err := types.DecodeBlock(cb.Block)
	if err != nil {
		return
	}
	f := consensus.Finalization{Proposal: cb.Certified.Proposal, Certificate: cb.Certified.Certificate}
	if b.Header.Inner.Number != height || consensus.Digest(b.Hash()) != f.Proposal.Payload {
		return
	}
	if f.Proposal.Round.Epoch != e.cur.epoch || consensus.VerifyFinalization(e.cur.identity, e.ns, f) != nil {
		return
	}
	e.store(&b)
	e.onFinalized(consensus.Finalized{Proposal: f.Proposal, Certificate: f.Certificate})
	e.drain()
	e.askedAt = 0
	e.progress = 0
	e.requestBackfill()
}

func (e *Engine) Deadline() consensus.Millis {
	d := e.now + requestEvery
	if e.cur.voter != nil {
		d = min(d, e.cur.voter.Deadline())
	}
	return max(d, e.now+1)
}

func (e *Engine) settle() {
	for range settleMax {
		if e.err != nil {
			return
		}
		before := e.events
		e.drain()
		replay := e.replay
		e.replay = nil
		for _, f := range replay {
			e.receiveCert(f)
		}
		if e.cur.voter != nil {
			e.emit(e.cur, e.cur.voter.Reevaluate(e.now))
		}
		if e.events == before {
			return
		}
	}
}

func (e *Engine) emit(st *epochState, o consensus.Output) {
	for _, m := range o.Broadcast {
		e.send(st.epoch, m)
	}
	if st != e.cur {
		return
	}
	for _, f := range o.Finalized {
		e.onFinalized(f)
	}
}

func (e *Engine) send(epoch uint64, m consensus.Message) {
	switch m.Kind {
	case consensus.MsgVote:
		e.cfg.Net.Send(wire.ChannelVotes, nil, wire.EncodeVote(epoch, m.Vote))
	case consensus.MsgNotarization:
		e.cfg.Net.Send(wire.ChannelCertificates, nil, wire.EncodeCertificate(epoch, consensus.KindNotarize, m.Proposal, m.Certificate))
	case consensus.MsgNullification:
		e.cfg.Net.Send(wire.ChannelCertificates, nil, wire.EncodeCertificate(epoch, consensus.KindNullify, m.Proposal, m.Certificate))
	case consensus.MsgFinalization:
		e.cfg.Net.Send(wire.ChannelCertificates, nil, wire.EncodeCertificate(epoch, consensus.KindFinalize, m.Proposal, m.Certificate))
	default:
		assert.Unreachable("MessageKind: unhandled value")
	}
}

func (e *Engine) Receive(now consensus.Millis, from PeerKey, channel uint64, msg []byte) {
	e.now = now
	if e.err != nil {
		return
	}
	switch channel {
	case wire.ChannelVotes:
		e.receiveVote(from, msg)
	case wire.ChannelCertificates:
		e.receiveCert(msg)
	case wire.ChannelBroadcast:
		e.receiveBlock(msg)
	case wire.ChannelMarshal:
		e.receiveMarshal(from, msg)
	case wire.ChannelDKG:
		e.cur.round.receive(from, msg)
	default:
		return
	}
	e.settle()
}

func (e *Engine) stateFor(epoch uint64) *epochState {
	if epoch == e.cur.epoch {
		return e.cur
	}
	if e.prev != nil && epoch == e.prev.epoch {
		return e.prev
	}
	return nil
}

func (e *Engine) receiveVote(from PeerKey, msg []byte) {
	ev, err := wire.DecodeVote(msg)
	if err != nil {
		return
	}
	st := e.stateFor(ev.Epoch)
	if st == nil || st.voter == nil {
		return
	}
	index, ok := st.participants.Index(from)
	if !ok || index != ev.Vote.Signer {
		return
	}
	e.emit(st, st.voter.Handle(e.now, index, consensus.Message{Kind: consensus.MsgVote, Vote: ev.Vote}))
}

func certMessage(c wire.Certified) consensus.Message {
	m := consensus.Message{Kind: consensus.MsgNotarization, Proposal: c.Proposal, Certificate: c.Certificate}
	if c.Kind == consensus.KindNullify {
		m.Kind = consensus.MsgNullification
	}
	if c.Kind == consensus.KindFinalize {
		m.Kind = consensus.MsgFinalization
	}
	return m
}

func (e *Engine) receiveCert(msg []byte) {
	ec, err := wire.DecodeCertificate(msg)
	if err != nil {
		return
	}
	if ec.Epoch > e.cur.epoch {
		if len(e.future) < futureMax {
			e.future = append(e.future, msg)
		}
		return
	}
	st := e.stateFor(ec.Epoch)
	if st == nil {
		return
	}
	if st.voter != nil {
		e.emit(st, st.voter.Handle(e.now, consensus.NoLeader, certMessage(ec.Certified)))
		return
	}
	if st != e.cur || ec.Certified.Kind != consensus.KindFinalize {
		return
	}
	f := consensus.Finalization{Proposal: ec.Certified.Proposal, Certificate: ec.Certified.Certificate}
	if consensus.VerifyFinalization(st.identity, e.ns, f) != nil {
		return
	}
	e.onFinalized(consensus.Finalized{Proposal: f.Proposal, Certificate: f.Certificate})
}

func (e *Engine) store(b *types.Block) consensus.Digest {
	d := consensus.Digest(b.Hash())
	if _, ok := e.blocks[d]; ok {
		return d
	}
	if len(e.blocks) >= blocksMax {
		e.prune()
	}
	if len(e.blocks) < blocksMax {
		e.blocks[d] = b
		e.order = append(e.order, d)
	}
	delete(e.requested, d)
	return d
}

func (e *Engine) prune() {
	head := e.cfg.Chain.Head().Header.Inner.Number
	kept := e.order[:0]
	for _, d := range e.order {
		b, ok := e.blocks[d]
		if !ok {
			continue
		}
		if b.Header.Inner.Number > head {
			kept = append(kept, d)
			continue
		}
		delete(e.blocks, d)
		delete(e.verdicts, d)
	}
	e.order = kept
}

func (e *Engine) receiveBlock(msg []byte) {
	raw, err := wire.DecodeBlock(msg)
	if err != nil {
		return
	}
	b, err := types.DecodeBlock(raw)
	if err != nil {
		return
	}
	e.acceptBlock(&b)
}

func (e *Engine) acceptBlock(b *types.Block) {
	d := e.store(b)
	e.events++
	if f, ok := e.finalOf[d]; ok {
		delete(e.finalOf, d)
		e.onFinalized(f)
	}
}

func (e *Engine) block(d consensus.Digest) (*types.Block, bool) {
	if b, ok := e.blocks[d]; ok {
		return b, true
	}
	head := e.cfg.Chain.Head()
	if consensus.Digest(head.Hash()) == d {
		return head, true
	}
	if consensus.Digest(e.cur.boundary.Hash()) == d {
		return e.cur.boundary, true
	}
	return nil, false
}

func (e *Engine) onFinalized(f consensus.Finalized) {
	d := f.Proposal.Payload
	b, ok := e.block(d)
	if !ok {
		if len(e.finalOf) < futureMax {
			e.finalOf[d] = f
		}
		e.request(d)
		return
	}
	n := b.Header.Inner.Number
	if n <= e.cfg.Chain.Head().Header.Inner.Number {
		return
	}
	if _, ok := e.finals[n]; !ok {
		e.finals[n] = f
		e.events++
	}
}

func (e *Engine) drain() {
	for range pendingWalkMax {
		if e.err != nil {
			return
		}
		top, ok := e.topFinal()
		if !ok {
			return
		}
		next, ok := e.nextToCommit(top)
		if !ok {
			return
		}
		e.commit(next)
	}
}

func (e *Engine) topFinal() (consensus.Finalized, bool) {
	heights := slices.Sorted(maps.Keys(e.finals))
	if len(heights) == 0 {
		return consensus.Finalized{}, false
	}
	top := heights[len(heights)-1]
	if top <= e.cfg.Chain.Head().Header.Inner.Number {
		return consensus.Finalized{}, false
	}
	return e.finals[top], true
}

func (e *Engine) nextToCommit(top consensus.Finalized) (*types.Block, bool) {
	head := e.cfg.Chain.Head()
	want := head.Header.Inner.Number + 1
	if f, ok := e.finals[want]; ok {
		if b, ok := e.block(f.Proposal.Payload); ok && b.Header.Inner.ParentHash == head.Hash() {
			return b, true
		}
	}
	b, ok := e.block(top.Proposal.Payload)
	for range pendingWalkMax {
		if !ok {
			return nil, false
		}
		if b.Header.Inner.Number == want {
			if b.Header.Inner.ParentHash != head.Hash() {
				e.err = ErrChainLinked
				return nil, false
			}
			return b, true
		}
		parent := consensus.Digest(b.Header.Inner.ParentHash)
		b, ok = e.block(parent)
		if !ok {
			e.request(parent)
		}
	}
	return nil, false
}

func (e *Engine) commit(b *types.Block) {
	st := e.cur
	n := b.Header.Inner.Number
	boundary := e.cfg.Epocher.IsLast(n)
	if boundary {
		r, err := st.round.outcome()
		if err != nil {
			e.err = err
			return
		}
		if string(r.encoded) != string(b.Header.Inner.ExtraData) {
			e.err = ErrBoundary
			return
		}
	}
	if err := e.cfg.Chain.Commit(b); err != nil {
		e.err = err
		return
	}
	e.stats.Committed++
	e.events++
	e.progress = e.now
	if !boundary {
		e.prev = nil
	}
	delete(e.blocks, consensus.Digest(b.Hash()))
	e.pruneFinals(n)
	st.round.onFinalized(b)
	if boundary {
		e.transition(b)
	}
}

func (e *Engine) pruneFinals(n uint64) {
	for _, h := range slices.Sorted(maps.Keys(e.finals)) {
		if h+finalsKept < n {
			delete(e.finals, h)
		}
	}
}

func (e *Engine) transition(boundary *types.Block) {
	st := e.cur
	r, err := st.round.outcome()
	if err != nil {
		e.err = err
		return
	}
	share, err := st.round.nextShare(r)
	if err != nil {
		e.err = err
		return
	}
	if share != nil && e.cfg.SaveShare != nil {
		if err := e.cfg.SaveShare(st.epoch+1, *share); err != nil {
			e.err = err
			return
		}
	}
	next, err := e.newEpoch(st.epoch+1, r.value, boundary, share)
	if err != nil {
		e.err = err
		return
	}
	e.prev = st
	e.cur = next
	e.enterEpoch()
}

func (e *Engine) request(d consensus.Digest) {
	if at, ok := e.requested[d]; ok && e.now < at+requestEvery {
		return
	}
	if _, ok := e.requested[d]; !ok {
		e.pending = append(e.pending, d)
	}
	e.requested[d] = e.now
	e.nextID++
	e.asked[e.nextID] = d
	q := wire.MarshalRequest{ID: e.nextID, Key: wire.MarshalKey{Kind: wire.KeyBlock, Digest: d}}
	e.cfg.Net.Send(wire.ChannelMarshal, nil, q.Encode())
}

func (e *Engine) retryRequests() {
	kept := e.pending[:0]
	for _, d := range e.pending {
		at, ok := e.requested[d]
		if !ok {
			continue
		}
		kept = append(kept, d)
		if e.now >= at+requestEvery {
			e.request(d)
		}
	}
	e.pending = kept
	for _, id := range slices.Sorted(maps.Keys(e.asked)) {
		if _, ok := e.requested[e.asked[id]]; !ok {
			delete(e.asked, id)
		}
	}
}

func (e *Engine) receiveMarshal(from PeerKey, msg []byte) {
	f, err := wire.DecodeFrame(msg)
	if err != nil {
		return
	}
	switch f.Kind {
	case wire.FrameRequest:
		e.serve(from, f)
	case wire.FrameResponse:
		e.onResponse(f)
	case wire.FrameError:
		delete(e.asked, f.ID)
		delete(e.backfill, f.ID)
	default:
		assert.Unreachable("FrameKind: unhandled value")
	}
}

func (e *Engine) serve(from PeerKey, f wire.Frame) {
	key, err := f.MarshalKey()
	if err != nil {
		return
	}
	data, ok := e.lookup(key)
	if !ok {
		e.cfg.Net.Send(wire.ChannelMarshal, []PeerKey{from}, wire.MarshalError{ID: f.ID}.Encode())
		return
	}
	e.cfg.Net.Send(wire.ChannelMarshal, []PeerKey{from}, wire.MarshalResponse{ID: f.ID, Data: data}.Encode())
}

func (e *Engine) lookup(key wire.MarshalKey) ([]byte, bool) {
	switch key.Kind {
	case wire.KeyBlock:
		b, ok := e.block(key.Digest)
		if !ok {
			return nil, false
		}
		return b.Encode(), true
	case wire.KeyFinalized:
		f, ok := e.finals[key.Height]
		if !ok {
			return nil, false
		}
		b, ok := e.cfg.Chain.BlockByNumber(key.Height)
		if !ok {
			return nil, false
		}
		c := wire.CertifiedBlock{Certified: wire.Certified{Kind: consensus.KindFinalize, Proposal: f.Proposal, Certificate: f.Certificate}, Block: b.Encode()}
		return c.Encode(), true
	case wire.KeyNotarized:
		return nil, false
	default:
		assert.Unreachable("MarshalKeyKind: unhandled value")
	}
	return nil, false
}

func (e *Engine) onResponse(f wire.Frame) {
	if h, ok := e.backfill[f.ID]; ok {
		delete(e.backfill, f.ID)
		e.onBackfill(h, f.Body)
		return
	}
	d, ok := e.asked[f.ID]
	if !ok {
		return
	}
	delete(e.asked, f.ID)
	b, err := types.DecodeBlock(f.Body)
	if err != nil {
		return
	}
	if consensus.Digest(b.Hash()) != d {
		return
	}
	e.acceptBlock(&b)
}

func (e *Engine) proposer(st *epochState, leader int) [types.ProposerKeyLength]byte {
	if leader < 0 || leader >= len(st.participants) {
		return [types.ProposerKeyLength]byte{}
	}
	return [types.ProposerKeyLength]byte(st.participants[leader])
}

func (e *Engine) timestampFor(parent *types.Block) uint64 {
	return max(e.cfg.WallClock(), parent.Header.TimestampMillis()+1)
}
