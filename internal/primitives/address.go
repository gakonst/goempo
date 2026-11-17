package primitives

import (
	"bytes"
)

const AddressLength = 20

type Address [AddressLength]byte

func BytesToAddress(b []byte) Address {
	var a Address
	if len(b) > AddressLength {
		b = b[len(b)-AddressLength:]
	}
	copy(a[AddressLength-len(b):], b)
	return a
}

func HexToAddress(s string) (Address, error) {
	var a Address
	err := DecodeHexFixed(a[:], s)
	return a, err
}

func (a Address) Bytes() []byte { return a[:] }

func (a Address) Hex() string { return EncodeHex(a[:]) }

func (a Address) String() string { return a.Hex() }

func (a Address) IsZero() bool { return a == Address{} }

func (a Address) Compare(b Address) int { return bytes.Compare(a[:], b[:]) }

func (a Address) MarshalText() ([]byte, error) {
	return EncodeHexBytes(a[:]), nil
}

func (a *Address) UnmarshalText(text []byte) error {
	return DecodeHexFixed(a[:], string(text))
}
