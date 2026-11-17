package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gakonst/goempo/internal/builder"
	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/cwp2p"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/genesis"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/txpool"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/validator"
)

const (
	maxMessageSize    = 8_388_780
	dialTimeoutNanos  = uint64(5 * time.Second)
	sharePrefix       = "signing.share."
	reportEvery       = 10 * time.Second
	inboundBuffered   = 1024
	defaultLeaderMs   = 2000
	defaultNotarizeMs = 3000
	defaultRetryMs    = 1000
	epochLengthMax    = 1 << 20
)

const _ = uint(validator.RecoverMax - epochLengthMax)

var errEpochLength = errors.New("genesis config epochLength must be between 1 and 2^20")

type validatorOptions struct {
	chain        string
	datadir      string
	signingKey   string
	signingShare string
	listen       string
	feeRecipient string
	http         string
	metrics      string
	bypassIP     bool
	leaderMs     uint64
	notarizeMs   uint64
	retryMs      uint64
}

func parseValidator(args []string) (validatorOptions, error) {
	var o validatorOptions
	fs := flag.NewFlagSet("goempo validator", flag.ContinueOnError)
	fs.StringVar(&o.chain, "chain", "", "genesis JSON file of the network")
	fs.StringVar(&o.datadir, "datadir", "", "state, blocks and saved shares")
	fs.StringVar(&o.signingKey, "consensus.signing-key", "", "ed25519 signing key file (0x-hex seed)")
	fs.StringVar(&o.signingShare, "consensus.signing-share", "", "BLS share file for the first epoch (0x-hex varint index || scalar)")
	fs.StringVar(&o.listen, "consensus.listen-address", "0.0.0.0:8000", "p2p listen address")
	fs.StringVar(&o.feeRecipient, "consensus.fee-recipient", "", "beneficiary of proposed blocks (default: the validator's registered fee recipient)")
	fs.StringVar(&o.http, "http", "", "JSON-RPC status address (eth_blockNumber, eth_getBlockByNumber, goempo_validatorStatus)")
	fs.StringVar(&o.metrics, "consensus.metrics-address", "", "Prometheus metrics address")
	fs.BoolVar(&o.bypassIP, "consensus.bypass-ip-check", false, "accepted for tempo compatibility: goempo authenticates peers by key only")
	fs.Uint64Var(&o.leaderMs, "consensus.wait-for-proposal", defaultLeaderMs, "leader timeout in ms")
	fs.Uint64Var(&o.notarizeMs, "consensus.wait-for-notarizations", defaultNotarizeMs, "notarization timeout in ms")
	fs.Uint64Var(&o.retryMs, "consensus.nullify-retry", defaultRetryMs, "nullify rebroadcast interval in ms")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.chain == "" || o.datadir == "" || o.signingKey == "" {
		return o, errors.New("goempo validator: --chain, --datadir and --consensus.signing-key are required")
	}
	return o, nil
}

type wallClock struct{ start time.Time }

func (c wallClock) MonotonicNanos() uint64 { return uint64(time.Since(c.start)) }

func (c wallClock) RealtimeNanos() uint64 { return uint64(time.Now().UnixNano()) }

type timerSleeper struct{}

