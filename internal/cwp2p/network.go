package cwp2p

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"maps"
	"slices"
	"sync"

	gio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const (
	DefaultBacklog          = 1024
	DefaultMailbox          = 1024
	DefaultSendBatch        = 8
	DefaultTrackedSets      = 1
	DefaultSynchronyMs      = 5_000
	DefaultMaxHandshakeAge  = 10_000
	DefaultHandshakeTimeout = 5 * nanosPerSecond
	DefaultPingNanos        = 50 * nanosPerSecond
	DefaultDialNanos        = 1 * nanosPerSecond
	DefaultBlockNanos       = 3600 * nanosPerSecond
	HandshakesMax           = 1024
	PeersMax                = 1024
	ChannelsMax             = 256
	TrackedSetsMax          = 64
	loopsMax                = 1 << 40
	pingBurst               = 2
	SendBatchMax            = 64
	MailboxMax              = 1 << 16
)

const _ = uint(DefaultMailbox - DefaultSendBatch)

const _ = uint(SendBatchMax - DefaultSendBatch)

const _ = uint(MailboxMax - DefaultMailbox)

const _ = uint(TrackedSetsMax - DefaultTrackedSets)

const _ = uint(PeersMax - pingBurst)

const _ = uint(HandshakesMax - ChannelsMax)

const _ = uint(loopsMax - PeersMax)

var (
	ErrClosed         = errors.New("cwp2p: network closed")
	ErrConfig         = errors.New("cwp2p: invalid network config")
	ErrUnknownChannel = errors.New("cwp2p: message on an unregistered channel")
	ErrPingRate       = errors.New("cwp2p: peer pings too often")
)

type Sleeper interface {
	Sleep(ctx context.Context, nanos uint64) bool
}

type Channel struct {
	ID      uint64
	Rate    uint32
	Backlog uint32
}

type Peer struct {
	Key  PublicKey
	Addr string
}

type Message struct {
	From    PublicKey
	Channel uint64
	Data    []byte
}

type Priority bool

const (
	PriorityHigh Priority = true
	PriorityLow  Priority = false
)

type Config struct {
	Key                   ed25519.PrivateKey
	Namespace             []byte
	Listener              gio.Listener
	Dialer                gio.Dialer
	Clock                 gio.Clock
	Sleeper               Sleeper
	Entropy               io.Reader
	Channels              []Channel
	MaxMessageSize        uint32
	Sake                  SakeVersion
	Cups                  CupsVersion
	SynchronyMs           uint64
	MaxHandshakeAgeMs     uint64
	HandshakeTimeoutNanos uint64
	PingNanos             uint64
	DialNanos             uint64
	BlockNanos            uint64
	TrackedSets           int
	Mailbox               int
	SendBatch             int
}

type trackedSet struct {
	id    uint64
	peers map[PublicKey]string
}

type peerConn struct {
	key      PublicKey
	stream   *Stream
	high     chan []byte
	low      chan []byte
	ctx      context.Context
	cancel   context.CancelFunc
	inbound  map[uint64]*Bucket
	outbound map[uint64]*Bucket
	pings    Bucket
	lastPing uint64
	role     Role
}

type Network struct {
	cfg      Config
	self     PublicKey
	entropy  lockedReader
	inbound  map[uint64]chan Message
	rates    map[uint64]uint32
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	closed   bool
	sets     []trackedSet
	conns    map[PublicKey]*peerConn
	reserved map[PublicKey]bool
	blocked  map[PublicKey]uint64
	lastDial map[PublicKey]uint64
	shakes   int
}

type lockedReader struct {
	mu sync.Mutex
	r  io.Reader
}

func (l *lockedReader) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return io.ReadFull(l.r, p)
}

func withDefaults(cfg Config) Config {
	set := func(v *uint64, d uint64) {
		if *v == 0 {
			*v = d
		}
	}
	set(&cfg.SynchronyMs, DefaultSynchronyMs)
	set(&cfg.MaxHandshakeAgeMs, DefaultMaxHandshakeAge)
	set(&cfg.HandshakeTimeoutNanos, DefaultHandshakeTimeout)
	set(&cfg.PingNanos, DefaultPingNanos)
	set(&cfg.DialNanos, DefaultDialNanos)
	set(&cfg.BlockNanos, DefaultBlockNanos)
	if cfg.TrackedSets == 0 {
		cfg.TrackedSets = DefaultTrackedSets
	}
	if cfg.Mailbox == 0 {
		cfg.Mailbox = DefaultMailbox
	}
	if cfg.SendBatch == 0 {
		cfg.SendBatch = DefaultSendBatch
	}
	return cfg
}

