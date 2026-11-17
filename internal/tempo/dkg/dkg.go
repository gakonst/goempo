package dkg

import (
	"bytes"
	"errors"
)

const (
	ValidatorsMax   = 1<<16 - 1
	KeyBytes        = 32
	TranscriptBytes = 32
	G2Bytes         = 96
	varintBytesMax  = 10
	varintShift     = 7
	varintMore      = 0x80
	varintLow       = 0x7f
	u32Bytes        = 4
	modeVersion0    = 0
	extraDataMax    = 10 * 1024
)

const _ = uint(extraDataMax/KeyBytes - 4*varintBytesMax)

const _ = uint(ValidatorsMax - extraDataMax/KeyBytes)

var (
	ErrShort     = errors.New("dkg: outcome is truncated")
	ErrMalformed = errors.New("dkg: outcome is malformed")
	ErrRange     = errors.New("dkg: count out of range")
)

type Outcome struct {
	Epoch          uint64
	Total          uint32
	Identity       [G2Bytes]byte
	Coefficients   uint32
	Dealers        [][KeyBytes]byte
	Players        [][KeyBytes]byte
	Revealed       [][KeyBytes]byte
	NextPlayers    [][KeyBytes]byte
	IsNextFullDKG  bool
	TrailingBytes  int
	TranscriptHash [TranscriptBytes]byte
}

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

func (r *reader) byte1() (byte, error) {
	b, err := r.fixed(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *reader) varint() (uint64, error) {
	var value uint64
	for i := 0; i < varintBytesMax; i++ {
		b, err := r.byte1()
		if err != nil {
			return 0, err
		}
		value |= uint64(b&varintLow) << (varintShift * uint(i))
		if b&varintMore == 0 {
			return value, nil
		}
	}
	return 0, ErrMalformed
}

type bounds struct {
	min uint64
	max uint64
}

func (r *reader) length(b bounds) (uint32, error) {
	v, err := r.varint()
	if err != nil {
		return 0, err
	}
	if v < b.min || v > b.max {
		return 0, ErrRange
	}
	return uint32(v), nil
}

func (r *reader) keySet(minCount uint64) ([][KeyBytes]byte, error) {
	count, err := r.length(bounds{min: minCount, max: ValidatorsMax})
	if err != nil {
		return nil, err
	}
	keys := make([][KeyBytes]byte, count)
	for i := range keys {
		k, err := r.fixed(KeyBytes)
		if err != nil {
			return nil, err
		}
		keys[i] = [KeyBytes]byte(k)
		if i > 0 && bytes.Compare(keys[i-1][:], keys[i][:]) >= 0 {
			return nil, ErrMalformed
		}
	}
	return keys, nil
}

func Decode(data []byte) (Outcome, error) {
	r := &reader{data: data}
	var o Outcome
	var err error
	if o.Epoch, err = r.varint(); err != nil {
		return Outcome{}, err
	}
	t, err := r.fixed(TranscriptBytes)
	if err != nil {
		return Outcome{}, err
	}
	o.TranscriptHash = [TranscriptBytes]byte(t)
	mode, err := r.byte1()
	if err != nil {
		return Outcome{}, err
	}
	if mode != modeVersion0 {
		return Outcome{}, ErrMalformed
	}
	total, err := r.fixed(u32Bytes)
	if err != nil {
		return Outcome{}, err
	}
	o.Total = uint32(total[0])<<24 | uint32(total[1])<<16 | uint32(total[2])<<8 | uint32(total[3])
	if o.Total == 0 || o.Total > ValidatorsMax {
		return Outcome{}, ErrRange
	}
	if err := decodeCommitment(r, &o); err != nil {
		return Outcome{}, err
	}
	return decodeSets(r, o)
}

func decodeCommitment(r *reader, o *Outcome) error {
	n, err := r.length(bounds{min: 1, max: ValidatorsMax})
	if err != nil {
		return err
	}
	o.Coefficients = n
	id, err := r.fixed(G2Bytes)
	if err != nil {
		return err
	}
	o.Identity = [G2Bytes]byte(id)
	_, err = r.fixed(int(n-1) * G2Bytes)
	return err
}

func decodeSets(r *reader, o Outcome) (Outcome, error) {
	var err error
	if o.Dealers, err = r.keySet(1); err != nil {
		return Outcome{}, err
	}
	if o.Players, err = r.keySet(1); err != nil {
		return Outcome{}, err
	}
	if o.Revealed, err = r.keySet(0); err != nil {
		return Outcome{}, err
	}
	if o.NextPlayers, err = r.keySet(1); err != nil {
		return Outcome{}, err
	}
	full, err := r.byte1()
	if err != nil {
		return Outcome{}, err
	}
	if full > 1 {
		return Outcome{}, ErrMalformed
	}
	o.IsNextFullDKG = full == 1
	o.TrailingBytes = len(r.data)
	return o, nil
}
