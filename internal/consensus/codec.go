package consensus

import "errors"

const (
	varintBytesMax = 10
	varintShift    = 7
	varintMore     = 0x80
	varintLow      = 0x7f
	DigestBytes    = 32
	SignatureBytes = 48
	IdentityBytes  = 96
	uint64Bits     = 64
)

const _ = uint(varintBytesMax*varintShift - uint64Bits)

var (
	ErrShort    = errors.New("consensus: message is truncated")
	ErrVarint   = errors.New("consensus: varint is malformed")
	ErrLeftover = errors.New("consensus: message has bytes left over")
)

type Digest [DigestBytes]byte

type reader struct {
	data []byte
}

func (r *reader) fixed(n int) ([]byte, error) {
	if len(r.data) < n {
		return nil, ErrShort
	}
	out := r.data[:n]
	r.data = r.data[n:]
	return out, nil
}

func (r *reader) varint() (uint64, error) {
	var value uint64
	for i := 0; i < varintBytesMax; i++ {
		if len(r.data) == 0 {
			return 0, ErrShort
		}
		b := r.data[0]
		r.data = r.data[1:]
		if i == varintBytesMax-1 && b > 1 {
			return 0, ErrVarint
		}
		value |= uint64(b&varintLow) << (varintShift * uint(i))
		if b&varintMore != 0 {
			continue
		}
		if b == 0 && i > 0 {
			return 0, ErrVarint
		}
		return value, nil
	}
	return 0, ErrVarint
}

func (r *reader) done() error {
	if len(r.data) != 0 {
		return ErrLeftover
	}
	return nil
}

func appendVarint(out []byte, v uint64) []byte {
	for i := 0; i < varintBytesMax; i++ {
		if v < varintMore {
			return append(out, byte(v))
		}
		out = append(out, byte(v&varintLow)|varintMore)
		v >>= varintShift
	}
	return out
}

type Domain []byte

func unionUnique(namespace Domain, msg []byte) []byte {
	out := make([]byte, 0, varintBytesMax+len(namespace)+len(msg))
	out = appendVarint(out, uint64(len(namespace)))
	out = append(out, namespace...)
	return append(out, msg...)
}
