package cwp2p

import (
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"math"

	"github.com/kapetan-io/tiger/assert"
	"golang.org/x/crypto/chacha20poly1305"
)

type CupsVersion uint8

const (
	CupsV0 CupsVersion = 0
	CupsV1 CupsVersion = 1
)

type Role bool

const (
	RoleDialer   Role = true
	RoleListener Role = false
)

const (
	TagSize        = chacha20poly1305.Overhead
	keySize        = chacha20poly1305.KeySize
	nonceSize      = chacha20poly1305.NonceSize
	noiseSize      = 64
	v1HeaderPlain  = 4
	v1HeaderSize   = v1HeaderPlain + TagSize
	MessageSizeMax = 1 << 30
	cupsNamespace  = "_COMMONWARE_STREAM_CUPS"
	labelCipherL2D = "cipher_l2d"
	labelCipherD2L = "cipher_d2l"
)

const _ = uint(math.MaxUint32 - MessageSizeMax - TagSize)

const _ = uint(noiseSize - keySize)

var (
	ErrSeal       = errors.New("cwp2p: seal failed")
	ErrOpen       = errors.New("cwp2p: open failed")
	ErrTooLarge   = errors.New("cwp2p: frame too large")
	ErrStreamDone = errors.New("cwp2p: stream closed after an earlier failure")
)

type Cipher struct {
	aead  cipher.AEAD
	nonce [nonceSize]byte
	spent bool
}

func newCipher(t *Transcript, label string) *Cipher {
	var block [noiseSize]byte
	t.Noise(label, block[:])
	aead, err := chacha20poly1305.New(block[:keySize])
	assert.NoError(err, "cwp2p: chacha20poly1305 key has the right size")
	return &Cipher{aead: aead}
}

func (c *Cipher) next() ([nonceSize]byte, bool) {
	n := c.nonce
	if c.spent {
		return n, false
	}
	for i := range nonceSize {
		c.nonce[i]++
		if c.nonce[i] != 0 {
			return n, true
		}
	}
	c.spent = true
	return n, true
}

func (c *Cipher) Seal(plaintext []byte) ([]byte, error) {
	n, ok := c.next()
	if !ok {
		return nil, ErrSeal
	}
	return c.aead.Seal(make([]byte, 0, len(plaintext)+TagSize), n[:], plaintext, nil), nil
}

func (c *Cipher) Open(sealed []byte) ([]byte, error) {
	n, ok := c.next()
	if !ok {
		return nil, ErrOpen
	}
	out, err := c.aead.Open(sealed[:0], n[:], sealed, nil)
	if err != nil {
		return nil, ErrOpen
	}
	return out, nil
}

type Ciphers struct {
	Send *Cipher
	Recv *Cipher
}

func DeriveCiphers(t *Transcript, version CupsVersion, role Role) Ciphers {
	if version == CupsV1 {
		t = t.Fork(cupsNamespace)
		t.Commit([]byte{modeV1})
	}
	l2d := newCipher(t, labelCipherL2D)
	d2l := newCipher(t, labelCipherD2L)
	if role == RoleDialer {
		return Ciphers{Send: d2l, Recv: l2d}
	}
	return Ciphers{Send: l2d, Recv: d2l}
}

type RecordSealer struct {
	Version        CupsVersion
	Cipher         *Cipher
	MaxMessageSize uint32
}

func (s *RecordSealer) Seal(plaintext []byte) ([]byte, error) {
	if uint64(len(plaintext)) > uint64(s.MaxMessageSize) {
		return nil, ErrTooLarge
	}
	n := uint32(len(plaintext))
	if s.Version == CupsV1 {
		header, err := s.Cipher.Seal(binary.BigEndian.AppendUint32(nil, n))
		if err != nil {
			return nil, err
		}
		body, err := s.Cipher.Seal(plaintext)
		if err != nil {
			return nil, err
		}
		return append(header, body...), nil
	}
	body, err := s.Cipher.Seal(plaintext)
	if err != nil {
		return nil, err
	}
	return append(AppendVarint(make([]byte, 0, varintU32Max+len(body)), uint64(len(body))), body...), nil
}
