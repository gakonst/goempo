package cwp2p

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
)

type SakeVersion uint8

const (
	SakeV0 SakeVersion = 0
	SakeV1 SakeVersion = 1
)

const (
	PublicKeySize = ed25519.PublicKeySize
	SignatureSize = ed25519.SignatureSize
	ephemeralSize = 32
	timeSize      = 8
	SynSize       = timeSize + ephemeralSize + SignatureSize
	SynAckSize    = SynSize + SummarySize
	AckSize       = SummarySize
	modeV1        = 1
)

var (
	ErrTimestamp       = errors.New("cwp2p: handshake timestamp outside the accepted window")
	ErrSignature       = errors.New("cwp2p: handshake signature invalid")
	ErrEphemeral       = errors.New("cwp2p: handshake ephemeral key not contributory")
	ErrConfirmation    = errors.New("cwp2p: handshake confirmation mismatch")
	ErrHandshakeFormat = errors.New("cwp2p: handshake message malformed")
)

const (
	labelConfirmationL2D = "confirmation_l2d"
	labelConfirmationD2L = "confirmation_d2l"
	sakeNamespaceV0      = "_COMMONWARE_CRYPTOGRAPHY_HANDSHAKE"
	sakeNamespaceV1      = "_COMMONWARE_CRYPTOGRAPHY_SAKE"
)

type PublicKey [PublicKeySize]byte

type Syn struct {
	TimeMs    uint64
	Ephemeral [ephemeralSize]byte
	Signature [SignatureSize]byte
}

type SynAck struct {
	Syn
	Confirmation Summary
}

type Ack struct {
	Confirmation Summary
}

func (s *Syn) Encode() []byte {
	out := binary.BigEndian.AppendUint64(make([]byte, 0, SynAckSize), s.TimeMs)
	out = append(out, s.Ephemeral[:]...)
	return append(out, s.Signature[:]...)
}

func DecodeSyn(b []byte) (Syn, error) {
	var s Syn
	if len(b) != SynSize {
		return s, ErrHandshakeFormat
	}
	s.TimeMs = binary.BigEndian.Uint64(b)
	copy(s.Ephemeral[:], b[timeSize:])
	copy(s.Signature[:], b[timeSize+ephemeralSize:])
	return s, nil
}

func (s *SynAck) Encode() []byte {
	return append(s.Syn.Encode(), s.Confirmation[:]...)
}

func DecodeSynAck(b []byte) (SynAck, error) {
	var s SynAck
	if len(b) != SynAckSize {
		return s, ErrHandshakeFormat
	}
	syn, err := DecodeSyn(b[:SynSize])
	if err != nil {
		return s, err
	}
	s.Syn = syn
	copy(s.Confirmation[:], b[SynSize:])
	return s, nil
}

type HandshakeContext struct {
	Version     SakeVersion
	Namespace   []byte
	NowMs       uint64
	MaxAgeMs    uint64
	SynchronyMs uint64
	Key         ed25519.PrivateKey
	Peer        PublicKey
	Entropy     io.Reader
}

func (c *HandshakeContext) accepts(ms uint64) bool {
	from := uint64(0)
	if c.NowMs > c.MaxAgeMs {
		from = c.NowMs - c.MaxAgeMs
	}
	until := c.NowMs + c.SynchronyMs
	if until < c.NowMs {
		until = ^uint64(0)
	}
	return ms >= from && ms < until
}

func (c *HandshakeContext) transcript() *Transcript {
	if c.Version == SakeV1 {
		t := NewTranscript(c.Namespace, TranscriptV1).Fork(sakeNamespaceV1)
		t.Commit([]byte{modeV1})
		return t
	}
	return NewTranscript(c.Namespace, TranscriptV0).Fork(sakeNamespaceV0)
}

func PublicKeyOf(key ed25519.PrivateKey) PublicKey {
	var out PublicKey
	copy(out[:], key[ed25519.SeedSize:])
	return out
}

func SignSummary(key ed25519.PrivateKey, s Summary) [SignatureSize]byte {
	msg := append([]byte{0}, s[:]...)
	return [SignatureSize]byte(ed25519.Sign(key, msg))
}

func VerifySummary(pub PublicKey, s Summary, sig [SignatureSize]byte) bool {
	msg := append([]byte{0}, s[:]...)
	return ed25519.Verify(pub[:], msg, sig[:])
}

func timeBytes(ms uint64) []byte {
	return binary.BigEndian.AppendUint64(nil, ms)
}

