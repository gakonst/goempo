package p2p

import (
	"errors"
	stdio "io"
	"strconv"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/p2p/rlpx"
	"github.com/gakonst/goempo/internal/primitives"
)

const (
	messagesPerReplyMax = 4096
	replyBytesSoftMax   = 2 << 20
	ReasonClientQuit    = DisconnectReason(0x08)
	ReasonSubprotocol   = DisconnectReason(0x10)
	ReasonUseless       = DisconnectReason(0x03)
)

const _ = uint(replyBytesSoftMax - messagesPerReplyMax)

var (
	ErrNoEth      = errors.New("p2p: peer shares no eth version")
	ErrWrongChain = errors.New("p2p: peer is on another chain")
	ErrForkID     = errors.New("p2p: peer's fork id is incompatible")
	ErrUnexpected = errors.New("p2p: unexpected message")
	ErrNoAnswer   = errors.New("p2p: peer sent too many messages without answering")
)

type DisconnectError struct {
	Reason DisconnectReason
}

func (e *DisconnectError) Error() string {
	return "p2p: peer disconnected, reason " + strconv.FormatUint(uint64(e.Reason), 10)
}

type Store interface {
	HeaderNumber(hash primitives.Hash) (uint64, bool)
	Header(number uint64) ([]byte, bool)
	Body(hash primitives.Hash) ([]byte, bool)
}

type Config struct {
	Key          secp256k1.PrivateKey
	Entropy      stdio.Reader
	Client       string
	NetworkID    uint64
	Genesis      primitives.Hash
	Schedule     Schedule
	Head         Head
	HeadHash     primitives.Hash
	Earliest     uint64
	TimeoutNanos uint64
	Store        Store
}

type Peer struct {
	conn    *rlpx.Conn
	hello   Hello
	status  Status
	blocks  BlockRange
	nextID  uint64
	config  Config
	version uint64
}

func Connect(conn io.Conn, remote secp256k1.PublicKey, cfg Config) (*Peer, error) {
	keys, err := rlpx.GenerateKeys(cfg.Entropy, cfg.Key)
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadTimeout(cfg.TimeoutNanos); err != nil {
		return nil, err
	}
	c, err := rlpx.Dial(conn, rlpx.NewInitiator(keys, remote, cfg.Entropy))
	if err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return handshake(c, cfg)
}

func Accept(conn io.Conn, cfg Config) (*Peer, error) {
	keys, err := rlpx.GenerateKeys(cfg.Entropy, cfg.Key)
	if err != nil {
		return nil, err
	}
	c, err := rlpx.Accept(conn, rlpx.NewRecipient(keys, cfg.Entropy))
	if err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return handshake(c, cfg)
}

func (cfg Config) localStatus(version uint64) Status {
	return Status{
		Version:    version,
		NetworkID:  cfg.NetworkID,
		TD:         []byte{},
		Genesis:    cfg.Genesis,
		Fork:       cfg.Schedule.ID(cfg.Head.Timestamp),
		Earliest:   cfg.Earliest,
		Latest:     cfg.Head.Number,
		LatestHash: cfg.HeadHash,
	}
}

func sharedEth(caps []Cap) uint64 {
	best := uint64(0)
	for _, c := range caps {
		if c.Name != "eth" || c.Version <= best {
			continue
		}
		if c.Version == eth68 || c.Version == eth69 {
			best = c.Version
		}
	}
	return best
}

func handshake(c *rlpx.Conn, cfg Config) (*Peer, error) {
	p := &Peer{conn: c, config: cfg}
	pub, err := cfg.Key.PublicKey()
	if err != nil {
		return nil, errors.Join(err, c.Close())
	}
	hello := Hello{Version: BaseVersion, Client: cfg.Client, Caps: []Cap{{Name: "eth", Version: eth68}, {Name: "eth", Version: eth69}}, ID: pub}
	if err := c.WriteMsg(rlpx.Msg{Code: codeHello, Payload: hello.Encode()}); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	if err := p.readHello(); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	if err := p.exchangeStatus(); err != nil {
		return nil, errors.Join(err, p.disconnect(ReasonSubprotocol))
	}
	return p, nil
}

func (p *Peer) readHello() error {
	m, err := p.conn.ReadMsg()
	if err != nil {
		return err
	}
	if m.Code == codeDisc {
		return &DisconnectError{Reason: decodeDisconnect(m.Payload)}
	}
	if m.Code != codeHello {
		return ErrUnexpected
	}
	if p.hello, err = DecodeHello(m.Payload); err != nil {
		return err
	}
	p.version = sharedEth(p.hello.Caps)
	if p.version == 0 {
		return ErrNoEth
	}
	if p.hello.Version >= BaseVersion {
		p.conn.EnableSnappy()
	}
	return nil
}

func (p *Peer) exchangeStatus() error {
	local := p.config.localStatus(p.version)
	if err := p.conn.WriteMsg(rlpx.Msg{Code: ethOffset + ethStatus, Payload: local.Encode()}); err != nil {
		return err
	}
	for range messagesPerReplyMax {
		m, err := p.conn.ReadMsg()
		if err != nil {
			return err
		}
		if m.Code == ethOffset+ethStatus {
			return p.acceptStatus(m.Payload)
		}
		if err := p.handle(m); err != nil {
			return err
		}
	}
	return ErrNoAnswer
}

