package validator_test

import (
	"errors"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/validator"
	"github.com/kapetan-io/tiger/assert"
)

const (
	netValidators  = 4
	netEpochLength = 20
	netEpochs      = 3
	netGasLimit    = 30_000_000
	genesisSeconds = 1_700_000_000
	millisPerSec   = 1000
	tickStep       = 25
	netStepsMax    = 400_000
	netSeed        = 7
	stepsPerBlock  = 4000
)

const _ = uint(netStepsMax - stepsPerBlock*(netEpochs*netEpochLength+2))

var errNotNext = errors.New("memchain: block does not extend the head")

type memChain struct {
	blocks  []*types.Block
	players dkg.KeySet
	execs   int
}

func (c *memChain) Head() *types.Block { return c.blocks[len(c.blocks)-1] }

func (c *memChain) BlockByNumber(n uint64) (*types.Block, bool) {
	if n >= uint64(len(c.blocks)) {
		return nil, false
	}
	return c.blocks[n], true
}

func (c *memChain) Build(on validator.Branch, attrs validator.Attributes) (*types.Block, error) {
	if err := c.linked(on); err != nil {
		return nil, err
	}
	parent := on.Parent
	ctx := attrs.Consensus
	h := types.Header{
		GeneralGasLimit: netGasLimit, TimestampMillisPart: attrs.TimestampMillis % millisPerSec,
		Inner: types.EthHeader{
			ParentHash: parent.Hash(), StateRoot: parent.Header.Inner.StateRoot, Number: parent.Header.Inner.Number + 1,
			GasLimit: netGasLimit, Timestamp: attrs.TimestampMillis / millisPerSec, ExtraData: attrs.ExtraData,
			OptionalFields: parent.Header.Inner.OptionalFields,
		},
		Consensus: &ctx,
	}
	return &types.Block{Header: h, HasWithdrawals: parent.HasWithdrawals}, nil
}

func (c *memChain) linked(on validator.Branch) error {
	parent := c.Head()
	for _, b := range on.Blocks {
		if b.Header.Inner.ParentHash != parent.Hash() {
			return errNotNext
		}
		parent = b
	}
	if parent.Hash() != on.Parent.Hash() {
		return errNotNext
	}
	return nil
}

func (c *memChain) Execute(on validator.Branch, b *types.Block) error {
	c.execs++
	if err := c.linked(on); err != nil {
		return err
	}
	if b.Header.Inner.ParentHash != on.Parent.Hash() {
		return errNotNext
	}
	return nil
}

func (c *memChain) Commit(b *types.Block) error {
	if err := c.Execute(validator.Branch{Parent: c.Head()}, b); err != nil {
		return err
	}
	c.blocks = append(c.blocks, b)
	return nil
}

func (c *memChain) OutcomeInputs(on validator.Branch) (validator.OutcomeInputs, error) {
	if err := c.linked(on); err != nil {
		return validator.OutcomeInputs{}, err
	}
	return validator.OutcomeInputs{NextPlayers: c.players}, nil
}

type packet struct {
	from    int
	to      []validator.PeerKey
	channel uint64
	msg     []byte
}

type memNet struct {
	from  int
	queue *[]packet
}

func (n memNet) Send(channel uint64, to []validator.PeerKey, msg []byte) {
	*n.queue = append(*n.queue, packet{from: n.from, to: to, channel: channel, msg: append([]byte{}, msg...)})
}

type cluster struct {
	keys    []validator.SigningKey
	pubs    []validator.PeerKey
	chains  []*memChain
	engines []*validator.Engine
	queue   []packet
	now     consensus.Millis
	down    map[int]bool
	drop    func(packet) bool
}

type genesis struct {
	block  *types.Block
	shares map[validator.PeerKey]dkg.Share
}

