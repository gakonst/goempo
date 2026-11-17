package sim

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/types"
)

const (
	ValidatorsMax  = 64
	eventsDefault  = 2_000_000
	eventsMax      = 100_000_000
	heapDepthMax   = 64
	EpochLengthMax = 1 << 20
	heightsMax     = 1 << 24
	seedLen        = ed25519.SeedSize
	percent        = 100
)

const _ = uint(dkg.ParticipantsMax - ValidatorsMax)

const _ = uint(eventsMax - eventsDefault)

const _ = uint(heightsMax - eventsDefault)

const _ = uint(1<<(heapDepthMax-1) - eventsMax)

const _ = uint(heightsMax - EpochLengthMax)

var (
	ErrConfig  = errors.New("sim: invalid config")
	ErrStalled = errors.New("sim: did not reach the target height")
)

type Outage struct {
	Node int
	From consensus.Millis
	To   consensus.Millis
}

type Config struct {
	Validators  int
	EpochLength uint64
	Epochs      uint64
	Seed        uint64
	DropPercent int
	LatencyMin  consensus.Millis
	LatencyMax  consensus.Millis
	Timeouts    consensus.Timeouts
	Outages     []Outage
	TimeMax     consensus.Millis
	EventsMax   int
}

type Result struct {
	Genesis      []byte
	Blocks       [][]byte
	Certificates [][]byte
	Heads        []uint64
	Views        []uint64
	Events       int
	Time         consensus.Millis
	Messages     int
	Dropped      int
	Failures     []int
}

type event struct {
	at   consensus.Millis
	seq  uint64
	node int
	env  *envelope
}

type queue struct {
	items []event
}

func (e event) before(o event) bool {
	if e.at != o.at {
		return e.at < o.at
	}
	return e.seq < o.seq
}

func (q *queue) len() int { return len(q.items) }

func (q *queue) push(e event) {
	q.items = append(q.items, e)
	i := len(q.items) - 1
	for range heapDepthMax {
		if i == 0 {
			return
		}
		p := (i - 1) / 2
		if !q.items[i].before(q.items[p]) {
			return
		}
		q.items[i], q.items[p] = q.items[p], q.items[i]
		i = p
	}
}

func (q *queue) pop() event {
	top := q.items[0]
	last := len(q.items) - 1
	q.items[0] = q.items[last]
	q.items = q.items[:last]
	i := 0
	for range heapDepthMax {
		small := i
		for _, c := range []int{2*i + 1, 2*i + 2} {
			if c < len(q.items) && q.items[c].before(q.items[small]) {
				small = c
			}
		}
		if small == i {
			break
		}
		q.items[i], q.items[small] = q.items[small], q.items[i]
		i = small
	}
	return top
}

type world struct {
	cfg       Config
	nodes     []*node
	q         queue
	seq       uint64
	rng       *rand.Rand
	now       consensus.Millis
	scheduled []consensus.Millis
	result    Result
}

func (w *world) push(e event) {
	w.seq++
	e.seq = w.seq
	w.q.push(e)
}

func (w *world) offline(node int, at consensus.Millis) bool {
	for _, o := range w.cfg.Outages {
		if o.Node == node && at >= o.From && at < o.To {
			return true
		}
	}
	return false
}

func (w *world) flush(n *node) {
	for _, e := range n.outbox {
		w.result.Messages++
		if w.offline(e.from, w.now) || int(w.rng.IntN(percent)) < w.cfg.DropPercent {
			w.result.Dropped++
			continue
		}
		span := int64(w.cfg.LatencyMax - w.cfg.LatencyMin + 1)
		latency := w.cfg.LatencyMin + consensus.Millis(w.rng.Int64N(span))
		env := e
		w.push(event{at: w.now + latency, node: e.to, env: &env})
	}
	n.outbox = n.outbox[:0]
	d := n.deadline()
	if d > w.now && d != w.scheduled[n.index] {
		w.scheduled[n.index] = d
		w.push(event{at: d, node: n.index})
	}
}

func validate(cfg Config) error {
	if cfg.Validators < 1 || cfg.Validators > ValidatorsMax || cfg.EpochLength < 4 || cfg.EpochLength > EpochLengthMax || cfg.Epochs < 1 {
		return ErrConfig
	}
	if cfg.LatencyMin < 1 || cfg.LatencyMax < cfg.LatencyMin || cfg.DropPercent < 0 || cfg.DropPercent >= percent {
		return ErrConfig
	}
	return nil
}

func keys(cfg Config) []ed25519.PrivateKey {
	out := make([]ed25519.PrivateKey, cfg.Validators)
	for i := range out {
		seed := make([]byte, seedLen)
		r := seededReader(fmt.Sprintf("validator-%d-%d", cfg.Seed, i), dkg.PublicKey{}, 0)
		_, err := r.Read(seed)
		if err != nil {
			return nil
		}
		out[i] = ed25519.NewKeyFromSeed(seed)
	}
	return out
}

type genesis struct {
	block  *block
	output dkg.Output
	shares map[dkg.PublicKey]dkg.Share
}

