package precompiles

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha512"
	"math/big"
)

const (
	p384ScalarBytes = 48
	p384KeyBytes    = 1 + 2*p384ScalarBytes
)

const _ = uint(nitroSignatureSize - 2*p384ScalarBytes)

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
	r := new(big.Int).SetBytes(in.sig[:p384ScalarBytes])
	s := new(big.Int).SetBytes(in.sig[p384ScalarBytes:])
	return ecdsa.Verify(in.key, in.digest, r, s)
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
