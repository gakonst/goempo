package rlpx

import (
	"encoding/binary"
	"errors"
	"hash"
	stdio "io"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"golang.org/x/crypto/sha3"
)

const (
	NonceLength      = 32
	handshakeVersion = 4
	paddingMin       = 100
	paddingSpread    = 200
	PacketMax        = 4096
	keyTries         = 16
)

const _ = uint(PacketMax - paddingMin - paddingSpread - eciesOverhead)

var (
	ErrPacket       = errors.New("rlpx: malformed handshake packet")
	ErrRole         = errors.New("rlpx: handshake step does not fit this side")
	ErrIncomplete   = errors.New("rlpx: handshake is not complete")
	ErrKeyEntropy   = errors.New("rlpx: entropy gave no valid private key")
	ErrRemoteStatic = errors.New("rlpx: remote static key does not match")
)

type Nonce [NonceLength]byte

type Keys struct {
	Static    secp256k1.PrivateKey
	Ephemeral secp256k1.PrivateKey
	Nonce     Nonce
}

type Handshake struct {
	initiator       bool
	keys            Keys
	entropy         stdio.Reader
	remoteStatic    secp256k1.PublicKey
	remoteEphemeral secp256k1.PublicKey
	remoteNonce     Nonce
	auth            []byte
	ack             []byte
}

type Secrets struct {
	AES     [32]byte
	MAC     [32]byte
	Egress  hash.Hash
	Ingress hash.Hash
}

func NewInitiator(keys Keys, remote secp256k1.PublicKey, entropy stdio.Reader) *Handshake {
	return &Handshake{initiator: true, keys: keys, entropy: entropy, remoteStatic: remote}
}

func NewRecipient(keys Keys, entropy stdio.Reader) *Handshake {
	return &Handshake{keys: keys, entropy: entropy}
}

func GenerateKey(entropy stdio.Reader) (secp256k1.PrivateKey, error) {
	var key secp256k1.PrivateKey
	for range keyTries {
		if _, err := stdio.ReadFull(entropy, key[:]); err != nil {
			return key, err
		}
		if _, err := key.PublicKey(); err == nil {
			return key, nil
		}
	}
	return key, ErrKeyEntropy
}

func GenerateKeys(entropy stdio.Reader, static secp256k1.PrivateKey) (Keys, error) {
	keys := Keys{Static: static}
	eph, err := GenerateKey(entropy)
	if err != nil {
		return keys, err
	}
	keys.Ephemeral = eph
	_, err = stdio.ReadFull(entropy, keys.Nonce[:])
	return keys, err
}

func (h *Handshake) RemoteStatic() secp256k1.PublicKey { return h.remoteStatic }

func (h *Handshake) RemoteEphemeral() secp256k1.PublicKey { return h.remoteEphemeral }

func (h *Handshake) RemoteNonce() Nonce { return h.remoteNonce }

func (h *Handshake) signedMessage(remoteStatic secp256k1.PublicKey, nonce Nonce) (primitives.Hash, error) {
	shared, err := secp256k1.SharedSecret(h.keys.Static, remoteStatic)
	if err != nil {
		return primitives.Hash{}, err
	}
	var msg primitives.Hash
	for i := range msg {
		msg[i] = shared[i] ^ nonce[i]
	}
	return msg, nil
}

func (h *Handshake) seal(body []byte) ([]byte, error) {
	var pad [1]byte
	if _, err := stdio.ReadFull(h.entropy, pad[:]); err != nil {
		return nil, err
	}
	padding := make([]byte, paddingMin+int(pad[0])%paddingSpread)
	if _, err := stdio.ReadFull(h.entropy, padding); err != nil {
		return nil, err
	}
	plain := append(body, padding...)
	var s Sealer
	eph, err := GenerateKey(h.entropy)
	if err != nil {
		return nil, err
	}
	s.Ephemeral = eph
	if _, err := stdio.ReadFull(h.entropy, s.IV[:]); err != nil {
		return nil, err
	}
	prefix := binary.BigEndian.AppendUint16(nil, uint16(len(plain)+eciesOverhead))
	sealed, err := Seal(h.remoteStatic, Envelope{Prefix: prefix, Body: plain}, s)
	if err != nil {
		return nil, err
	}
	return append(prefix, sealed...), nil
}

func (h *Handshake) open(packet []byte) (rlp.Stream, error) {
	if len(packet) < 2 || len(packet) > PacketMax {
		return rlp.Stream{}, ErrPacket
	}
	if int(binary.BigEndian.Uint16(packet)) != len(packet)-2 {
		return rlp.Stream{}, ErrPacket
	}
	plain, err := Open(h.keys.Static, Envelope{Prefix: packet[:2], Body: packet[2:]})
	if err != nil {
		return rlp.Stream{}, err
	}
	outer := rlp.NewStream(plain)
	return outer.List()
}

