package validator

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"strings"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
)

const (
	SeedBytes       = ed25519.SeedSize
	PublicKeyBytes  = ed25519.PublicKeySize
	SignatureBytes  = ed25519.SignatureSize
	keyFileBytesMax = 4096
	keyFileMode     = fs.FileMode(0o600)
	varintMore      = 0x80
	varintLow       = 0x7f
	varintShift     = 7
	varintU32Max    = 5
	u32Bits         = 32
	hexPrefixBytes  = 2
	shareHexBytes   = hexPrefixBytes + 2*(varintU32Max+bls12381.ScalarLength)
)

const _ = uint(varintU32Max*varintShift - u32Bits)

const _ = uint(keyFileBytesMax - 2*shareHexBytes)

type Overwrite bool

const (
	NoOverwrite Overwrite = false
	Replace     Overwrite = true
)

type Namespace []byte

type Message []byte

var (
	ErrKeyHex      = errors.New("validator: key file is not hex")
	ErrKeyLength   = errors.New("validator: key has the wrong length")
	ErrKeyFile     = errors.New("validator: key file is too large")
	ErrShareIndex  = errors.New("validator: share index is malformed")
	ErrShareScalar = errors.New("validator: share scalar is malformed")
	ErrKeyExists   = errors.New("validator: key file exists")
)

type SigningKey struct {
	seed [SeedBytes]byte
}

func NewSigningKey(rng io.Reader) (SigningKey, error) {
	var k SigningKey
	if _, err := io.ReadFull(rng, k.seed[:]); err != nil {
		return SigningKey{}, err
	}
	return k, nil
}

func SigningKeyFromSeed(seed []byte) (SigningKey, error) {
	if len(seed) != SeedBytes {
		return SigningKey{}, ErrKeyLength
	}
	var k SigningKey
	copy(k.seed[:], seed)
	return k, nil
}

func (k SigningKey) Private() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(k.seed[:]) }

func (k SigningKey) Public() [PublicKeyBytes]byte {
	var out [PublicKeyBytes]byte
	copy(out[:], k.Private().Public().(ed25519.PublicKey))
	return out
}

func (k SigningKey) Seed() [SeedBytes]byte { return k.seed }

func (k SigningKey) Sign(ns Namespace, msg Message) [SignatureBytes]byte {
	var out [SignatureBytes]byte
	copy(out[:], ed25519.Sign(k.Private(), UnionUnique(ns, msg)))
	return out
}

func (k SigningKey) Hex() string { return "0x" + hex.EncodeToString(k.seed[:]) }

type Signed struct {
	Public    [PublicKeyBytes]byte
	Namespace Namespace
	Message   Message
	Signature []byte
}

func (s Signed) Verify() bool {
	if len(s.Signature) != SignatureBytes {
		return false
	}
	return ed25519.Verify(s.Public[:], UnionUnique(s.Namespace, s.Message), s.Signature)
}

func UnionUnique(ns Namespace, msg Message) []byte {
	out := make([]byte, 0, varintU32Max+len(ns)+len(msg))
	out = appendVarint(out, uint64(len(ns)))
	out = append(out, ns...)
	return append(out, msg...)
}

func appendVarint(out []byte, v uint64) []byte {
	for range 2 * varintU32Max {
		if v <= varintLow {
			return append(out, byte(v))
		}
		out = append(out, byte(v&varintLow)|varintMore)
		v >>= varintShift
	}
	return out
}

type varint struct {
	value uint64
	size  int
}

func readVarintU32(b []byte) (varint, error) {
	var v uint64
	for i := range varintU32Max {
		if i >= len(b) {
			return varint{}, ErrShareIndex
		}
		c := b[i]
		v |= uint64(c&varintLow) << (varintShift * uint(i))
		if c&varintMore != 0 {
			continue
		}
		if c == 0 {
			if i > 0 {
				return varint{}, ErrShareIndex
			}
		}
		if v > math.MaxUint32 {
			return varint{}, ErrShareIndex
		}
		return varint{value: v, size: i + 1}, nil
	}
	return varint{}, ErrShareIndex
}

func DecodeHex(text string) ([]byte, error) {
	s := strings.TrimSpace(text)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, ErrKeyHex
	}
	return b, nil
}

func ParseSigningKey(text string) (SigningKey, error) {
	b, err := DecodeHex(text)
	if err != nil {
		return SigningKey{}, err
	}
	return SigningKeyFromSeed(b)
}

func readKeyFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, keyFileBytesMax+1))
	if err != nil {
		return "", err
	}
	if len(b) > keyFileBytesMax {
		return "", ErrKeyFile
	}
	return string(b), nil
}

func ReadSigningKey(path string) (SigningKey, error) {
	text, err := readKeyFile(path)
	if err != nil {
		return SigningKey{}, err
	}
	return ParseSigningKey(text)
}

func WriteKeyFile(path string, contents []byte, mode Overwrite) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if mode == Replace {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, keyFileMode)
	if errors.Is(err, fs.ErrExist) {
		return ErrKeyExists
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(contents); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(keyFileMode); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func WriteSigningKey(path string, k SigningKey, mode Overwrite) error {
	return WriteKeyFile(path, []byte(k.Hex()), mode)
}

func EncodeShare(s dkg.Share) []byte {
	out := appendVarint(nil, uint64(s.Index))
	return append(out, s.Private[:]...)
}

func DecodeShare(b []byte) (dkg.Share, error) {
	index, err := readVarintU32(b)
	if err != nil {
		return dkg.Share{}, err
	}
	if len(b)-index.size != bls12381.ScalarLength {
		return dkg.Share{}, ErrShareScalar
	}
	k, err := bls12381.ScalarFromBytes(b[index.size:])
	if err != nil {
		return dkg.Share{}, ErrShareScalar
	}
	if k.IsZero() {
		return dkg.Share{}, ErrShareScalar
	}
	return dkg.Share{Index: int(index.value), Private: k}, nil
}

func ShareHex(s dkg.Share) string { return "0x" + hex.EncodeToString(EncodeShare(s)) }

func ParseShare(text string) (dkg.Share, error) {
	b, err := DecodeHex(text)
	if err != nil {
		return dkg.Share{}, err
	}
	return DecodeShare(b)
}

func ReadShare(path string) (dkg.Share, error) {
	text, err := readKeyFile(path)
	if err != nil {
		return dkg.Share{}, err
	}
	return ParseShare(text)
}

func WriteShare(path string, s dkg.Share, mode Overwrite) error {
	return WriteKeyFile(path, []byte(ShareHex(s)), mode)
}