func validate(cfg *Config) error {
	if len(cfg.Key) != ed25519.PrivateKeySize || cfg.Clock == nil || cfg.Sleeper == nil || cfg.Entropy == nil {
		return ErrConfig
	}
	if cfg.Dialer == nil || len(cfg.Channels) > ChannelsMax || cfg.TrackedSets > TrackedSetsMax {
		return ErrConfig
	}
	if cfg.SendBatch > SendBatchMax || cfg.Mailbox > MailboxMax {
		return ErrConfig
	}
	if cfg.MaxMessageSize == 0 || cfg.MaxMessageSize > MessageSizeMax-PayloadOverheadMax {
		return ErrConfig
	}
	return nil
}

func New(cfg Config) (*Network, error) {
	cfg = withDefaults(cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	n := &Network{
		cfg:      cfg,
		self:     PublicKeyOf(cfg.Key),
		entropy:  lockedReader{r: cfg.Entropy},
		inbound:  make(map[uint64]chan Message, len(cfg.Channels)),
		rates:    make(map[uint64]uint32, len(cfg.Channels)),
		conns:    make(map[PublicKey]*peerConn),
		reserved: make(map[PublicKey]bool),
		blocked:  make(map[PublicKey]uint64),
		lastDial: make(map[PublicKey]uint64),
	}
	n.ctx, n.cancel = context.WithCancel(context.Background())
	for _, ch := range cfg.Channels {
		if _, dup := n.inbound[ch.ID]; dup {
			return nil, ErrConfig
		}
		backlog := ch.Backlog
		if backlog == 0 {
			backlog = DefaultBacklog
		}
		n.inbound[ch.ID] = make(chan Message, backlog)
		n.rates[ch.ID] = ch.Rate
	}
	if cfg.Listener != nil {
		n.wg.Go(n.acceptLoop)
	}
	n.wg.Go(n.maintainLoop)
	return n, nil
}

func (n *Network) PublicKey() PublicKey {
	return n.self
}

func (n *Network) Inbound(channel uint64) <-chan Message {
	return n.inbound[channel]
}

func (n *Network) streamConfig() *StreamConfig {
	return &StreamConfig{
		Sake:                  n.cfg.Sake,
		Cups:                  n.cfg.Cups,
		Namespace:             n.cfg.Namespace,
		Key:                   n.cfg.Key,
		Clock:                 n.cfg.Clock,
		MaxAgeMs:              n.cfg.MaxHandshakeAgeMs,
		SynchronyMs:           n.cfg.SynchronyMs,
		HandshakeTimeoutNanos: n.cfg.HandshakeTimeoutNanos,
		Entropy:               &n.entropy,
		MaxMessageSize:        n.cfg.MaxMessageSize + PayloadOverheadMax,
	}
}

func (n *Network) authorizedLocked(key PublicKey) (string, bool) {
	for i := len(n.sets) - 1; i >= 0; i-- {
		if addr, ok := n.sets[i].peers[key]; ok {
			return addr, true
		}
	}
	return "", false
}

func (n *Network) blockedLocked(key PublicKey) bool {
	until, ok := n.blocked[key]
	if !ok {
		return false
	}
	if until > n.cfg.Clock.MonotonicNanos() {
		return true
	}
	delete(n.blocked, key)
	return false
}

func (n *Network) reserve(key PublicKey) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || key == n.self || n.reserved[key] || n.blockedLocked(key) {
		return false
	}
	if _, ok := n.authorizedLocked(key); !ok {
		return false
	}
	n.reserved[key] = true
	return true
}

func (n *Network) release(key PublicKey) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.reserved, key)
}

func (n *Network) handshakeSlot() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.shakes >= HandshakesMax {
		return false
	}
	n.shakes++
	return true
}

func (n *Network) handshakeDone() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.shakes--
}

func (n *Network) acceptLoop() {
	for range loopsMax {
		conn, err := n.cfg.Listener.Accept()
		if err != nil {
			return
		}
		if !n.handshakeSlot() {
			closeQuietly(conn)
			continue
		}
		n.wg.Go(func() {
			defer n.handshakeDone()
			n.accept(conn)
		})
	}
	assert.Fail("cwp2p: accept loop ran loopsMax times")
}

func closeQuietly(c gio.Conn) {
	if err := c.Close(); err != nil {
		return
	}
}

func (n *Network) accept(conn gio.Conn) {
	var reserved PublicKey
	var held bool
	s, err := AcceptStream(conn, n.streamConfig(), func(key PublicKey) bool {
		held = n.reserve(key)
		reserved = key
		return held
	})
	if err != nil {
		if held {
			n.release(reserved)
		}
		closeQuietly(conn)
		return
	}
	n.run(conn, s, RoleListener)
}

