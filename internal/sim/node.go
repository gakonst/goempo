package sim

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

const (
	gasLimit        = 30_000_000
	genesisSeconds  = 1_700_000_000
	millisPerSecond = 1000
	pendingMax      = 4096
	waitingMax      = 4096
	helpEvery       = 500
	requestEvery    = 300
	syncBatch       = 32
)

const _ = uint(waitingMax - syncBatch)

const _ = uint(pendingMax - waitingMax)

var (
	errOutcome = errors.New("sim: boundary outcome differs from the ceremony")
	errSafety  = errors.New("sim: finalized a block that conflicts with the finalized chain")
)

type block struct {
	raw    []byte
	blk    types.Block
	digest consensus.Digest
}

func newBlock(b types.Block) *block {
	return &block{raw: b.Encode(), blk: b, digest: consensus.Digest(b.Hash())}
}

func (b *block) number() uint64 { return b.blk.Header.Inner.Number }

func (b *block) parent() consensus.Digest { return consensus.Digest(b.blk.Header.Inner.ParentHash) }

func (b *block) extra() []byte { return b.blk.Header.Inner.ExtraData }

type deal struct {
	dealer     dkg.PublicKey
	commitment dkg.Commitment
	share      bls12381.Scalar
}

type ack struct {
	player dkg.PublicKey
	sig    [dkg.SignatureLen]byte
}

type envelope struct {
	from      int
	to        int
	epoch     uint64
	msg       *consensus.Message
	block     *block
	deal      *deal
	ack       *ack
	log       *dkg.SignedDealerLog
	request   *consensus.Digest
	ancestors []*block
}

type node struct {
	index       int
	key         ed25519.PrivateKey
	players     dkg.KeySet
	epocher     consensus.Epocher
	ns          consensus.Namespace
	timeouts    consensus.Timeouts
	now         consensus.Millis
	blocks      map[consensus.Digest]*block
	finalized   []consensus.Digest
	certs       map[uint64][]byte
	epoch       uint64
	voter       *consensus.Voter
	output      dkg.Output
	share       dkg.Share
	info        *dkg.Info
	dealer      *dkg.Dealer
	player      *dkg.Player
	acks        int
	acked       map[dkg.PublicKey]bool
	myLog       *dkg.SignedDealerLog
	logIncluded bool
	logs        map[dkg.PublicKey]dkg.SignedDealerLog
	waiting     []consensus.Finalized
	future      []envelope
	boundaries  map[uint64]envelope
	helpedAt    map[int]consensus.Millis
	requested   map[consensus.Digest]consensus.Millis
	outbox      []envelope
	err         error
	failures    int
	inbox       []envelope
	startAt     *block
}

func (n *node) me() dkg.PublicKey { return n.players[n.index] }

func (n *node) head() uint64 { return uint64(len(n.finalized)) - 1 }

func (n *node) send(e envelope) {
	e.from = n.index
	e.epoch = n.epoch
	n.outbox = append(n.outbox, e)
}

func (n *node) broadcast(e envelope) {
	for to := range n.players {
		if to == n.index {
			continue
		}
		e.to = to
		n.send(e)
	}
}

func (n *node) store(b *block) bool {
	if _, ok := n.blocks[b.digest]; ok {
		return false
	}
	n.blocks[b.digest] = b
	return true
}

func (n *node) startEpoch(boundary *block) {
	pubs := make([]bls12381.G2, len(n.players))
	for i := range pubs {
		pubs[i] = n.output.Public.Poly.Eval(i)
	}
	n.voter = consensus.NewVoter(consensus.VoterConfig{
		Epoch: n.epoch, Signer: consensus.NewSigner(n.share, n.ns),
		Identity: consensus.NewIdentity(n.output.Public.Poly.Constant()), Publics: pubs,
		Namespace: n.ns, Genesis: boundary.digest, Timeouts: n.timeouts, App: n,
	})
	n.startCeremony()
	n.emit(n.voter.Start(n.now))
	n.inbox = append(n.inbox, n.future...)
	n.future = nil
}

func (n *node) step(e *envelope) {
	if e == nil {
		n.tick(n.now)
	} else {
		n.receive(*e)
	}
	for range pendingMax + 1 {
		if n.err != nil {
			return
		}
		if n.startAt != nil {
			b := n.startAt
			n.startAt = nil
			n.startEpoch(b)
			continue
		}
		if len(n.inbox) == 0 {
			return
		}
		next := n.inbox[0]
		n.inbox = n.inbox[1:]
		n.receive(next)
	}
}

func seededReader(label string, pub dkg.PublicKey, epoch uint64) *dkg.Noise {
	t := dkg.NewTranscript([]byte("goempo-sim"))
	t.Commit(pub[:])
	t.Commit(binary.BigEndian.AppendUint64(nil, epoch))
	return t.Noise([]byte(label))
}