func (timerSleeper) Sleep(ctx context.Context, nanos uint64) bool {
	t := time.NewTimer(time.Duration(nanos))
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func tempoChannels() []cwp2p.Channel {
	return []cwp2p.Channel{
		{ID: wire.ChannelVotes, Rate: 128}, {ID: wire.ChannelCertificates, Rate: 128}, {ID: wire.ChannelResolver, Rate: 128},
		{ID: wire.ChannelBroadcast, Rate: 8}, {ID: wire.ChannelMarshal, Rate: 8}, {ID: wire.ChannelDKG, Rate: 128},
	}
}

type transport struct{ net *cwp2p.Network }

func (t transport) Send(channel uint64, to []validator.PeerKey, msg []byte) {
	var peers []cwp2p.PublicKey
	if to != nil {
		peers = make([]cwp2p.PublicKey, len(to))
		for i, k := range to {
			peers[i] = cwp2p.PublicKey(k)
		}
	}
	prio := cwp2p.PriorityHigh
	if channel == wire.ChannelBroadcast || channel == wire.ChannelMarshal {
		prio = !cwp2p.PriorityHigh
	}
	t.net.Send(channel, peers, msg, prio)
}

type chain struct {
	*node.Local
	build *builder.Builder
	fee   primitives.Address
}

func (c *chain) Build(parent *types.Block, attrs validator.Attributes) (*types.Block, error) {
	var out *types.Block
	err := c.WithHead(func(st *state.State, head *types.Block, getHash func(uint64) primitives.Hash) error {
		if head.Hash() != parent.Hash() {
			return node.ErrLocalParent
		}
		ctx := attrs.Consensus
		built, err := c.build.Build(st, c.Executor(), parent, builder.Attributes{
			TimestampMillis: attrs.TimestampMillis, ExtraData: attrs.ExtraData, Beneficiary: c.fee,
			Consensus: &ctx, GasLimit: parent.Header.Inner.GasLimit, GetHash: getHash,
		})
		if err != nil {
			return err
		}
		out = &built.Block
		return nil
	})
	return out, err
}

func (c *chain) OutcomeInputs() (validator.OutcomeInputs, error) {
	cfg, err := c.DkgConfig()
	if err != nil {
		return validator.OutcomeInputs{}, err
	}
	keys := make([]dkg.PublicKey, 0, len(cfg.ActiveValidators))
	for _, v := range cfg.ActiveValidators {
		k := dkg.PublicKey(v.PublicKey)
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	epoch := c.Executor().EpochLength
	head := c.Head().Header.Inner.Number
	next := uint64(0)
	if epoch > 0 {
		next = head/epoch + 1
	}
	return validator.OutcomeInputs{NextPlayers: dkg.NewKeySet(keys), FullDKG: cfg.NextFullDkgEpoch == next}, nil
}

func (c *chain) peers() ([]cwp2p.Peer, error) {
	cfg, err := c.DkgConfig()
	if err != nil {
		return nil, err
	}
	out := make([]cwp2p.Peer, 0, len(cfg.ActiveValidators))
	for _, v := range cfg.ActiveValidators {
		out = append(out, cwp2p.Peer{Key: cwp2p.PublicKey(v.PublicKey), Addr: string(v.Ingress)})
	}
	return out, nil
}

func (c *chain) registeredFee(me validator.PeerKey) (primitives.Address, bool) {
	cfg, err := c.DkgConfig()
	if err != nil {
		return primitives.Address{}, false
	}
	for _, v := range cfg.ActiveValidators {
		if dkg.PublicKey(v.PublicKey) == me {
			return v.FeeRecipient, true
		}
	}
	return primitives.Address{}, false
}

func rulesFor(id hardfork.ChainID) func(uint64) validator.Rules {
	return func(ts uint64) validator.Rules {
		if hardfork.At(id, ts).AtLeast(hardfork.T12) {
			return validator.Rules{Elector: consensus.ElectorV1, Reveal: dkg.RevealV1}
		}
		return validator.Rules{Elector: consensus.ElectorV0, Reveal: dkg.RevealV0}
	}
}

func sharePath(datadir string, epoch uint64) string {
	return filepath.Join(datadir, sharePrefix+strconv.FormatUint(epoch, 10))
}

func loadShare(o *validatorOptions, epoch uint64) (*dkg.Share, error) {
	if s, err := validator.ReadShare(sharePath(o.datadir, epoch)); err == nil {
		return &s, nil
	}
	if o.signingShare == "" {
		return nil, nil
	}
	s, err := validator.ReadShare(o.signingShare)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func openChain(o *validatorOptions) (*chain, error) {
	raw, err := os.ReadFile(o.chain)
	if err != nil {
		return nil, err
	}
	g, err := genesis.Parse(raw)
	if err != nil {
		return nil, err
	}
	stateDir, blockDir := filepath.Join(o.datadir, "state"), filepath.Join(o.datadir, "blocks")
	if err := os.MkdirAll(stateDir, dirMode); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(blockDir, dirMode); err != nil {
		return nil, err
	}
	sfs, err := goio.NewDirFS(stateDir)
	if err != nil {
		return nil, err
	}
	bfs, err := goio.NewDirFS(blockDir)
	if err != nil {
		return nil, err
	}
	l, err := node.OpenLocal(node.LocalConfig{State: sfs, Blocks: bfs, Genesis: g})
	if err != nil {
		return nil, err
	}
	return &chain{Local: l, build: builder.New(txpool.New(txpool.Config{Executor: l.Executor(), Now: func() uint64 { return uint64(time.Now().Unix()) }}))}, nil
}

type session struct {
	o      *validatorOptions
	c      *chain
	net    *cwp2p.Network
	eng    *validator.Engine
	start  time.Time
	epoch  uint64
	logged time.Time
	status *status
	srvs   [2]*http.Server
	wg     sync.WaitGroup
}

func (s *session) serve(slot int, addr string) error {
	srv, err := serveStatus(&s.wg, addr, s.status)
	s.srvs[slot] = srv
	return err
}

func closeServer(srv *http.Server) error {
	if srv == nil {
		return nil
	}
	return srv.Close()
}

func (s *session) closeServers() error {
	err := errors.Join(closeServer(s.srvs[0]), closeServer(s.srvs[1]))
	s.wg.Wait()
	return err
}

func runValidator(args []string) int {
	o, err := parseValidator(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	s, err := startValidator(&o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "goempo validator:", err)
		return 1
	}
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(sig)
	defer cancel()
	err = s.loop(ctx, cancel)
	err = errors.Join(err, s.closeServers(), s.net.Close(), s.c.Close())
	if err != nil {
		fmt.Fprintln(os.Stderr, "goempo validator:", err)
		return 1
	}
	return 0
}

func startValidator(o *validatorOptions) (*session, error) {
	key, err := validator.ReadSigningKey(o.signingKey)
	if err != nil {
		return nil, err
	}
	c, err := openChain(o)
	if err != nil {
		return nil, err
	}
	me := dkg.KeyOf(key.Private())
	c.fee, _ = c.registeredFee(me)
	if o.feeRecipient != "" {
		if c.fee, err = primitives.HexToAddress(o.feeRecipient); err != nil {
			return nil, errors.Join(err, c.Close())
		}
	}
	length := c.Executor().EpochLength
	if length == 0 || length > epochLengthMax {
		return nil, errors.Join(errEpochLength, c.Close())
	}
	head := c.Head().Header.Inner.Number
	epocher := consensus.Epocher{Length: length}
	share, err := loadShare(o, epocher.Epoch(head+1))
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	l, err := goio.ListenTCP(o.listen)
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	start := time.Now()
	net, err := cwp2p.New(cwp2p.Config{
		Key: key.Private(), Namespace: cwp2p.TempoNamespace(), Listener: l, Dialer: goio.TCPDialer{TimeoutNanos: dialTimeoutNanos},
		Clock: wallClock{start: start}, Sleeper: timerSleeper{}, Entropy: rand.Reader, Channels: tempoChannels(),
		MaxMessageSize: maxMessageSize,
	})
	if err != nil {
		return nil, errors.Join(err, l.Close(), c.Close())
	}
	s := &session{o: o, c: c, net: net, start: start, logged: start}
	s.eng, err = validator.New(validator.Config{
		Key: key, Share: share, Epocher: epocher, Chain: c, Net: transport{net: net}, Rand: rand.Reader,
		Timeouts:  consensus.Timeouts{Leader: consensus.Millis(o.leaderMs), Notarization: consensus.Millis(o.notarizeMs), Retry: consensus.Millis(o.retryMs)},
		WallClock: func() uint64 { return uint64(time.Now().UnixMilli()) },
		RulesAt:   rulesFor(c.Executor().ChainID),
		SaveShare: func(epoch uint64, sh dkg.Share) error {
			return validator.WriteShare(sharePath(o.datadir, epoch), sh, validator.Replace)
		},
	})
	if err != nil {
		return nil, errors.Join(err, net.Close(), c.Close())
	}
	s.status = &status{c: c}
	if err := s.serve(0, o.http); err != nil {
		return nil, errors.Join(err, net.Close(), c.Close())
	}
	if err := s.serve(1, o.metrics); err != nil {
		return nil, errors.Join(err, s.closeServers(), net.Close(), c.Close())
	}
	fmt.Fprintf(os.Stderr, "goempo validator: key %s head %d epoch %d member %v listen %s\n", me, head, s.eng.Epoch(), share != nil, o.listen)
	return s, nil
}

func (s *session) now() consensus.Millis { return consensus.Millis(time.Since(s.start).Milliseconds()) }

func (s *session) track() {
	peers, err := s.c.peers()
	if err != nil {
		fmt.Fprintln(os.Stderr, "goempo validator: read peers:", err)
		return
	}
	s.net.Track(s.eng.Epoch(), peers)
	s.epoch = s.eng.Epoch()
}

type inbound struct {
	votes, certs, resolver, broadcast, marshal, dkg <-chan cwp2p.Message
}

func (s *session) inbound() inbound {
	return inbound{
		votes: s.net.Inbound(wire.ChannelVotes), certs: s.net.Inbound(wire.ChannelCertificates),
		resolver: s.net.Inbound(wire.ChannelResolver), broadcast: s.net.Inbound(wire.ChannelBroadcast),
		marshal: s.net.Inbound(wire.ChannelMarshal), dkg: s.net.Inbound(wire.ChannelDKG),
	}
}

func (s *session) loop(ctx context.Context, cancel context.CancelFunc) error {
	s.track()
	s.eng.Start(s.now())
	in := s.inbound()
	timer := time.NewTimer(0)
	defer timer.Stop()
	var failed error
	for {
		if err := s.eng.Err(); err != nil && failed == nil {
			failed = err
			cancel()
		}
		if s.eng.Epoch() != s.epoch {
			s.track()
		}
		s.status.last.Store(&snapshot{epoch: s.eng.Epoch(), member: s.eng.Member(), peers: len(s.net.Connected()), stats: s.eng.Stats()})
		s.report()
		wait := time.Duration(s.eng.Deadline()-s.now()) * time.Millisecond
		timer.Reset(max(wait, time.Millisecond))
		var m cwp2p.Message
		select {
		case <-ctx.Done():
			return failed
		case m = <-in.votes:
		case m = <-in.certs:
		case m = <-in.resolver:
		case m = <-in.broadcast:
		case m = <-in.marshal:
		case m = <-in.dkg:
		case <-timer.C:
			s.eng.Tick(s.now())
			continue
		}
		s.eng.Receive(s.now(), validator.PeerKey(m.From), m.Channel, m.Data)
	}
}

func (s *session) report() {
	if time.Since(s.logged) < reportEvery {
		return
	}
	s.logged = time.Now()
	st := s.eng.Stats()
	h := s.c.Head()
	fmt.Fprintf(os.Stderr, "goempo validator: head %d %s epoch %d member %v peers %d proposed %d committed %d\n",
		h.Header.Inner.Number, h.Hash(), s.eng.Epoch(), s.eng.Member(), len(s.net.Connected()), st.Proposed, st.Committed)
}
