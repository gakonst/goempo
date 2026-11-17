package rlpx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"errors"
	"hash"

	"github.com/gakonst/goempo/internal/p2p/snappy"
)

const (
	HeaderLength = 32
	blockLength  = 16
	macLength    = 16
	FrameSizeMax = 1<<24 - 1
)

const _ = uint(snappy.DecodedMax - FrameSizeMax)

var (
	ErrMAC       = errors.New("rlpx: frame MAC does not match")
	ErrFrameSize = errors.New("rlpx: frame size out of range")
)

type Codec struct {
	egress     cipher.Stream
	ingress    cipher.Stream
	macBlock   cipher.Block
	egressMAC  hash.Hash
	ingressMAC hash.Hash
}

func NewCodec(s Secrets) (*Codec, error) {
	encBlock, err := aes.NewCipher(s.AES[:])
	if err != nil {
		return nil, err
	}
	macBlock, err := aes.NewCipher(s.MAC[:])
	if err != nil {
		return nil, err
	}
	var iv [blockLength]byte
	return &Codec{
		egress:     cipher.NewCTR(encBlock, iv[:]),
		ingress:    cipher.NewCTR(encBlock, iv[:]),
		macBlock:   macBlock,
		egressMAC:  s.Egress,
		ingressMAC: s.Ingress,
	}, nil
}

func padded(n int) int {
	return (n + blockLength - 1) / blockLength * blockLength
}

func BodyLength(size int) int {
	return padded(size) + macLength
}

func (c *Codec) updateMAC(mac hash.Hash, seed []byte) []byte {
	var buf [blockLength]byte
	sum := mac.Sum(nil)
	c.macBlock.Encrypt(buf[:], sum[:blockLength])
	for i := range buf {
		buf[i] ^= seed[i]
	}
	mac.Write(buf[:])
	return mac.Sum(nil)[:macLength]
}

func (c *Codec) Seal(payload []byte) ([]byte, error) {
	if len(payload) > FrameSizeMax {
		return nil, ErrFrameSize
	}
	out := make([]byte, HeaderLength+BodyLength(len(payload)))
	header := out[:blockLength]
	header[0] = byte(len(payload) >> 16)
	header[1] = byte(len(payload) >> 8)
	header[2] = byte(len(payload))
	header[3], header[4], header[5] = 0xc2, 0x80, 0x80
	c.egress.XORKeyStream(header, header)
	copy(out[blockLength:HeaderLength], c.updateMAC(c.egressMAC, header))
	body := out[HeaderLength : HeaderLength+padded(len(payload))]
	copy(body, payload)
	c.egress.XORKeyStream(body, body)
	c.egressMAC.Write(body)
	seed := c.egressMAC.Sum(nil)
	copy(out[HeaderLength+len(body):], c.updateMAC(c.egressMAC, seed[:blockLength]))
	return out, nil
}

func (c *Codec) OpenHeader(header []byte) (int, error) {
	if len(header) != HeaderLength {
		return 0, ErrFrameSize
	}
	want := c.updateMAC(c.ingressMAC, header[:blockLength])
	if subtle.ConstantTimeCompare(want, header[blockLength:]) != 1 {
		return 0, ErrMAC
	}
	var plain [blockLength]byte
	c.ingress.XORKeyStream(plain[:], header[:blockLength])
	return int(plain[0])<<16 | int(plain[1])<<8 | int(plain[2]), nil
}

func (c *Codec) OpenBody(size int, body []byte) ([]byte, error) {
	if size < 0 || size > FrameSizeMax || len(body) != BodyLength(size) {
		return nil, ErrFrameSize
	}
	ciphertext := body[:padded(size)]
	c.ingressMAC.Write(ciphertext)
	seed := c.ingressMAC.Sum(nil)
	want := c.updateMAC(c.ingressMAC, seed[:blockLength])
	if subtle.ConstantTimeCompare(want, body[len(ciphertext):]) != 1 {
		return nil, ErrMAC
	}
	plain := make([]byte, len(ciphertext))
	c.ingress.XORKeyStream(plain, ciphertext)
	return plain[:size], nil
}
