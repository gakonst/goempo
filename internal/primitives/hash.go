package primitives

import (
	"bytes"
)

const HashLength = 32

type Hash [HashLength]byte

func BytesToHash(b []byte) Hash {
	var h Hash
	if len(b) > HashLength {
		b = b[len(b)-HashLength:]
	}
	copy(h[HashLength-len(b):], b)
	return h
}

func HexToHash(s string) (Hash, error) {
	var h Hash
	err := DecodeHexFixed(h[:], s)
	return h, err
}

func (h Hash) Bytes() []byte { return h[:] }

func (h Hash) Hex() string { return EncodeHex(h[:]) }

func (h Hash) String() string { return h.Hex() }

func (h Hash) IsZero() bool { return h == Hash{} }

func (h Hash) Compare(o Hash) int { return bytes.Compare(h[:], o[:]) }

func (h Hash) MarshalText() ([]byte, error) {
	return EncodeHexBytes(h[:]), nil
}

func (h *Hash) UnmarshalText(text []byte) error {
	return DecodeHexFixed(h[:], string(text))
}
