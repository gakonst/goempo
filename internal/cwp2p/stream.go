package cwp2p

import (
	"bufio"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"

	gio "github.com/gakonst/goempo/internal/io"
)

var ErrPeerRejected = errors.New("cwp2p: peer rejected by bouncer")

const nanosPerMilli = 1_000_000

type StreamConfig struct {
	Sake                  SakeVersion
	Cups                  CupsVersion
	Namespace             []byte
	Key                   ed25519.PrivateKey
	Clock                 gio.Clock
	MaxAgeMs              uint64
	SynchronyMs           uint64
	HandshakeTimeoutNanos uint64
	Entropy               io.Reader
	MaxMessageSize        uint32
}

func (c *StreamConfig) context(peer PublicKey) *HandshakeContext {
	return &HandshakeContext{
		Version:     c.Sake,
		Namespace:   c.Namespace,
		NowMs:       c.Clock.RealtimeNanos() / nanosPerMilli,
		MaxAgeMs:    c.MaxAgeMs,
		SynchronyMs: c.SynchronyMs,
		Key:         c.Key,
		Peer:        peer,
		Entropy:     c.Entropy,
	}
}

type Sender struct {
	w      io.Writer
	sealer RecordSealer
	buf    []byte
	failed bool
}

type Receiver struct {
	r              *bufio.Reader
	cipher         *Cipher
	version        CupsVersion
	maxMessageSize uint32
	failed         bool
}

type Stream struct {
	Peer     PublicKey
	Sender   *Sender
	Receiver *Receiver
}

func writeFrame(w io.Writer, payload []byte) error {
	out := AppendVarint(make([]byte, 0, varintU32Max+len(payload)), uint64(len(payload)))
	_, err := w.Write(append(out, payload...))
	return err
}

func readLength(r *bufio.Reader) (uint32, error) {
	var d varintU32Decoder
	for range varintU32Max {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if err := d.feed(b); err != nil {
			return 0, err
		}
		if d.done {
			return d.value, nil
		}
	}
	return 0, ErrVarint
}

func readFrame(r *bufio.Reader, sizeMax uint32) ([]byte, error) {
	n, err := readLength(r)
	if err != nil {
		return nil, err
	}
	if n > sizeMax {
		return nil, ErrTooLarge
	}
	out := make([]byte, n)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

func readExact(r *bufio.Reader, size int) ([]byte, error) {
	out, err := readFrame(r, uint32(size))
	if err != nil {
		return nil, err
	}
	if len(out) != size {
		return nil, ErrHandshakeFormat
	}
	return out, nil
}

func (s Stream) established(conn gio.Conn, r *bufio.Reader, c *StreamConfig, cs Ciphers) (*Stream, error) {
	if err := conn.SetReadTimeout(0); err != nil {
		return nil, err
	}
	s.Sender = &Sender{w: conn, sealer: RecordSealer{Version: c.Cups, Cipher: cs.Send, MaxMessageSize: c.MaxMessageSize}}
	s.Receiver = &Receiver{r: r, cipher: cs.Recv, version: c.Cups, maxMessageSize: c.MaxMessageSize}
	return &s, nil
}

func DialStream(conn gio.Conn, c *StreamConfig, peer PublicKey) (*Stream, error) {
	if err := conn.SetReadTimeout(c.HandshakeTimeoutNanos); err != nil {
		return nil, err
	}
	r := bufio.NewReader(conn)
	me := PublicKeyOf(c.Key)
	if err := writeFrame(conn, me[:]); err != nil {
		return nil, err
	}
	state, err := DialStart(c.context(peer))
	if err != nil {
		return nil, err
	}
	if err := writeFrame(conn, state.Syn.Encode()); err != nil {
		return nil, err
	}
	raw, err := readExact(r, SynAckSize)
	if err != nil {
		return nil, err
	}
	synAck, err := DecodeSynAck(raw)
	if err != nil {
		return nil, err
	}
	ack, err := state.Finish(synAck)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(conn, ack.Confirmation[:]); err != nil {
		return nil, err
	}
	return Stream{Peer: peer}.established(conn, r, c, DeriveCiphers(state.Transcript(), c.Cups, RoleDialer))
}

type Bouncer func(PublicKey) bool

func AcceptStream(conn gio.Conn, c *StreamConfig, bouncer Bouncer) (*Stream, error) {
	if err := conn.SetReadTimeout(c.HandshakeTimeoutNanos); err != nil {
		return nil, err
	}
	r := bufio.NewReader(conn)
	raw, err := readExact(r, PublicKeySize)
	if err != nil {
		return nil, err
	}
	peer := PublicKey(raw)
	if !bouncer(peer) {
		return nil, ErrPeerRejected
	}
	raw, err = readExact(r, SynSize)
	if err != nil {
		return nil, err
	}
	syn, err := DecodeSyn(raw)
	if err != nil {
		return nil, err
	}
	state, err := ListenStart(c.context(peer), syn)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(conn, state.SynAck.Encode()); err != nil {
		return nil, err
	}
	raw, err = readExact(r, AckSize)
	if err != nil {
		return nil, err
	}
	t, err := state.Finish(Ack{Confirmation: Summary(raw)})
	if err != nil {
		return nil, err
	}
	return Stream{Peer: peer}.established(conn, r, c, DeriveCiphers(t, c.Cups, RoleListener))
}

func (s *Sender) SendMany(msgs [][]byte) error {
	if s.failed {
		return ErrStreamDone
	}
	s.buf = s.buf[:0]
	for _, m := range msgs {
		record, err := s.sealer.Seal(m)
		if err != nil {
			s.failed = true
			return err
		}
		s.buf = append(s.buf, record...)
	}
	if _, err := s.w.Write(s.buf); err != nil {
		s.failed = true
		return err
	}
	return nil
}

func (s *Sender) Send(msg []byte) error {
	return s.SendMany([][]byte{msg})
}

func (r *Receiver) Recv() ([]byte, error) {
	if r.failed {
		return nil, ErrStreamDone
	}
	out, err := r.recv()
	if err != nil {
		r.failed = true
	}
	return out, err
}

func (r *Receiver) recv() ([]byte, error) {
	if r.version == CupsV1 {
		header := make([]byte, v1HeaderSize)
		if _, err := io.ReadFull(r.r, header); err != nil {
			return nil, err
		}
		plain, err := r.cipher.Open(header)
		if err != nil {
			return nil, err
		}
		n := binary.BigEndian.Uint32(plain)
		if n > r.maxMessageSize {
			return nil, ErrTooLarge
		}
		body := make([]byte, uint64(n)+TagSize)
		if _, err := io.ReadFull(r.r, body); err != nil {
			return nil, err
		}
		return r.cipher.Open(body)
	}
	sealed, err := readFrame(r.r, r.maxMessageSize+TagSize)
	if err != nil {
		return nil, err
	}
	if len(sealed) < TagSize {
		return nil, ErrOpen
	}
	return r.cipher.Open(sealed)
}