func genesisOf(keys []validator.SigningKey, players dkg.KeySet, rng *rand.ChaCha8) genesis {
	info, err := dkg.NewInfo([]byte(dkg.TempoNamespace), dkg.RoundConfig{Reveal: dkg.RevealV1, Dealers: players, Players: players})
	assert.NoError(err, "genesis info")
	ps := make([]*dkg.Player, len(keys))
	for i, k := range keys {
		ps[i], err = dkg.NewPlayer(info, k.Private())
		assert.NoError(err, "player")
	}
	entries := make([]dkg.DealerEntry, 0, len(keys))
	for _, pk := range players {
		var k validator.SigningKey
		for _, cand := range keys {
			if dkg.KeyOf(cand.Private()) == pk {
				k = cand
			}
		}
		d, err := dkg.NewDealer(info, dkg.DealerConfig{Key: k.Private(), Rand: rng})
		assert.NoError(err, "dealer")
		for _, p := range ps {
			sig, err := p.Receive(pk, d.Commitment(), d.ShareFor(p.Index()))
			assert.NoError(err, "receive")
			assert.NoError(d.Ack(players[p.Index()], sig), "ack")
		}
		entries = append(entries, dkg.DealerEntry{Dealer: pk, Log: d.Finalize().Log})
	}
	sel, err := info.Select(entries)
	assert.NoError(err, "select")
	out, err := info.Reckon(entries, sel)
	assert.NoError(err, "reckon")
	shares := map[validator.PeerKey]dkg.Share{}
	for _, p := range ps {
		s, err := p.Finalize(entries, sel)
		assert.NoError(err, "share")
		shares[players[s.Index]] = s
	}
	o := dkg.Outcome{Output: out, NextPlayers: players}
	b := &types.Block{Header: types.Header{GeneralGasLimit: netGasLimit, Inner: types.EthHeader{
		GasLimit: netGasLimit, Timestamp: genesisSeconds, ExtraData: o.Encode(),
	}}}
	return genesis{block: b, shares: shares}
}

func newCluster() *cluster {
	rng := rand.NewChaCha8([32]byte{netSeed})
	c := &cluster{down: map[int]bool{}}
	for range netValidators {
		k, err := validator.NewSigningKey(rng)
		assert.NoError(err, "key")
		c.keys = append(c.keys, k)
		c.pubs = append(c.pubs, dkg.KeyOf(k.Private()))
	}
	players := dkg.NewKeySet(c.pubs)
	g := genesisOf(c.keys, players, rng)
	for i, k := range c.keys {
		chain := &memChain{blocks: []*types.Block{g.block}, players: players}
		share := g.shares[c.pubs[i]]
		e, err := validator.New(validator.Config{
			Key: k, Share: &share, Epocher: consensus.Epocher{Length: netEpochLength},
			Timeouts: consensus.Timeouts{Leader: 1000, Notarization: 2000, Retry: 100},
			Chain:    chain, Net: memNet{from: i, queue: &c.queue}, Rand: rand.NewChaCha8([32]byte{byte(i), netSeed}),
			WallClock: func() uint64 { return genesisSeconds*millisPerSec + uint64(c.now) },
			RulesAt: func(uint64) validator.Rules {
				return validator.Rules{Elector: consensus.ElectorV1, Reveal: dkg.RevealV1}
			},
		})
		assert.NoError(err, "engine")
		c.chains = append(c.chains, chain)
		c.engines = append(c.engines, e)
	}
	for _, e := range c.engines {
		e.Start(c.now)
	}
	return c
}

func (c *cluster) index(k validator.PeerKey) int {
	for i, p := range c.pubs {
		if p == k {
			return i
		}
	}
	return -1
}

func (c *cluster) step() {
	if len(c.queue) == 0 {
		c.now += tickStep
		for i, e := range c.engines {
			if !c.down[i] {
				e.Tick(c.now)
			}
		}
		return
	}
	p := c.queue[0]
	c.queue = c.queue[1:]
	if c.down[p.from] {
		return
	}
	if c.drop != nil && c.drop(p) {
		return
	}
	targets := p.to
	if targets == nil {
		targets = c.pubs
	}
	for _, k := range targets {
		i := c.index(k)
		if i < 0 || i == p.from || c.down[i] {
			continue
		}
		c.engines[i].Receive(c.now, c.pubs[p.from], p.channel, p.msg)
	}
}

