package wire

import (
	"encoding/binary"
	"errors"
	"math"
)

const (
	ChannelVotes        uint64 = 0
	ChannelCertificates uint64 = 1
	ChannelResolver     uint64 = 2
	ChannelBroadcast    uint64 = 3
	ChannelMarshal      uint64 = 4
	ChannelDKG          uint64 = 5
)

const (
	varintBytesMax   = 10
	varintU32Max     = 5
	varintShift      = 7
	varintMore       = 0x80
	varintLow        = 0x7f
	varintLastU64Max = 1
	uint64Bytes      = 8
	uint64Bits       = 64
)

const _ = uint(varintBytesMax*varintShift - uint64Bits)

const _ = uint(varintBytesMax - varintU32Max)

const _ = uint(1<<(uint64Bits-(varintBytesMax-1)*varintShift) - 1 - varintLastU64Max)

var (
	ErrShort    = errors.New("wire: message is truncated")
	ErrVarint   = errors.New("wire: varint is malformed")
	ErrLeftover = errors.New("wire: message has bytes left over")
	ErrTag      = errors.New("wire: unknown enum tag")
	ErrRange    = errors.New("wire: value out of range")
)

type reader struct {
	data []byte
}

func (r *reader) fixed(n int) ([]byte, error) {
	if n < 0 || len(r.data) < n {
		return nil, ErrShort
	}
	out := r.data[:n]
	r.data = r.data[n:]
	return out, nil
}

func (r *reader) byte1() (byte, error) {
	b, err := r.fixed(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *reader) varint() (uint64, error) {
	var value uint64
	for i := range varintBytesMax {
		b, err := r.byte1()
		if err != nil {
			return 0, err
		}
		if i == varintBytesMax-1 && b > varintLastU64Max {
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

func (r *reader) varint32() (uint32, error) {
	before := len(r.data)
	v, err := r.varint()
	if err != nil {
		return 0, err
	}
	if v > math.MaxUint32 || before-len(r.data) > varintU32Max {
		return 0, ErrVarint
	}
	return uint32(v), nil
}

func (r *reader) uint64BE() (uint64, error) {
	b, err := r.fixed(uint64Bytes)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

func (r *reader) rest() []byte {
	out := r.data
	r.data = nil
	return out
}

func (r *reader) done() error {
	if len(r.data) != 0 {
		return ErrLeftover
	}
	return nil
}

func appendVarint(out []byte, v uint64) []byte {
	for range varintBytesMax {
		if v < varintMore {
			return append(out, byte(v))
		}
		out = append(out, byte(v&varintLow)|varintMore)
		v >>= varintShift
	}
	return out
}

func AppendMux(out []byte, subchannel uint64, payload []byte) []byte {
	out = appendVarint(out, subchannel)
	return append(out, payload...)
}

type Muxed struct {
	Subchannel uint64
	Payload    []byte
}

func SplitMux(frame []byte) (Muxed, error) {
	r := reader{data: frame}
	sub, err := r.varint()
	if err != nil {
		return Muxed{}, err
	}
	return Muxed{Subchannel: sub, Payload: r.rest()}, nil
}