func (n *Network) dial(peer Peer) {
	conn, err := n.cfg.Dialer.Dial(peer.Addr)
	if err != nil {
		n.release(peer.Key)
		return
	}
	s, err := DialStream(conn, n.streamConfig(), peer.Key)
	if err != nil {
		n.release(peer.Key)
		closeQuietly(conn)
		return
	}
	n.run(conn, s, RoleDialer)
}

func (n *Network) newPeer(s *Stream, role Role) *peerConn {
	p := &peerConn{
		key:      s.Peer,
		stream:   s,
		high:     make(chan []byte, n.cfg.Mailbox),
		low:      make(chan []byte, n.cfg.Mailbox),
		inbound:  make(map[uint64]*Bucket, len(n.rates)),
		outbound: make(map[uint64]*Bucket, len(n.rates)),
		lastPing: n.cfg.Clock.MonotonicNanos(),
		role:     role,
	}
	for _, id := range slices.Sorted(maps.Keys(n.rates)) {
		rate := n.rates[id]
		in, out := NewBucket(rate), NewBucket(rate)
		p.inbound[id], p.outbound[id] = &in, &out
	}
	p.ctx, p.cancel = context.WithCancel(n.ctx)
	p.pings = Bucket{interval: n.cfg.PingNanos / pingBurst, tolerance: n.cfg.PingNanos / pingBurst}
	return p
}

func (n *Network) run(conn gio.Conn, s *Stream, role Role) {
	p := n.newPeer(s, role)
	n.mu.Lock()
	if n.closed || len(n.conns) >= PeersMax {
		n.mu.Unlock()
		n.release(p.key)
		closeQuietly(conn)
		return
	}
	n.conns[p.key] = p
	n.mu.Unlock()
	var inner sync.WaitGroup
	inner.Go(func() { n.writeLoop(p) })
	inner.Go(func() { n.readLoop(p) })
	<-p.ctx.Done()
	closeQuietly(conn)
	inner.Wait()
	n.mu.Lock()
	if n.conns[p.key] == p {
		delete(n.conns, p.key)
	}
	delete(n.reserved, p.key)
	n.mu.Unlock()
}

func (n *Network) disconnect(p *peerConn) {
	p.cancel()
}

func (n *Network) writeLoop(p *peerConn) {
	batch := make([][]byte, 0, n.cfg.SendBatch)
	for range loopsMax {
		batch = batch[:0]
		select {
		case <-p.ctx.Done():
			return
		case m := <-p.high:
			batch = append(batch, m)
		case m := <-p.low:
			batch = append(batch, m)
		}
		batch = drain(p, batch, n.cfg.SendBatch)
		if err := p.stream.Sender.SendMany(batch); err != nil {
			n.disconnect(p)
			return
		}
	}
	assert.Fail("cwp2p: write loop ran loopsMax times")
}

func drain(p *peerConn, batch [][]byte, limit int) [][]byte {
	for range min(limit, SendBatchMax) {
		if len(batch) >= limit {
			return batch
		}
		select {
		case m := <-p.high:
			batch = append(batch, m)
			continue
		default:
		}
		select {
		case m := <-p.low:
			batch = append(batch, m)
		default:
			return batch
		}
	}
	return batch
}

func (n *Network) readLoop(p *peerConn) {
	defer n.disconnect(p)
	for range loopsMax {
		raw, err := p.stream.Receiver.Recv()
		if err != nil {
			return
		}
		msg, err := DecodeMessage(raw, n.cfg.MaxMessageSize)
		if err != nil {
			return
		}
		if err := n.handle(p, msg); err != nil {
			return
		}
	}
	assert.Fail("cwp2p: read loop ran loopsMax times")
}

func (n *Network) handle(p *peerConn, msg WireMessage) error {
	if msg.Kind == KindPing {
		if !p.pings.Take(n.cfg.Clock.MonotonicNanos()) {
			return ErrPingRate
		}
		return nil
	}
	bucket, ok := p.inbound[msg.Channel]
	if !ok {
		return ErrUnknownChannel
	}
	if wait := bucket.Wait(n.cfg.Clock.MonotonicNanos()); wait > 0 {
		if !n.cfg.Sleeper.Sleep(p.ctx, wait) {
			return ErrClosed
		}
	}
	bucket.Take(n.cfg.Clock.MonotonicNanos())
	select {
	case n.inbound[msg.Channel] <- Message{From: p.key, Channel: msg.Channel, Data: msg.Data}:
	default:
	}
	return nil
}