func newEphemeral(entropy io.Reader) (*ecdh.PrivateKey, error) {
	var seed [ephemeralSize]byte
	if _, err := io.ReadFull(entropy, seed[:]); err != nil {
		return nil, err
	}
	return ecdh.X25519().NewPrivateKey(seed[:])
}

func commitShared(t *Transcript, esk *ecdh.PrivateKey, peer [ephemeralSize]byte) error {
	pub, err := ecdh.X25519().NewPublicKey(peer[:])
	if err != nil {
		return ErrEphemeral
	}
	shared, err := esk.ECDH(pub)
	if err != nil {
		return ErrEphemeral
	}
	t.Commit(shared)
	return nil
}

type DialHandshake struct {
	Syn        Syn
	ctx        HandshakeContext
	esk        *ecdh.PrivateKey
	transcript *Transcript
}

func DialStart(c *HandshakeContext) (*DialHandshake, error) {
	esk, err := newEphemeral(c.Entropy)
	if err != nil {
		return nil, err
	}
	t := c.transcript()
	dialer := PublicKeyOf(c.Key)
	t.Commit(timeBytes(c.NowMs)).Commit(c.Peer[:])
	if c.Version == SakeV1 {
		t.Commit(dialer[:])
	}
	epk := [ephemeralSize]byte(esk.PublicKey().Bytes())
	syn := Syn{TimeMs: c.NowMs, Ephemeral: epk}
	syn.Signature = SignSummary(c.Key, t.Commit(epk[:]).Summarize())
	if c.Version == SakeV0 {
		t.Commit(dialer[:])
	}
	return &DialHandshake{Syn: syn, ctx: *c, esk: esk, transcript: t}, nil
}

func (d *DialHandshake) Finish(msg SynAck) (Ack, error) {
	if !d.ctx.accepts(msg.TimeMs) {
		return Ack{}, ErrTimestamp
	}
	t := d.transcript
	t.Commit(timeBytes(msg.TimeMs)).Commit(msg.Ephemeral[:])
	if !VerifySummary(d.ctx.Peer, t.Summarize(), msg.Signature) {
		return Ack{}, ErrSignature
	}
	if err := commitShared(t, d.esk, msg.Ephemeral); err != nil {
		return Ack{}, err
	}
	l2d := t.Fork(labelConfirmationL2D).Summarize()
	if subtle.ConstantTimeCompare(l2d[:], msg.Confirmation[:]) != 1 {
		return Ack{}, ErrConfirmation
	}
	return Ack{Confirmation: t.Fork(labelConfirmationD2L).Summarize()}, nil
}

func (d *DialHandshake) Transcript() *Transcript {
	return d.transcript
}

type ListenHandshake struct {
	SynAck     SynAck
	transcript *Transcript
}

func ListenStart(c *HandshakeContext, msg Syn) (*ListenHandshake, error) {
	if !c.accepts(msg.TimeMs) {
		return nil, ErrTimestamp
	}
	t := c.transcript()
	listener := PublicKeyOf(c.Key)
	t.Commit(timeBytes(msg.TimeMs)).Commit(listener[:])
	if c.Version == SakeV1 {
		t.Commit(c.Peer[:])
	}
	t.Commit(msg.Ephemeral[:])
	if !VerifySummary(c.Peer, t.Summarize(), msg.Signature) {
		return nil, ErrSignature
	}
	if c.Version == SakeV0 {
		t.Commit(c.Peer[:])
	}
	esk, err := newEphemeral(c.Entropy)
	if err != nil {
		return nil, err
	}
	var out SynAck
	epk := [ephemeralSize]byte(esk.PublicKey().Bytes())
	out.TimeMs, out.Ephemeral = c.NowMs, epk
	out.Signature = SignSummary(c.Key, t.Commit(timeBytes(c.NowMs)).Commit(epk[:]).Summarize())
	if err := commitShared(t, esk, msg.Ephemeral); err != nil {
		return nil, err
	}
	out.Confirmation = t.Fork(labelConfirmationL2D).Summarize()
	return &ListenHandshake{SynAck: out, transcript: t}, nil
}

func (l *ListenHandshake) Finish(msg Ack) (*Transcript, error) {
	d2l := l.transcript.Fork(labelConfirmationD2L).Summarize()
	if subtle.ConstantTimeCompare(d2l[:], msg.Confirmation[:]) != 1 {
		return nil, ErrConfirmation
	}
	return l.transcript, nil
}
