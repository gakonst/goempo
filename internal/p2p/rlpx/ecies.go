// Package rlpx is the devp2p transport: the ECIES-encrypted EIP-8
// handshake over secp256k1, the AES-CTR frames with their keccak MACs, and
// a message connection on top of an internal/io connection.
package rlpx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
)

const (
	ivLength      = 16
	eciesKeyStart = 1
	eciesOverhead = 1 + secp256k1.PublicKeyLength + ivLength + sha256.Size
)

var ErrECIES = errors.New("rlpx: ECIES message does not decrypt")

type Envelope struct {
	Prefix []byte
	Body   []byte
}

type Sealer struct {
	Ephemeral secp256k1.PrivateKey
	IV        [ivLength]byte
}

type eciesKeys struct {
	enc []byte
	mac [sha256.Size]byte
}

func deriveECIES(shared [secp256k1.ScalarLength]byte) eciesKeys {
	var counter [4]byte
	binary.BigEndian.PutUint32(counter[:], eciesKeyStart)
	h := sha256.New()
	h.Write(counter[:])
	h.Write(shared[:])
	k := h.Sum(nil)
	return eciesKeys{enc: k[:16], mac: sha256.Sum256(k[16:32])}
}

func (k eciesKeys) tag(iv []byte, sealed Envelope) []byte {
	m := hmac.New(sha256.New, k.mac[:])
	m.Write(iv)
	m.Write(sealed.Body)
	m.Write(sealed.Prefix)
	return m.Sum(nil)
}

func Seal(remote secp256k1.PublicKey, env Envelope, s Sealer) ([]byte, error) {
	shared, err := secp256k1.SharedSecret(s.Ephemeral, remote)
	if err != nil {
		return nil, err
	}
	pub, err := s.Ephemeral.PublicKey()
	if err != nil {
		return nil, err
	}
	keys := deriveECIES(shared)
	block, err := aes.NewCipher(keys.enc)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, eciesOverhead+len(env.Body))
	out = append(out, 0x04)
	out = append(out, pub[:]...)
	out = append(out, s.IV[:]...)
	start := len(out)
	out = append(out, env.Body...)
	cipher.NewCTR(block, s.IV[:]).XORKeyStream(out[start:], env.Body)
	return append(out, keys.tag(s.IV[:], Envelope{Prefix: env.Prefix, Body: out[start:]})...), nil
}

func Open(key secp256k1.PrivateKey, env Envelope) ([]byte, error) {
	msg := env.Body
	if len(msg) < eciesOverhead || msg[0] != 0x04 {
		return nil, ErrECIES
	}
	var pub secp256k1.PublicKey
	copy(pub[:], msg[1:1+secp256k1.PublicKeyLength])
	shared, err := secp256k1.SharedSecret(key, pub)
	if err != nil {
		return nil, errors.Join(ErrECIES, err)
	}
	keys := deriveECIES(shared)
	ivStart := 1 + secp256k1.PublicKeyLength
	iv := msg[ivStart : ivStart+ivLength]
	ciphertext := msg[ivStart+ivLength : len(msg)-sha256.Size]
	tag := msg[len(msg)-sha256.Size:]
	if !hmac.Equal(tag, keys.tag(iv, Envelope{Prefix: env.Prefix, Body: ciphertext})) {
		return nil, ErrECIES
	}
	block, err := aes.NewCipher(keys.enc)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCTR(block, iv).XORKeyStream(plain, ciphertext)
	return plain, nil
}
