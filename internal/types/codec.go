package types

import (
	"errors"

	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	encodedBytesMax = 1 << 26
	listItemsMax    = 1 << 22
	u128Bytes       = 16
	scalarBytes     = 32
)

const _ = uint(encodedBytesMax - listItemsMax)

const _ = uint(u256.Bytes - scalarBytes)

const _ = uint(scalarBytes - u128Bytes)

var (
	ErrTooLarge         = errors.New("types: encoding exceeds the size limit")
	ErrTooManyItems     = errors.New("types: list has more items than the limit")
	ErrBool             = errors.New("types: boolean is not 0 or 1")
	ErrTxType           = errors.New("types: unknown transaction type")
	ErrEmptyTx          = errors.New("types: empty transaction encoding")
	ErrSignatureV       = errors.New("types: signature v is not a valid value")
	ErrSignature        = errors.New("types: signature encoding is invalid")
	ErrHighS            = errors.New("types: signature s is above n/2")
	ErrUnsupportedSig   = errors.New("types: signature type is not supported")
	ErrCalls            = errors.New("types: tempo transaction calls are invalid")
	ErrValidity         = errors.New("types: valid_before is not after valid_after")
	ErrKeyAuthorization = errors.New("types: key authorization encoding is invalid")
	ErrHeaderFields     = errors.New("types: header has an unexpected number of fields")
	ErrBlockFields      = errors.New("types: block has an unexpected number of fields")
	ErrReceipt          = errors.New("types: receipt encoding is invalid")
	ErrNonCanonical     = errors.New("types: optional field is not in canonical form")
	ErrP256             = errors.New("types: p256 signature does not verify")
	ErrWebAuthn         = errors.New("types: webauthn data is invalid")
)

type decodeFunc[T any] func(s *rlp.Stream) (T, error)

func decodeList[T any](s *rlp.Stream, decode decodeFunc[T]) ([]T, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	n, err := list.Count()
	if err != nil {
		return nil, err
	}
	if n > listItemsMax {
		return nil, ErrTooManyItems
	}
	out := make([]T, 0, n)
	for i := 0; i < n; i++ {
		v, err := decode(&list)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, list.Finish()
}

func readU256(s *rlp.Stream) (u256.Int, error) {
	b, err := s.UintBytes(u256.Bytes)
	if err != nil {
		return u256.Int{}, err
	}
	return u256.FromBytes(b), nil
}

func readU128(s *rlp.Stream) (u256.Int, error) {
	b, err := s.UintBytes(u128Bytes)
	if err != nil {
		return u256.Int{}, err
	}
	return u256.FromBytes(b), nil
}

func readScalar(s *rlp.Stream) ([scalarBytes]byte, error) {
	var out [scalarBytes]byte
	b, err := s.UintBytes(scalarBytes)
	if err != nil {
		return out, err
	}
	copy(out[scalarBytes-len(b):], b)
	return out, nil
}

func readBool(s *rlp.Stream) (bool, error) {
	v, err := s.Uint64()
	if err != nil {
		return false, err
	}
	if v > 1 {
		return false, ErrBool
	}
	return v == 1, nil
}

func readByte(s *rlp.Stream) (uint8, error) {
	v, err := s.Uint64()
	if err != nil {
		return 0, err
	}
	if v > 0xff {
		return 0, ErrSignatureV
	}
	return uint8(v), nil
}

func writeU256(e *rlp.Encoder, v u256.Int) {
	var b [u256.Bytes]byte
	v.PutBytes32(b[:])
	e.WriteUintBytes(b[:])
}

type rlpBool bool

func (b rlpBool) uint64() uint64 {
	if b {
		return 1
	}
	return 0
}

func readBytesCopy(s *rlp.Stream) ([]byte, error) {
	b, err := s.Bytes()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), b...), nil
}
