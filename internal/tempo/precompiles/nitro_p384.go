package precompiles

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"math/big"
	"sync"
)

const (
	p384ScalarBytes = 48
	p384KeyBytes    = 1 + 2*p384ScalarBytes
)

const (
	p384VerifiedMax      = 1 << 12
	p384VerifiedBytesMax = 1 << 20
)

const _ = uint(p384VerifiedBytesMax - p384VerifiedMax*sha256.Size*2)

const _ = uint(nitroSignatureSize - 2*p384ScalarBytes)

type p384Memo struct {
	mu   sync.Mutex
	seen map[[sha256.Size]byte]struct{}
}

var p384Verified = p384Memo{seen: make(map[[sha256.Size]byte]struct{}, p384VerifiedMax)}

func (m *p384Memo) has(id [sha256.Size]byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.seen[id]
	return ok
}

func (m *p384Memo) add(id [sha256.Size]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.seen) >= p384VerifiedMax {
		clear(m.seen)
	}
	m.seen[id] = struct{}{}
}

func p384CheckID(in p384Check) ([sha256.Size]byte, bool) {
	key, err := in.key.Bytes()
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	h := sha256.New()
	h.Write(key)
	h.Write(in.digest)
	h.Write(in.sig)
	var id [sha256.Size]byte
	h.Sum(id[:0])
	return id, true
}

func p384Key(raw []byte) (*ecdsa.PublicKey, error) {
	if len(raw) != p384KeyBytes || raw[0] != 0x04 {
		return nil, errNitro
	}
	k, err := ecdsa.ParseUncompressedPublicKey(elliptic.P384(), raw)
	if err != nil {
		return nil, errNitro
	}
	return k, nil
}

type p384Check struct {
	key    *ecdsa.PublicKey
	digest []byte
	sig    []byte
}

func p384VerifyFixed(in p384Check) bool {
	if len(in.sig) != nitroSignatureSize {
		return false
	}
	id, ok := p384CheckID(in)
	if ok && p384Verified.has(id) {
		return true
	}
	r := new(big.Int).SetBytes(in.sig[:p384ScalarBytes])
	s := new(big.Int).SetBytes(in.sig[p384ScalarBytes:])
	if !ecdsa.Verify(in.key, in.digest, r, s) {
		return false
	}
	if ok {
		p384Verified.add(id)
	}
	return true
}

func p384SignatureFromDER(sig []byte) ([]byte, error) {
	seq, err := derOnly(sig, derSequence)
	if err != nil {
		return nil, err
	}
	fields := derReader{data: seq.value}
	var parts [2][]byte
	for i := range parts {
		e, err := fields.expect(derInteger)
		if err != nil {
			return nil, err
		}
		if parts[i], err = derUintMagnitude(e.value); err != nil {
			return nil, err
		}
		if len(parts[i]) > p384ScalarBytes {
			return nil, errNitro
		}
	}
	if !fields.done() {
		return nil, errNitro
	}
	out := make([]byte, nitroSignatureSize)
	copy(out[p384ScalarBytes-len(parts[0]):p384ScalarBytes], parts[0])
	copy(out[nitroSignatureSize-len(parts[1]):], parts[1])
	return out, nil
}

func sha384Of(data []byte) []byte {
	h := sha512.Sum384(data)
	return h[:]
}