func (p *Peer) acceptStatus(payload []byte) error {
	st, err := DecodeStatus(payload)
	if err != nil {
		return err
	}
	if st.Version != p.version {
		return ErrUnexpected
	}
	if st.NetworkID != p.config.NetworkID || st.Genesis != p.config.Genesis {
		return ErrWrongChain
	}
	if !p.config.Schedule.Compatible(p.config.Head, st.Fork) {
		return ErrForkID
	}
	p.status = st
	p.blocks = BlockRange{Earliest: st.Earliest, Latest: st.Latest, LatestHash: st.LatestHash}
	return nil
}

func (p *Peer) Hello() Hello { return p.hello }

func (p *Peer) Status() Status { return p.status }

func (p *Peer) Version() uint64 { return p.version }

func (p *Peer) Blocks() BlockRange { return p.blocks }

func (p *Peer) disconnect(reason DisconnectReason) error {
	writeErr := p.conn.WriteMsg(rlpx.Msg{Code: codeDisc, Payload: encodeDisconnect(reason)})
	return errors.Join(writeErr, p.conn.Close())
}

func (p *Peer) Close() error {
	return p.disconnect(ReasonClientQuit)
}

type request struct {
	code    uint64
	reply   uint64
	id      uint64
	payload []byte
}

func (p *Peer) roundTrip(q request) ([][]byte, error) {
	if err := p.conn.WriteMsg(rlpx.Msg{Code: ethOffset + q.code, Payload: q.payload}); err != nil {
		return nil, err
	}
	for range messagesPerReplyMax {
		m, err := p.conn.ReadMsg()
		if err != nil {
			return nil, err
		}
		if m.Code == ethOffset+q.reply {
			items, err := decodeItems(m.Payload)
			if err != nil {
				return nil, err
			}
			if items.id == q.id {
				return items.items, nil
			}
			continue
		}
		if err := p.handle(m); err != nil {
			return nil, err
		}
	}
	return nil, ErrNoAnswer
}

func (p *Peer) Headers(r HeadersRequest) ([][]byte, error) {
	r.Amount = min(r.Amount, ItemsMax)
	id := p.nextID
	p.nextID++
	return p.roundTrip(request{code: ethGetBlockHeaders, reply: ethBlockHeaders, id: id, payload: encodeHeadersRequest(id, r)})
}

func (p *Peer) Bodies(hashes []primitives.Hash) ([][]byte, error) {
	if len(hashes) > ItemsMax {
		return nil, ErrTooMany
	}
	id := p.nextID
	p.nextID++
	return p.roundTrip(request{code: ethGetBlockBodies, reply: ethBlockBodies, id: id, payload: encodeHashes(id, hashes)})
}

func (p *Peer) Serve(messages int) error {
	for range min(messages, messagesPerReplyMax) {
		m, err := p.conn.ReadMsg()
		if err != nil {
			return err
		}
		if err := p.handle(m); err != nil {
			return err
		}
	}
	return nil
}

func (p *Peer) handle(m rlpx.Msg) error {
	switch m.Code {
	case codePing:
		return p.conn.WriteMsg(rlpx.Msg{Code: codePong, Payload: []byte{0xc0}})
	case codeDisc:
		return &DisconnectError{Reason: decodeDisconnect(m.Payload)}
	case ethOffset + ethBlockRangeUpdate:
		r, err := DecodeBlockRange(m.Payload)
		if err != nil {
			return err
		}
		p.blocks = r
	case ethOffset + ethGetBlockHeaders:
		return p.serveHeaders(m.Payload)
	case ethOffset + ethGetBlockBodies:
		return p.serveBodies(m.Payload)
	}
	return nil
}

func (p *Peer) serveHeaders(payload []byte) error {
	q, err := decodeHeadersRequest(payload)
	if err != nil {
		return err
	}
	var items [][]byte
	if p.config.Store != nil {
		items = collectHeaders(p.config.Store, q.request)
	}
	return p.conn.WriteMsg(rlpx.Msg{Code: ethOffset + ethBlockHeaders, Payload: encodeItems(q.id, items)})
}

func collectHeaders(store Store, r HeadersRequest) [][]byte {
	number := r.Number
	if r.ByHash {
		n, ok := store.HeaderNumber(r.Hash)
		if !ok {
			return nil
		}
		number = n
	}
	var items [][]byte
	size := 0
	for i := range min(r.Amount, ItemsMax) {
		if i > 0 && r.Reverse && number < r.Skip+1 {
			break
		}
		if i > 0 && r.Reverse {
			number -= r.Skip + 1
		} else if i > 0 {
			number += r.Skip + 1
		}
		h, ok := store.Header(number)
		if !ok || size > replyBytesSoftMax {
			break
		}
		items = append(items, h)
		size += len(h)
	}
	return items
}

func (p *Peer) serveBodies(payload []byte) error {
	q, err := decodeHashes(payload)
	if err != nil {
		return err
	}
	var items [][]byte
	size := 0
	for _, h := range q.hashes {
		if p.config.Store == nil || size > replyBytesSoftMax {
			break
		}
		body, ok := p.config.Store.Body(h)
		if !ok {
			break
		}
		items = append(items, body)
		size += len(body)
	}
	return p.conn.WriteMsg(rlpx.Msg{Code: ethOffset + ethBlockBodies, Payload: encodeItems(q.id, items)})
}