func (h *Handshake) WriteAuth() ([]byte, error) {
	if !h.initiator || h.auth != nil {
		return nil, ErrRole
	}
	msg, err := h.signedMessage(h.remoteStatic, h.keys.Nonce)
	if err != nil {
		return nil, err
	}
	sig, err := secp256k1.Sign(msg, h.keys.Ephemeral)
	if err != nil {
		return nil, err
	}
	pub, err := h.keys.Static.PublicKey()
	if err != nil {
		return nil, err
	}
	sigBytes := sig.Bytes()
	e := rlp.NewEncoder(256)
	e.ListStart()
	e.WriteBytes(sigBytes[:])
	e.WriteBytes(pub[:])
	e.WriteBytes(h.keys.Nonce[:])
	e.WriteUint64(handshakeVersion)
	e.ListEnd()
	packet, err := h.seal(append([]byte(nil), e.Encoded()...))
	if err != nil {
		return nil, err
	}
	h.auth = packet
	return packet, nil
}

func (h *Handshake) ReadAuth(packet []byte) error {
	if h.initiator || h.auth != nil {
		return ErrRole
	}
	list, err := h.open(packet)
	if err != nil {
		return err
	}
	var sigBytes [secp256k1.SignatureLength]byte
	if err := list.BytesFixed(sigBytes[:]); err != nil {
		return errors.Join(ErrPacket, err)
	}
	if err := list.BytesFixed(h.remoteStatic[:]); err != nil {
		return errors.Join(ErrPacket, err)
	}
	if err := list.BytesFixed(h.remoteNonce[:]); err != nil {
		return errors.Join(ErrPacket, err)
	}
	msg, err := h.signedMessage(h.remoteStatic, h.remoteNonce)
	if err != nil {
		return err
	}
	sig, ok := secp256k1.SignatureFromBytes(sigBytes[:])
	if !ok {
		return ErrPacket
	}
	h.remoteEphemeral, err = secp256k1.RecoverPublicKey(msg, sig)
	if err != nil {
		return err
	}
	h.auth = append([]byte(nil), packet...)
	return nil
}

func (h *Handshake) WriteAck() ([]byte, error) {
	if h.initiator || h.auth == nil || h.ack != nil {
		return nil, ErrRole
	}
	pub, err := h.keys.Ephemeral.PublicKey()
	if err != nil {
		return nil, err
	}
	e := rlp.NewEncoder(128)
	e.ListStart()
	e.WriteBytes(pub[:])
	e.WriteBytes(h.keys.Nonce[:])
	e.WriteUint64(handshakeVersion)
	e.ListEnd()
	packet, err := h.seal(append([]byte(nil), e.Encoded()...))
	if err != nil {
		return nil, err
	}
	h.ack = packet
	return packet, nil
}

func (h *Handshake) ReadAck(packet []byte) error {
	if !h.initiator || h.ack != nil {
		return ErrRole
	}
	list, err := h.open(packet)
	if err != nil {
		return err
	}
	if err := list.BytesFixed(h.remoteEphemeral[:]); err != nil {
		return errors.Join(ErrPacket, err)
	}
	if err := list.BytesFixed(h.remoteNonce[:]); err != nil {
		return errors.Join(ErrPacket, err)
	}
	if !h.remoteEphemeral.Valid() {
		return ErrPacket
	}
	h.ack = append([]byte(nil), packet...)
	return nil
}

type Transcript struct {
	Auth []byte
	Ack  []byte
}

func (h *Handshake) SetTranscript(t Transcript) {
	h.auth = append([]byte(nil), t.Auth...)
	h.ack = append([]byte(nil), t.Ack...)
}

func (h *Handshake) Secrets() (Secrets, error) {
	var s Secrets
	if h.auth == nil || h.ack == nil {
		return s, ErrIncomplete
	}
	ephShared, err := secp256k1.SharedSecret(h.keys.Ephemeral, h.remoteEphemeral)
	if err != nil {
		return s, err
	}
	initiatorNonce, recipientNonce := h.keys.Nonce, h.remoteNonce
	own, remote := h.auth, h.ack
	if !h.initiator {
		initiatorNonce, recipientNonce = h.remoteNonce, h.keys.Nonce
		own, remote = h.ack, h.auth
	}
	nonces := keccak(recipientNonce[:], initiatorNonce[:])
	shared := keccak(ephShared[:], nonces[:])
	s.AES = keccak(ephShared[:], shared[:])
	s.MAC = keccak(ephShared[:], s.AES[:])
	s.Egress = macState(s.MAC, h.remoteNonce, own)
	s.Ingress = macState(s.MAC, h.keys.Nonce, remote)
	return s, nil
}

func keccak(parts ...[]byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

func macState(mac [32]byte, nonce Nonce, packet []byte) hash.Hash {
	var x [32]byte
	for i := range x {
		x[i] = mac[i] ^ nonce[i]
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(x[:])
	h.Write(packet)
	return h
}
