package dkg

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
)

const (
	PublicKeyLen     = 32
	SignatureLen     = 64
	ScalarLen        = 32
	G2Len            = 96
	ParticipantsMax  = 1024
	modeVersion0     = 0
	resultAvailable  = 0
	resultTooMany    = 1
	tagAck           = 0
	tagReveal        = 1
	u32Len           = 4
	encodedMax       = 1 << 20
	setEncodedMax    = varintBytesMax + ParticipantsMax*PublicKeyLen
	commitEncodedMax = varintBytesMax + ParticipantsMax*G2Len
)

const _ = uint(encodedMax - commitEncodedMax - 4*setEncodedMax)

const _ = uint(encodedMax/G2Len - ParticipantsMax)

var (
	ErrShort     = errors.New("dkg: input is truncated")
	ErrMalformed = errors.New("dkg: input is malformed")
	ErrRange     = errors.New("dkg: count out of range")
	ErrLeftover  = errors.New("dkg: trailing bytes")
)

type PublicKey [PublicKeyLen]byte

type KeySet []PublicKey

func NewKeySet(keys []PublicKey) KeySet {
	out := slices.Clone(keys)
	slices.SortFunc(out, func(a, b PublicKey) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(out)
}

func (s KeySet) Index(k PublicKey) (int, bool) {
	return slices.BinarySearchFunc(s, k, func(a, b PublicKey) int { return bytes.Compare(a[:], b[:]) })
}

func (s KeySet) MaxFaults() int { return (len(s) - 1) / 3 }

func (s KeySet) Quorum() int { return len(s) - s.MaxFaults() }

func (s KeySet) Equal(o KeySet) bool { return slices.Equal(s, o) }

type Commitment []bls12381.G2

func (c Commitment) Constant() bls12381.G2 { return c[0] }

type PublicPoly struct {
	Total uint32
	Poly  Commitment
}

type Output struct {
	Summary  Summary
	Public   PublicPoly
	Dealers  KeySet
	Players  KeySet
	Revealed KeySet
}

type Outcome struct {
	Epoch       uint64
	Output      Output
	NextPlayers KeySet
	NextFullDKG bool
}

type AckOrReveal struct {
	Reveal bool
	Ack    [SignatureLen]byte
	Share  bls12381.Scalar
}

type DealerResults struct {
	Players KeySet
	Values  []AckOrReveal
}

type DealerLog struct {
	Commitment Commitment
	Results    *DealerResults
}

type SignedDealerLog struct {
	Dealer    PublicKey
	Log       DealerLog
	Signature [SignatureLen]byte
}

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
		value |= uint64(b&varintLow) << (varintShift * uint(i))
		if b&varintMore != 0 {
			continue
		}
		if b == 0 && i > 0 {
			return 0, ErrMalformed
		}
		return value, nil
	}
	return 0, ErrMalformed
}

func (r *reader) count(least int) (int, error) {
	v, err := r.varint()
	if err != nil {
		return 0, err
	}
	if v < uint64(least) || v > ParticipantsMax {
		return 0, ErrRange
	}
	return int(v), nil
}

func (r *reader) done() error {
	if len(r.data) != 0 {
		return ErrLeftover
	}
	return nil
}

func (r *reader) keySet(least int) (KeySet, error) {
	n, err := r.count(least)
	if err != nil {
		return nil, err
	}
	out := make(KeySet, n)
	for i := range out {
		b, err := r.fixed(PublicKeyLen)
		if err != nil {
			return nil, err
		}
		out[i] = PublicKey(b)
		if i > 0 && bytes.Compare(out[i-1][:], out[i][:]) >= 0 {
			return nil, ErrMalformed
		}
	}
	return out, nil
}

func (r *reader) commitment() (Commitment, error) {
	n, err := r.count(1)
	if err != nil {
		return nil, err
	}
	out := make(Commitment, n)
	for i := range out {
		b, err := r.fixed(G2Len)
		if err != nil {
			return nil, err
		}
		if out[i], err = bls12381.DecodeG2(b); err != nil {
			return nil, errors.Join(ErrMalformed, err)
		}
	}
	return out, nil
}

func (r *reader) scalar() (bls12381.Scalar, error) {
	b, err := r.fixed(ScalarLen)
	if err != nil {
		return bls12381.Scalar{}, err
	}
	s, err := bls12381.ScalarFromBytes(b)
	if err != nil || s.IsZero() {
		return bls12381.Scalar{}, ErrMalformed
	}
	return s, nil
}

