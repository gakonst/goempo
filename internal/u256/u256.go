package u256

import (
	"encoding/hex"
	"errors"
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

const (
	Limbs     = 4
	Bytes     = 32
	Bits      = 256
	limbBits  = 64
	limbBytes = 8
	hexMax    = 2 + 2*Bytes
)

const _ = uint(Limbs*limbBytes - Bytes)

const _ = uint(Bytes*limbBytes - Bits)

const _ = uint(hexMax - 2*Bytes)

var (
	ErrHexSyntax = errors.New("u256: invalid hex")
	ErrHexRange  = errors.New("u256: hex value exceeds 256 bits")
)

type Int [Limbs]uint64

func Zero() Int { return Int{} }

func One() Int { return Int{1, 0, 0, 0} }

func Max() Int {
	return Int{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)}
}

func MinSigned() Int { return Int{0, 0, 0, 1 << (limbBits - 1)} }

func FromUint64(v uint64) Int { return Int{v, 0, 0, 0} }

func FromBytes32(b [Bytes]byte) Int {
	var z Int
	for i := 0; i < Limbs; i++ {
		off := Bytes - (i+1)*limbBytes
		z[i] = beUint64(b[off : off+limbBytes])
	}
	return z
}

func FromBytes(b []byte) Int {
	assert.Ok(len(b) <= Bytes, "u256: FromBytes takes at most 32 bytes")
	var buf [Bytes]byte
	copy(buf[Bytes-len(b):], b)
	return FromBytes32(buf)
}

func FromHex(s string) (Int, error) {
	if len(s) > hexMax {
		return Int{}, ErrHexRange
	}
	if len(s) >= 2 {
		if s[0] == '0' {
			if s[1] == 'x' {
				s = s[2:]
			}
		}
	}
	if len(s) == 0 {
		return Int{}, ErrHexSyntax
	}
	if len(s) > 2*Bytes {
		return Int{}, ErrHexRange
	}
	var buf [2 * Bytes]byte
	for i := 0; i < len(buf); i++ {
		buf[i] = '0'
	}
	copy(buf[2*Bytes-len(s):], s)
	var raw [Bytes]byte
	_, err := hex.Decode(raw[:], buf[:])
	if err != nil {
		return Int{}, ErrHexSyntax
	}
	return FromBytes32(raw), nil
}

func (x Int) Bytes32() [Bytes]byte {
	var b [Bytes]byte
	x.PutBytes32(b[:])
	return b
}

func (x Int) PutBytes32(dst []byte) {
	assert.Ok(len(dst) >= Bytes, "u256: PutBytes32 needs 32 bytes")
	for i := 0; i < Limbs; i++ {
		off := Bytes - (i+1)*limbBytes
		putBeUint64(dst[off:off+limbBytes], x[i])
	}
}

func (x Int) Hex() string {
	b := x.Bytes32()
	n := x.ByteLen()
	if n == 0 {
		return "0x0"
	}
	s := hex.EncodeToString(b[Bytes-n:])
	if s[0] == '0' {
		s = s[1:]
	}
	return "0x" + s
}

func (x Int) Uint64() uint64 { return x[0] }

func (x Int) IsUint64() bool { return x[1]|x[2]|x[3] == 0 }

func (x Int) BitLen() int {
	for i := Limbs - 1; i >= 0; i-- {
		if x[i] != 0 {
			return i*limbBits + bits.Len64(x[i])
		}
	}
	return 0
}

func (x Int) Clz() Int { return FromUint64(uint64(Bits - x.BitLen())) }

func (x Int) ByteLen() int { return (x.BitLen() + limbBytes - 1) / limbBytes }

func (x Int) IsZero() bool { return x[0]|x[1]|x[2]|x[3] == 0 }

func (x Int) Eq(y Int) bool { return x == y }

func (x Int) Cmp(y Int) int {
	for i := Limbs - 1; i >= 0; i-- {
		if x[i] < y[i] {
			return -1
		}
		if x[i] > y[i] {
			return 1
		}
	}
	return 0
}

func (x Int) Lt(y Int) bool {
	_, borrow := x.SubOverflow(y)
	return borrow
}

func (x Int) Gt(y Int) bool { return y.Lt(x) }

func (x Int) IsNegative() bool { return x[Limbs-1]>>(limbBits-1) == 1 }

func (x Int) Slt(y Int) bool {
	xn := x.IsNegative()
	yn := y.IsNegative()
	if xn != yn {
		return xn
	}
	return x.Lt(y)
}

func (x Int) Sgt(y Int) bool { return y.Slt(x) }

func beUint64(b []byte) uint64 {
	assert.Ok(len(b) >= limbBytes, "u256: short limb")
	return uint64(b[7]) | uint64(b[6])<<8 | uint64(b[5])<<16 | uint64(b[4])<<24 |
		uint64(b[3])<<32 | uint64(b[2])<<40 | uint64(b[1])<<48 | uint64(b[0])<<56
}

func putBeUint64(b []byte, v uint64) {
	assert.Ok(len(b) >= limbBytes, "u256: short limb")
	for i := 0; i < limbBytes; i++ {
		b[limbBytes-1-i] = byte(v >> (8 * i))
	}
}
