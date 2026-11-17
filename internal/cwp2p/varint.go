package cwp2p

import "errors"

const (
	varintShift      = 7
	varintBytesMax   = 64/varintShift + 1
	varintU32Max     = 32/varintShift + 1
	varintMore       = 0x80
	varintLow        = 0x7f
	varintLastU64Top = 1<<(64-(varintBytesMax-1)*varintShift) - 1
	varintLastU32Top = 1<<(32-(varintU32Max-1)*varintShift) - 1
)

const _ = uint(varintBytesMax*varintShift - 64)

const _ = uint(varintU32Max*varintShift - 32)

const _ = uint(varintBytesMax - varintU32Max)

const _ = uint(varintLow - varintLastU64Top)

const _ = uint(varintLow - varintLastU32Top)

var ErrVarint = errors.New("cwp2p: invalid varint")

type Varint struct {
	Value uint64
	Len   int
}

func AppendVarint(dst []byte, v uint64) []byte {
	for range varintBytesMax {
		if v < varintMore {
			return append(dst, byte(v))
		}
		dst = append(dst, byte(v&varintLow)|varintMore)
		v >>= varintShift
	}
	return dst
}

func VarintLen(v uint64) int {
	n := 1
	for range varintBytesMax {
		if v < varintMore {
			return n
		}
		v >>= varintShift
		n++
	}
	return n
}

func ReadVarint(src []byte) (Varint, error) {
	var v uint64
	for i := range min(len(src), varintBytesMax) {
		b := src[i]
		if i == varintBytesMax-1 && b > varintLastU64Top {
			return Varint{}, ErrVarint
		}
		v |= uint64(b&varintLow) << (varintShift * i)
		if b&varintMore == 0 {
			if b == 0 && i > 0 {
				return Varint{}, ErrVarint
			}
			return Varint{Value: v, Len: i + 1}, nil
		}
	}
	return Varint{}, ErrVarint
}

type varintU32Decoder struct {
	value uint32
	count int
	done  bool
}

func (d *varintU32Decoder) feed(b byte) error {
	if d.count >= varintU32Max {
		return ErrVarint
	}
	if d.count == varintU32Max-1 && b > varintLastU32Top {
		return ErrVarint
	}
	d.value |= uint32(b&varintLow) << (varintShift * d.count)
	d.count++
	if b&varintMore != 0 {
		return nil
	}
	if b == 0 && d.count > 1 {
		return ErrVarint
	}
	d.done = true
	return nil
}
