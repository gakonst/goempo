package primitives

import (
	"errors"
	"math/bits"
)

const hexDigits = "0123456789abcdef"

var (
	ErrHexOddLength = errors.New("primitives: hex string has an odd number of digits")
	ErrHexDigit     = errors.New("primitives: invalid hex digit")
	ErrHexLength    = errors.New("primitives: hex string has the wrong length")
)

func EncodeHex(b []byte) string {
	return string(EncodeHexBytes(b))
}

func EncodeHexBytes(b []byte) []byte {
	out := make([]byte, 2+2*len(b))
	out[0] = '0'
	out[1] = 'x'
	for i := 0; i < len(b); i++ {
		out[2+2*i] = hexDigits[b[i]>>4]
		out[3+2*i] = hexDigits[b[i]&0x0f]
	}
	return out
}

func TrimHexPrefix(s string) string {
	if len(s) < 2 {
		return s
	}
	if s[0] != '0' {
		return s
	}
	if s[1] == 'x' {
		return s[2:]
	}
	if s[1] == 'X' {
		return s[2:]
	}
	return s
}

func DecodeHex(s string) ([]byte, error) {
	digits := TrimHexPrefix(s)
	if len(digits)%2 != 0 {
		return nil, ErrHexOddLength
	}
	out := make([]byte, len(digits)/2)
	if err := decodeHexInto(out, digits); err != nil {
		return nil, err
	}
	return out, nil
}

func DecodeHexFixed(dst []byte, s string) error {
	digits := TrimHexPrefix(s)
	if len(digits) != 2*len(dst) {
		return ErrHexLength
	}
	return decodeHexInto(dst, digits)
}

func decodeHexInto(dst []byte, digits string) error {
	for i := 0; i < len(dst); i++ {
		hi, okHi := hexValue(digits[2*i])
		lo, okLo := hexValue(digits[2*i+1])
		if !okHi {
			return ErrHexDigit
		}
		if !okLo {
			return ErrHexDigit
		}
		dst[i] = hi<<4 | lo
	}
	return nil
}

func hexValue(c byte) (byte, bool) {
	if c >= '0' && c <= '9' {
		return c - '0', true
	}
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 10, true
	}
	if c >= 'A' && c <= 'F' {
		return c - 'A' + 10, true
	}
	return 0, false
}

func DecodeHexUint64(s string) (uint64, error) {
	digits := TrimHexPrefix(s)
	if len(digits) == 0 {
		return 0, ErrHexLength
	}
	if len(digits) > 16 {
		return 0, ErrHexLength
	}
	var v uint64
	for i := 0; i < len(digits); i++ {
		d, ok := hexValue(digits[i])
		if !ok {
			return 0, ErrHexDigit
		}
		v = v<<4 | uint64(d)
	}
	return v, nil
}

func EncodeHexUint64(v uint64) string {
	if v == 0 {
		return "0x0"
	}
	nibbles := min((bits.Len64(v)+3)/4, 16)
	buf := make([]byte, 2+nibbles)
	buf[0] = '0'
	buf[1] = 'x'
	for k := 0; k < nibbles; k++ {
		buf[len(buf)-1-k] = hexDigits[(v>>(4*k))&0x0f]
	}
	return string(buf)
}