func (n *node) startCeremony() {
	info, err := dkg.NewInfo([]byte(dkg.TempoNamespace), dkg.RoundConfig{
		Round: n.epoch, Previous: &n.output, Reveal: dkg.RevealV1, Dealers: n.players, Players: n.players,
	})
	if err != nil {
		n.err = err
		return
	}
	n.info = info
	n.logs = map[dkg.PublicKey]dkg.SignedDealerLog{}
	n.acks = 0
	n.acked = map[dkg.PublicKey]bool{}
	n.myLog = nil
	n.logIncluded = false
	prev := n.share
	n.dealer, err = dkg.NewDealer(info, dkg.DealerConfig{Key: n.key, Previous: &prev, Rand: seededReader("dealer", n.me(), n.epoch)})
	if err != nil {
		n.err = err
		return
	}
	if n.player, err = dkg.NewPlayer(info, n.key); err != nil {
		n.err = err
		return
	}
	n.resendDeals()
}

func (n *node) resendDeals() {
	if n.dealer == nil || n.myLog != nil {
		return
	}
	for to, p := range n.players {
		if n.acked[p] {
			continue
		}
		d := &deal{dealer: n.me(), commitment: n.dealer.Commitment(), share: n.dealer.ShareFor(to)}
		if to == n.index {
			n.onDeal(*d)
			continue
		}
		n.send(envelope{to: to, deal: d})
	}
}

func (n *node) onDeal(d deal) {
	sig, err := n.player.Receive(d.dealer, d.commitment, d.share)
	if err != nil {
		return
	}
	a := ack{player: n.me(), sig: sig}
	if d.dealer == n.me() {
		n.onAck(a)
		return
	}
	if to, ok := n.players.Index(d.dealer); ok {
		n.send(envelope{to: to, ack: &a})
	}
}

func (n *node) onAck(a ack) {
	if n.myLog == nil && !n.acked[a.player] && n.dealer.Ack(a.player, a.sig) == nil {
		n.acked[a.player] = true
		n.acks++
	}
	n.maybeFinalizeLog()
}

func (n *node) maybeFinalizeLog() {
	if n.myLog != nil || n.dealer == nil {
		return
	}
	quarter := n.epocher.First(n.epoch) + n.epocher.Length/4
	if n.acks < len(n.players) && n.head() < quarter {
		return
	}
	l := n.dealer.Finalize()
	n.myLog = &l
	n.logs[n.me()] = l
	n.broadcast(envelope{log: &l})
}

func (n *node) onLog(l dkg.SignedDealerLog) {
	if _, ok := n.info.Dealers.Index(l.Dealer); !ok || !n.info.CheckSignedDealerLog(l) {
		return
	}
	if _, ok := n.logs[l.Dealer]; !ok {
		n.logs[l.Dealer] = l
	}
}

func (n *node) emit(o consensus.Output) {
	for i := range o.Broadcast {
		m := o.Broadcast[i]
		e := envelope{msg: &m}
		if m.Kind != consensus.MsgVote && m.Kind != consensus.MsgNullification {
			e.block = n.blocks[m.Proposal.Payload]
		}
		n.broadcast(e)
	}
	if len(n.waiting)+len(o.Finalized) <= waitingMax {
		n.waiting = append(n.waiting, o.Finalized...)
	}
	n.settle()
}

func (n *node) epochStart() uint64 {
	if n.epoch == 0 {
		return 1
	}
	return n.epocher.First(n.epoch)
}

type pathLogs struct {
	entries []dkg.DealerEntry
	dealers map[dkg.PublicKey]bool
}

func (n *node) logsOnPath(from *block) (pathLogs, bool) {
	out := pathLogs{dealers: map[dkg.PublicKey]bool{}}
	b := from
	for range n.epocher.Length + 1 {
		if b.number() < n.epochStart() {
			sortEntries(out.entries)
			return out, true
		}
		if len(b.extra()) > 0 {
			l, err := dkg.DecodeSignedDealerLog(b.extra())
			if err == nil && !out.dealers[l.Dealer] {
				out.dealers[l.Dealer] = true
				out.entries = append(out.entries, dkg.DealerEntry{Dealer: l.Dealer, Log: l.Log})
			}
		}
		next, ok := n.blocks[b.parent()]
		if !ok {
			return pathLogs{}, false
		}
		b = next
	}
	return pathLogs{}, false
}

func sortEntries(e []dkg.DealerEntry) {
	keys := make([]dkg.PublicKey, len(e))
	for i := range e {
		keys[i] = e[i].Dealer
	}
	sorted := dkg.NewKeySet(keys)
	out := make([]dkg.DealerEntry, len(e))
	for _, x := range e {
		i, _ := sorted.Index(x.Dealer)
		out[i] = x
	}
	copy(e, out)
}