func (r *reader) output() (Output, error) {
	var o Output
	s, err := r.fixed(SummaryLen)
	if err != nil {
		return Output{}, err
	}
	o.Summary = Summary(s)
	mode, err := r.byte1()
	if err != nil {
		return Output{}, err
	}
	if mode != modeVersion0 {
		return Output{}, ErrMalformed
	}
	total, err := r.fixed(u32Len)
	if err != nil {
		return Output{}, err
	}
	o.Public.Total = binary.BigEndian.Uint32(total)
	if o.Public.Poly, err = r.commitment(); err != nil {
		return Output{}, err
	}
	if o.Dealers, err = r.keySet(1); err != nil {
		return Output{}, err
	}
	if o.Players, err = r.keySet(1); err != nil {
		return Output{}, err
	}
	if o.Revealed, err = r.keySet(0); err != nil {
		return Output{}, err
	}
	return o, nil
}

func DecodeOutcome(data []byte) (Outcome, error) {
	r := &reader{data: data}
	var o Outcome
	var err error
	if o.Epoch, err = r.varint(); err != nil {
		return Outcome{}, err
	}
	if o.Output, err = r.output(); err != nil {
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
	o.NextFullDKG = full == 1
	return o, r.done()
}

func (r *reader) results() (*DealerResults, error) {
	tag, err := r.byte1()
	if err != nil {
		return nil, err
	}
	if tag == resultTooMany {
		return nil, nil
	}
	if tag != resultAvailable {
		return nil, ErrMalformed
	}
	players, err := r.keySet(0)
	if err != nil {
		return nil, err
	}
	n, err := r.count(0)
	if err != nil {
		return nil, err
	}
	if n != len(players) {
		return nil, ErrMalformed
	}
	res := &DealerResults{Players: players, Values: make([]AckOrReveal, n)}
	for i := range res.Values {
		if res.Values[i], err = r.ackOrReveal(); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func (r *reader) ackOrReveal() (AckOrReveal, error) {
	tag, err := r.byte1()
	if err != nil {
		return AckOrReveal{}, err
	}
	switch tag {
	case tagAck:
		b, err := r.fixed(SignatureLen)
		if err != nil {
			return AckOrReveal{}, err
		}
		return AckOrReveal{Ack: [SignatureLen]byte(b)}, nil
	case tagReveal:
		s, err := r.scalar()
		return AckOrReveal{Reveal: true, Share: s}, err
	}
	return AckOrReveal{}, ErrMalformed
}

func DecodeSignedDealerLog(data []byte) (SignedDealerLog, error) {
	r := &reader{data: data}
	var s SignedDealerLog
	d, err := r.fixed(PublicKeyLen)
	if err != nil {
		return SignedDealerLog{}, err
	}
	s.Dealer = PublicKey(d)
	if s.Log.Commitment, err = r.commitment(); err != nil {
		return SignedDealerLog{}, err
	}
	if s.Log.Results, err = r.results(); err != nil {
		return SignedDealerLog{}, err
	}
	sig, err := r.fixed(SignatureLen)
	if err != nil {
		return SignedDealerLog{}, err
	}
	s.Signature = [SignatureLen]byte(sig)
	return s, r.done()
}

func appendKeySet(out []byte, s KeySet) []byte {
	out = appendVarint(out, uint64(len(s)))
	for _, k := range s {
		out = append(out, k[:]...)
	}
	return out
}

func appendCommitment(out []byte, c Commitment) []byte {
	out = appendVarint(out, uint64(len(c)))
	for _, p := range c {
		b := p.Bytes()
		out = append(out, b[:]...)
	}
	return out
}

func (o Output) Encode() []byte {
	out := make([]byte, 0, SummaryLen+1+u32Len+commitEncodedMax/ParticipantsMax*len(o.Public.Poly)+3*varintBytesMax)
	out = append(out, o.Summary[:]...)
	out = append(out, modeVersion0)
	out = binary.BigEndian.AppendUint32(out, o.Public.Total)
	out = appendCommitment(out, o.Public.Poly)
	out = appendKeySet(out, o.Dealers)
	out = appendKeySet(out, o.Players)
	return appendKeySet(out, o.Revealed)
}

func (o Outcome) Encode() []byte {
	out := appendVarint(nil, o.Epoch)
	out = append(out, o.Output.Encode()...)
	out = appendKeySet(out, o.NextPlayers)
	if o.NextFullDKG {
		return append(out, 1)
	}
	return append(out, 0)
}

func (l DealerLog) Encode() []byte {
	out := appendCommitment(nil, l.Commitment)
	if l.Results == nil {
		return append(out, resultTooMany)
	}
	out = append(out, resultAvailable)
	out = appendKeySet(out, l.Results.Players)
	out = appendVarint(out, uint64(len(l.Results.Values)))
	for _, v := range l.Results.Values {
		if v.Reveal {
			out = append(out, tagReveal)
			out = append(out, v.Share[:]...)
			continue
		}
		out = append(out, tagAck)
		out = append(out, v.Ack[:]...)
	}
	return out
}

func (s SignedDealerLog) Encode() []byte {
	out := append([]byte(nil), s.Dealer[:]...)
	out = append(out, s.Log.Encode()...)
	return append(out, s.Signature[:]...)
}

func (k PublicKey) String() string { return hex.EncodeToString(k[:]) }
