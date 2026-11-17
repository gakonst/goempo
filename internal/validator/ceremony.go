package validator

import (
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

type ceremony struct {
	e        *Engine
	st       *epochState
	info     *dkg.Info
	dealer   *dkg.Dealer
	player   *dkg.Player
	acked    map[PeerKey]bool
	myLog    *dkg.SignedDealerLog
	included bool
	logs     map[PeerKey]dkg.SignedDealerLog
	cached   *reckoning
}

type reckoning struct {
	value     dkg.Outcome
	encoded   []byte
	entries   []dkg.DealerEntry
	selection []int
	failed    bool
}

func newCeremony(e *Engine, st *epochState) (*ceremony, error) {
	cfg := dkg.RoundConfig{
		Round: st.epoch, Reveal: st.rules.Reveal, Dealers: st.outcome.Output.Players, Players: st.outcome.NextPlayers,
	}
	if !st.outcome.NextFullDKG {
		prev := st.outcome.Output
		cfg.Previous = &prev
	}
	info, err := dkg.NewInfo([]byte(dkg.TempoNamespace), cfg)
	if err != nil {
		return nil, err
	}
	c := &ceremony{e: e, st: st, info: info, acked: map[PeerKey]bool{}, logs: map[PeerKey]dkg.SignedDealerLog{}}
	if _, ok := info.Players.Index(e.me); ok {
		if c.player, err = dkg.NewPlayer(info, e.cfg.Key.Private()); err != nil {
			return nil, err
		}
	}
	if _, ok := info.Dealers.Index(e.me); !ok {
		return c, nil
	}
	dc := dkg.DealerConfig{Key: e.cfg.Key.Private(), Rand: e.cfg.Rand}
	if cfg.Previous != nil {
		if st.share == nil {
			return c, nil
		}
		s := *st.share
		dc.Previous = &s
	}
	if c.dealer, err = dkg.NewDealer(info, dc); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *ceremony) start() {
	c.recover()
	c.distribute()
}

func (c *ceremony) recover() {
	e := c.e
	head := e.cfg.Chain.Head().Header.Inner.Number
	start := max(e.cfg.Epocher.First(c.st.epoch), c.st.boundary.Header.Inner.Number+1)
	if head < start {
		return
	}
	for i := range min(head-start+1, RecoverMax) {
		b, ok := e.cfg.Chain.BlockByNumber(start + i)
		if !ok {
			return
		}
		c.record(b)
	}
}

func (c *ceremony) early() bool {
	e := c.e
	head := e.cfg.Chain.Head().Header.Inner.Number
	return head < e.cfg.Epocher.First(c.st.epoch)+e.cfg.Epocher.Length/2
}

func (c *ceremony) distribute() {
	if c.dealer == nil || c.myLog != nil {
		return
	}
	if !c.early() {
		l := c.dealer.Finalize()
		c.myLog = &l
		return
	}
	commit := c.dealer.Commitment()
	for i, p := range c.info.Players {
		if c.acked[p] {
			continue
		}
		m := dkg.Message{Kind: dkg.MessageDealer, Commitment: commit, Share: c.dealer.ShareFor(i)}
		if p == c.e.me {
			c.onDealing(c.e.me, m)
			continue
		}
		c.e.stats.Dealt++
		c.e.cfg.Net.Send(wire.ChannelDKG, []PeerKey{p}, m.EncodeEpoch(c.st.epoch))
	}
}

func (c *ceremony) logToInclude() []byte {
	if c.myLog == nil || c.included {
		return nil
	}
	return c.myLog.Encode()
}

func (c *ceremony) receive(from PeerKey, frame []byte) {
	m, err := dkg.DecodeEpochMessage(frame, len(c.info.Players))
	if err != nil || m.Epoch != c.st.epoch {
		return
	}
	switch m.Message.Kind {
	case dkg.MessageDealer:
		if _, ok := c.info.Dealers.Index(from); ok {
			c.onDealing(from, m.Message)
		}
	case dkg.MessageAck:
		c.onAck(from, m.Message.Ack)
	default:
		assert.Unreachable("MessageKind: unhandled value")
	}
}

func (c *ceremony) onDealing(dealer PeerKey, m dkg.Message) {
	if c.player == nil {
		return
	}
	sig, err := c.player.Receive(dealer, m.Commitment, m.Share)
	if err != nil {
		return
	}
	if dealer == c.e.me {
		c.onAck(c.e.me, sig)
		return
	}
	ack := dkg.Message{Kind: dkg.MessageAck, Ack: sig}
	c.e.cfg.Net.Send(wire.ChannelDKG, []PeerKey{dealer}, ack.EncodeEpoch(c.st.epoch))
}

func (c *ceremony) onAck(player PeerKey, sig [dkg.SignatureLen]byte) {
	if c.dealer == nil || c.myLog != nil || c.acked[player] {
		return
	}
	if c.dealer.Ack(player, sig) == nil {
		c.acked[player] = true
		c.e.stats.Acks++
	}
}

func (c *ceremony) onFinalized(b *types.Block) {
	c.record(b)
	c.distribute()
}

func (c *ceremony) record(b *types.Block) {
	c.cached = nil
	extra := b.Header.Inner.ExtraData
	if len(extra) == 0 || c.e.cfg.Epocher.IsLast(b.Header.Inner.Number) {
		return
	}
	l, err := dkg.DecodeSignedDealerLog(extra)
	if err != nil || !c.info.CheckSignedDealerLog(l) {
		return
	}
	if _, ok := c.logs[l.Dealer]; !ok {
		c.logs[l.Dealer] = l
	}
	if l.Dealer == c.e.me {
		c.included = true
	}
}

func (c *ceremony) entries() []dkg.DealerEntry {
	out := make([]dkg.DealerEntry, 0, len(c.logs))
	for _, d := range c.info.Dealers {
		if l, ok := c.logs[d]; ok {
			out = append(out, dkg.DealerEntry{Dealer: d, Log: l.Log})
		}
	}
	return out
}

func (c *ceremony) outcome() (reckoning, error) {
	if c.cached != nil {
		return *c.cached, nil
	}
	in, err := c.e.cfg.Chain.OutcomeInputs()
	if err != nil {
		return reckoning{}, err
	}
	entries := c.entries()
	r := reckoning{entries: entries}
	r.value = dkg.Outcome{Epoch: c.st.epoch + 1, Output: c.st.outcome.Output, NextPlayers: in.NextPlayers, NextFullDKG: in.FullDKG}
	sel, err := c.info.Select(entries)
	if err != nil {
		r.failed = true
	} else {
		out, err := c.info.Reckon(entries, sel)
		if err != nil {
			return reckoning{}, err
		}
		r.selection = sel
		r.value.Output = out
	}
	r.encoded = r.value.Encode()
	c.cached = &r
	return r, nil
}

func (c *ceremony) nextShare(r reckoning) (*dkg.Share, error) {
	if r.failed {
		return c.st.share, nil
	}
	if c.player == nil {
		return nil, nil
	}
	s, err := c.player.Finalize(r.entries, r.selection)
	if err != nil {
		c.e.stats.MissedShares++
		return nil, nil
	}
	return &s, nil
}