type reckoned struct {
	logs      pathLogs
	selection []int
	output    dkg.Output
	encoded   []byte
	failed    bool
}

func (n *node) reckon(parent *block) (reckoned, bool) {
	logs, ok := n.logsOnPath(parent)
	if !ok {
		return reckoned{}, false
	}
	sel, err := n.info.Select(logs.entries)
	if err != nil {
		o := dkg.Outcome{Epoch: n.epoch + 1, Output: n.output, NextPlayers: n.players}
		return reckoned{logs: logs, output: n.output, encoded: o.Encode(), failed: true}, true
	}
	out, err := n.info.Reckon(logs.entries, sel)
	if err != nil {
		return reckoned{}, false
	}
	o := dkg.Outcome{Epoch: n.epoch + 1, Output: out, NextPlayers: n.players}
	return reckoned{logs: logs, selection: sel, output: out, encoded: o.Encode()}, true
}

func (n *node) extraFor(height uint64, parent *block) ([]byte, bool) {
	if height == n.epocher.Last(n.epoch) {
		r, ok := n.reckon(parent)
		return r.encoded, ok
	}
	logs, ok := n.logsOnPath(parent)
	if !ok {
		return nil, false
	}
	if n.myLog != nil && !logs.dealers[n.me()] {
		return n.myLog.Encode(), true
	}
	for _, p := range n.players {
		if l, ok := n.logs[p]; ok && !logs.dealers[p] {
			return l.Encode(), true
		}
	}
	return nil, true
}

func (n *node) Propose(ctx consensus.ProposeContext) (consensus.Digest, bool) {
	parent, ok := n.blocks[ctx.ParentDigest]
	if !ok {
		return consensus.Digest{}, false
	}
	if parent.number() == n.epocher.Last(n.epoch) {
		return ctx.ParentDigest, true
	}
	height := parent.number() + 1
	extra, ok := n.extraFor(height, parent)
	if !ok {
		return consensus.Digest{}, false
	}
	h := types.Header{
		GeneralGasLimit: gasLimit, TimestampMillisPart: uint64(n.now % millisPerSecond),
		Inner: types.EthHeader{
			ParentHash: primitives.Hash(ctx.ParentDigest), StateRoot: parent.blk.Header.Inner.StateRoot,
			Number: height, GasLimit: gasLimit, Timestamp: genesisSeconds + uint64(n.now/millisPerSecond),
			ExtraData: extra, OptionalFields: parent.blk.Header.Inner.OptionalFields,
		},
		Consensus: &types.ConsensusContext{Epoch: n.epoch, View: ctx.Round.View, ParentView: ctx.Parent, Proposer: n.players[ctx.Leader]},
	}
	b := newBlock(types.Block{Header: h, HasWithdrawals: parent.blk.HasWithdrawals})
	n.store(b)
	return b.digest, true
}

func (n *node) Verify(ctx consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	b, ok := n.blocks[payload]
	if !ok {
		n.request(payload)
		return consensus.Undecided
	}
	parent, ok := n.blocks[ctx.ParentDigest]
	if !ok {
		n.request(ctx.ParentDigest)
		return consensus.Undecided
	}
	if payload == ctx.ParentDigest {
		if parent.number() != n.epocher.Last(n.epoch) {
			return consensus.Reject
		}
		return consensus.Accept
	}
	c := b.blk.Header.Consensus
	if b.parent() != ctx.ParentDigest || b.number() != parent.number()+1 || b.number() > n.epocher.Last(n.epoch) || c == nil {
		return consensus.Reject
	}
	want := types.ConsensusContext{Epoch: n.epoch, View: ctx.Round.View, ParentView: ctx.Parent, Proposer: c.Proposer}
	if ctx.Leader != consensus.NoLeader {
		want.Proposer = n.players[ctx.Leader]
	}
	if *c != want {
		return consensus.Reject
	}
	if !n.verifyExtra(link{child: b, parent: parent}) {
		return consensus.Reject
	}
	return consensus.Accept
}

func (n *node) Certify(ctx consensus.ProposeContext, payload consensus.Digest) consensus.Verdict {
	return n.Verify(ctx, payload)
}

type link struct {
	child  *block
	parent *block
}

func (n *node) verifyExtra(l link) bool {
	b, parent := l.child, l.parent
	if b.number() == n.epocher.Last(n.epoch) {
		r, ok := n.reckon(parent)
		return ok && bytes.Equal(r.encoded, b.extra())
	}
	if len(b.extra()) == 0 {
		return true
	}
	signed, err := dkg.DecodeSignedDealerLog(b.extra())
	if err != nil || !n.info.CheckSignedDealerLog(signed) {
		return false
	}
	logs, ok := n.logsOnPath(parent)
	return ok && !logs.dealers[signed.Dealer]
}