func genesisCeremony(ks []ed25519.PrivateKey, players dkg.KeySet) (genesis, error) {
	info, err := dkg.NewInfo([]byte(dkg.TempoNamespace), dkg.RoundConfig{Dealers: players, Players: players})
	if err != nil {
		return genesis{}, err
	}
	ps := make([]*dkg.Player, len(ks))
	for i, k := range ks {
		if ps[i], err = dkg.NewPlayer(info, k); err != nil {
			return genesis{}, err
		}
	}
	entries := make([]dkg.DealerEntry, 0, len(ks))
	for _, k := range ks {
		d, err := dkg.NewDealer(info, dkg.DealerConfig{Key: k, Rand: seededReader("genesis", dkg.KeyOf(k), 0)})
		if err != nil {
			return genesis{}, err
		}
		for i, p := range ps {
			sig, err := p.Receive(dkg.KeyOf(k), d.Commitment(), d.ShareFor(p.Index()))
			if err != nil {
				return genesis{}, err
			}
			if err := d.Ack(dkg.KeyOf(ks[i]), sig); err != nil {
				return genesis{}, err
			}
		}
		entries = append(entries, dkg.DealerEntry{Dealer: dkg.KeyOf(k), Log: d.Finalize().Log})
	}
	sortEntries(entries)
	return finishGenesis(info, ps, entries)
}

func finishGenesis(info *dkg.Info, ps []*dkg.Player, entries []dkg.DealerEntry) (genesis, error) {
	sel, err := info.Select(entries)
	if err != nil {
		return genesis{}, err
	}
	out, err := info.Reckon(entries, sel)
	if err != nil {
		return genesis{}, err
	}
	g := genesis{output: out, shares: map[dkg.PublicKey]dkg.Share{}}
	for _, p := range ps {
		s, err := p.Finalize(entries, sel)
		if err != nil {
			return genesis{}, err
		}
		g.shares[info.Players[s.Index]] = s
	}
	o := dkg.Outcome{Epoch: 0, Output: out, NextPlayers: info.Players}
	g.block = newBlock(types.Block{
		Header: types.Header{GeneralGasLimit: gasLimit, Inner: types.EthHeader{
			GasLimit: gasLimit, Timestamp: genesisSeconds, ExtraData: o.Encode(),
		}},
	})
	return g, nil
}

func Run(cfg Config) (Result, error) {
	if err := validate(cfg); err != nil {
		return Result{}, err
	}
	ks := keys(cfg)
	pubs := make([]dkg.PublicKey, len(ks))
	for i, k := range ks {
		pubs[i] = dkg.KeyOf(k)
	}
	players := dkg.NewKeySet(pubs)
	g, err := genesisCeremony(ks, players)
	if err != nil {
		return Result{}, err
	}
	w := &world{cfg: cfg, rng: rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)), scheduled: make([]consensus.Millis, len(ks))}
	w.result.Genesis = g.block.raw
	w.nodes = make([]*node, len(ks))
	for _, k := range ks {
		index, _ := players.Index(dkg.KeyOf(k))
		w.nodes[index] = newNode(cfg, k, players, g)
	}
	for _, o := range cfg.Outages {
		w.push(event{at: o.To, node: o.Node})
	}
	for _, n := range w.nodes {
		n.startAt = g.block
		n.step(nil)
		w.flush(n)
	}
	return w.loop()
}

func newNode(cfg Config, k ed25519.PrivateKey, players dkg.KeySet, g genesis) *node {
	index, _ := players.Index(dkg.KeyOf(k))
	return &node{
		index: index, key: k, players: players, epocher: consensus.Epocher{Length: min(cfg.EpochLength, EpochLengthMax)},
		ns: consensus.NewNamespace(consensus.TempoNamespace), timeouts: cfg.Timeouts,
		blocks: map[consensus.Digest]*block{g.block.digest: g.block}, finalized: []consensus.Digest{g.block.digest},
		certs: map[uint64][]byte{}, output: g.output, share: g.shares[dkg.KeyOf(k)],
		boundaries: map[uint64]envelope{}, helpedAt: map[int]consensus.Millis{}, requested: map[consensus.Digest]consensus.Millis{},
	}
}

func (w *world) target() uint64 { return w.cfg.Epochs * w.cfg.EpochLength }

func (w *world) done() bool {
	for _, n := range w.nodes {
		if n.head() < w.target() {
			return false
		}
	}
	return true
}

func (w *world) loop() (Result, error) {
	limit := min(w.cfg.EventsMax, eventsMax)
	if limit <= 0 {
		limit = eventsDefault
	}
	for range limit {
		if w.q.len() == 0 || w.done() {
			break
		}
		e := w.q.pop()
		if e.at > w.cfg.TimeMax {
			break
		}
		w.now = e.at
		w.result.Events++
		n := w.nodes[e.node]
		if w.offline(e.node, w.now) {
			continue
		}
		n.now = w.now
		n.step(e.env)
		if n.err != nil {
			return w.result, fmt.Errorf("node %d at %dms: %w", e.node, w.now, n.err)
		}
		w.flush(n)
	}
	return w.collect()
}

func (w *world) collect() (Result, error) {
	w.result.Time = w.now
	for _, n := range w.nodes {
		w.result.Heads = append(w.result.Heads, n.head())
		w.result.Views = append(w.result.Views, n.epoch)
		w.result.Failures = append(w.result.Failures, n.failures)
	}
	ref := w.nodes[0]
	for _, n := range w.nodes[1:] {
		for h := range min(n.head(), ref.head(), heightsMax) + 1 {
			if n.finalized[h] != ref.finalized[h] {
				return w.result, fmt.Errorf("height %d: %w", h, errSafety)
			}
		}
	}
	w.result.Certificates = make([][]byte, ref.head()+1)
	for h := range min(ref.head(), heightsMax) {
		b := ref.blocks[ref.finalized[h+1]]
		w.result.Blocks = append(w.result.Blocks, b.raw)
		w.result.Certificates[h+1] = ref.certs[h+1]
	}
	if !w.done() {
		return w.result, ErrStalled
	}
	return w.result, nil
}