func (c *cluster) runUntil(done func() bool) {
	for range netStepsMax {
		for i, e := range c.engines {
			assert.NoError(e.Err(), "engine "+strconv.Itoa(i))
		}
		if done() {
			return
		}
		c.step()
	}
	assert.Fail("cluster stalled at heights %d %d %d %d", c.height(0), c.height(1), c.height(2), c.height(3))
}

func (c *cluster) height(i int) uint64 { return c.chains[i].Head().Header.Inner.Number }

func (c *cluster) allAt(h uint64) func() bool {
	return func() bool {
		for i := range c.engines {
			if !c.down[i] && c.height(i) < h {
				return false
			}
		}
		return true
	}
}

func TestEngineFinalizesAcrossDKGEpochs(_ *testing.T) {
	c := newCluster()
	target := uint64(netEpochs*netEpochLength + 2)
	c.runUntil(c.allAt(target))
	for i := range c.engines {
		for h := range target + 1 {
			a, _ := c.chains[0].BlockByNumber(h)
			b, _ := c.chains[i].BlockByNumber(h)
			assert.Equal(b.Hash(), a.Hash(), "every validator commits the same chain")
		}
		assert.Equal(c.engines[i].Epoch(), uint64(netEpochs), "validators entered every epoch")
		assert.Ok(c.engines[i].Member(), "still a voting member")
	}
	var proposed uint64
	for _, e := range c.engines {
		proposed += e.Stats().Proposed
	}
	assert.Ok(proposed >= target, "blocks were proposed")
	for _, e := range c.engines {
		assert.Ok(e.Stats().Acks > 0, "dealings were acknowledged over the DKG channel")
		assert.Equal(e.Stats().Epochs, uint64(netEpochs+1), "every epoch was entered")
	}
	for epoch := uint64(1); epoch <= netEpochs; epoch++ {
		b, _ := c.chains[0].BlockByNumber(epoch*netEpochLength - 1)
		o, err := dkg.DecodeOutcome(b.Header.Inner.ExtraData)
		assert.NoError(err, "boundary outcome")
		g, _ := c.chains[0].BlockByNumber(0)
		prev, err := dkg.DecodeOutcome(g.Header.Inner.ExtraData)
		assert.NoError(err, "genesis outcome")
		assert.Ok(o.Output.Public.Poly.Constant().Equal(prev.Output.Public.Poly.Constant()), "reshare keeps the network identity")
		assert.Equal(o.Epoch, epoch, "outcome epoch")
		assert.Ok(o.Output.Summary != prev.Output.Summary, "a fresh reshare, not a carried-over output")
	}
	for i := range c.engines {
		blk, _ := c.chains[i].BlockByNumber(1)
		assert.Ok(blk.Header.Consensus != nil, "blocks carry the consensus context")
	}
}

func TestEngineSurvivesOneValidatorDown(_ *testing.T) {
	c := newCluster()
	c.runUntil(c.allAt(3))
	c.down[netValidators-1] = true
	c.runUntil(c.allAt(netEpochLength + 3))
	assert.Ok(c.engines[0].Epoch() >= 1, "the epoch turned with a validator down")
}

func TestEngineBackfillsAfterDowntime(_ *testing.T) {
	c := newCluster()
	c.runUntil(c.allAt(3))
	last := netValidators - 1
	c.down[last] = true
	c.runUntil(c.allAt(netEpochLength + 5))
	behind := c.height(last)
	assert.Ok(behind < netEpochLength, "the stopped validator fell an epoch behind")
	c.down[last] = false
	c.runUntil(c.allAt(netEpochLength + 10))
	for h := range uint64(netEpochLength + 10) {
		a, _ := c.chains[0].BlockByNumber(h)
		b, _ := c.chains[last].BlockByNumber(h)
		assert.Equal(b.Hash(), a.Hash(), "backfilled chain matches")
	}
	assert.Ok(c.engines[last].Epoch() >= 1, "it crossed the epoch it missed")
}