func (n *Network) Send(channel uint64, to []PublicKey, data []byte, priority Priority) []PublicKey {
	if uint64(len(data)) > uint64(n.cfg.MaxMessageSize) {
		return nil
	}
	if _, ok := n.rates[channel]; !ok {
		return nil
	}
	frame := EncodeData(channel, data)
	now := n.cfg.Clock.MonotonicNanos()
	n.mu.Lock()
	defer n.mu.Unlock()
	targets := to
	if targets == nil {
		targets = n.connectedLocked()
	}
	sent := make([]PublicKey, 0, len(targets))
	for _, key := range targets {
		p, ok := n.conns[key]
		if !ok || !p.outbound[channel].Take(now) {
			continue
		}
		queue := p.low
		if priority == PriorityHigh {
			queue = p.high
		}
		select {
		case queue <- frame:
			sent = append(sent, key)
		default:
		}
	}
	return sent
}

func sortedKeys[V any](m map[PublicKey]V) []PublicKey {
	out := slices.Collect(maps.Keys(m))
	slices.SortFunc(out, func(a, b PublicKey) int { return bytes.Compare(a[:], b[:]) })
	return out
}

func (n *Network) connectedLocked() []PublicKey {
	return sortedKeys(n.conns)
}

func (n *Network) Connected() []PublicKey {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.connectedLocked()
}

func (n *Network) Track(id uint64, peers []Peer) bool {
	set := trackedSet{id: id, peers: make(map[PublicKey]string, len(peers))}
	for _, p := range peers {
		set.peers[p.Key] = p.Addr
	}
	n.mu.Lock()
	if len(n.sets) > 0 && id <= n.sets[len(n.sets)-1].id {
		n.mu.Unlock()
		return false
	}
	n.sets = append(n.sets, set)
	if len(n.sets) > n.cfg.TrackedSets {
		n.sets = slices.Delete(n.sets, 0, len(n.sets)-n.cfg.TrackedSets)
	}
	var drop []*peerConn
	for _, key := range sortedKeys(n.conns) {
		if _, ok := n.authorizedLocked(key); !ok {
			drop = append(drop, n.conns[key])
		}
	}
	n.mu.Unlock()
	for _, p := range drop {
		n.disconnect(p)
	}
	return true
}

func (n *Network) Authorized() []Peer {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.authorizedPeersLocked()
}

func (n *Network) Block(key PublicKey) {
	n.mu.Lock()
	n.blocked[key] = n.cfg.Clock.MonotonicNanos() + n.cfg.BlockNanos
	p := n.conns[key]
	n.mu.Unlock()
	if p != nil {
		n.disconnect(p)
	}
}

func (n *Network) maintainLoop() {
	for range loopsMax {
		n.maintain()
		if !n.cfg.Sleeper.Sleep(n.ctx, min(n.cfg.DialNanos, n.cfg.PingNanos)) {
			return
		}
	}
	assert.Fail("cwp2p: maintain loop ran loopsMax times")
}

func (n *Network) maintain() {
	now := n.cfg.Clock.MonotonicNanos()
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	var dials []Peer
	for _, p := range n.authorizedPeersLocked() {
		if p.Key == n.self || n.reserved[p.Key] || n.blockedLocked(p.Key) || p.Addr == "" {
			continue
		}
		if last, ok := n.lastDial[p.Key]; ok && now-last < n.cfg.DialNanos {
			continue
		}
		n.lastDial[p.Key] = now
		n.reserved[p.Key] = true
		dials = append(dials, p)
	}
	for _, key := range sortedKeys(n.conns) {
		p := n.conns[key]
		if now-p.lastPing >= n.cfg.PingNanos {
			select {
			case p.high <- EncodePing():
				p.lastPing = now
			default:
			}
		}
	}
	n.mu.Unlock()
	for _, p := range dials {
		n.wg.Go(func() { n.dial(p) })
	}
}

func (n *Network) authorizedPeersLocked() []Peer {
	union := make(map[PublicKey]string)
	for _, set := range n.sets {
		maps.Copy(union, set.peers)
	}
	keys := sortedKeys(union)
	out := make([]Peer, len(keys))
	for i, key := range keys {
		out[i] = Peer{Key: key, Addr: union[key]}
	}
	return out
}

func (n *Network) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return ErrClosed
	}
	n.closed = true
	n.cancel()
	n.mu.Unlock()
	var err error
	if n.cfg.Listener != nil {
		err = n.cfg.Listener.Close()
	}
	n.wg.Wait()
	return err
}