func finalizeOf(r consensus.Round) func(packet) bool {
	return func(p packet) bool {
		if p.channel == wire.ChannelVotes {
			v, err := wire.DecodeVote(p.msg)
			return err == nil && v.Epoch == r.Epoch && v.Vote.Kind == consensus.KindFinalize && v.Vote.Proposal.Round.View == r.View
		}
		if p.channel == wire.ChannelCertificates {
			ce, err := wire.DecodeCertificate(p.msg)
			return err == nil && ce.Epoch == r.Epoch && ce.Certified.Kind == consensus.KindFinalize && ce.Certified.Proposal.Round.View == r.View
		}
		return false
	}
}

func TestEngineBuildsOnNotarizedUnfinalizedParent(_ *testing.T) {
	const stuckView = 5
	c := newCluster()
	c.drop = finalizeOf(consensus.Round{Epoch: 0, View: stuckView})
	c.runUntil(c.allAt(stuckView + 5))
	c.drop = finalizeOf(consensus.Round{Epoch: 1, View: stuckView})
	c.runUntil(c.allAt(netEpochLength + stuckView + 5))
	for i := range c.engines {
		for h := range uint64(netEpochLength + stuckView + 6) {
			a, _ := c.chains[0].BlockByNumber(h)
			b, _ := c.chains[i].BlockByNumber(h)
			assert.Equal(b.Hash(), a.Hash(), "every validator commits the same chain")
		}
	}
	assert.Ok(c.engines[0].Epoch() >= 1, "the epoch turned while a notarized block waited for finality")
}

type heightDrop struct {
	heights map[consensus.Digest]uint64
	from    uint64
	to      uint64
}

func (h *heightDrop) drop(p packet) bool {
	switch p.channel {
	case wire.ChannelBroadcast:
		raw, err := wire.DecodeBlock(p.msg)
		if err != nil {
			return false
		}
		b, err := types.DecodeBlock(raw)
		if err == nil {
			h.heights[consensus.Digest(b.Hash())] = b.Header.Inner.Number
		}
		return false
	case wire.ChannelVotes:
		v, err := wire.DecodeVote(p.msg)
		return err == nil && v.Vote.Kind == consensus.KindFinalize && h.hit(v.Vote.Proposal.Payload)
	case wire.ChannelCertificates:
		ce, err := wire.DecodeCertificate(p.msg)
		return err == nil && ce.Certified.Kind == consensus.KindFinalize && h.hit(ce.Certified.Proposal.Payload)
	default:
		return false
	}
}

func (h *heightDrop) hit(d consensus.Digest) bool {
	n, ok := h.heights[d]
	return ok && n >= h.from && n <= h.to
}

func TestEngineConcludesDKGOnNotarizedBranch(_ *testing.T) {
	c := newCluster()
	h := &heightDrop{heights: map[consensus.Digest]uint64{}, from: netEpochLength/2 + 1, to: netEpochLength - 2}
	c.drop = h.drop
	c.runUntil(func() bool { return c.height(0) > h.to })
	assert.Equal(c.height(0), uint64(netEpochLength-1), "the logs' blocks were finalized only with the boundary")
	c.runUntil(c.allAt(netEpochLength + 2))
	b, _ := c.chains[0].BlockByNumber(netEpochLength - 1)
	o, err := dkg.DecodeOutcome(b.Header.Inner.ExtraData)
	assert.NoError(err, "boundary outcome")
	assert.Equal(o.Epoch, uint64(1), "outcome epoch")
	logs := 0
	for n := h.from; n <= h.to; n++ {
		blk, _ := c.chains[0].BlockByNumber(n)
		if len(blk.Header.Inner.ExtraData) > 0 {
			logs++
		}
	}
	assert.Equal(logs, netValidators, "every dealer log landed in a block that was only notarized")
	assert.Equal(len(o.Output.Dealers), netValidators-1, "the boundary outcome selected the required logs from notarized blocks")
	for i := range c.engines {
		assert.Equal(c.engines[i].Epoch(), uint64(1), "every validator turned the epoch")
		got, _ := c.chains[i].BlockByNumber(netEpochLength + 2)
		want, _ := c.chains[0].BlockByNumber(netEpochLength + 2)
		assert.Equal(got.Hash(), want.Hash(), "same chain")
	}
}
