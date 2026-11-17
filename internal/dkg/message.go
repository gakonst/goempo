package dkg

import (
	"math"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
)

type MessageKind uint8

const (
	MessageDealer MessageKind = iota
	MessageAck
)

type Message struct {
	Kind       MessageKind
	Commitment Commitment
	Share      bls12381.Scalar
	Ack        [SignatureLen]byte
}

type EpochMessage struct {
	Epoch   uint64
	Message Message
}

func (m Message) Encode() []byte {
	out := []byte{byte(m.Kind)}
	if m.Kind == MessageAck {
		return append(out, m.Ack[:]...)
	}
	out = appendCommitment(out, m.Commitment)
	return append(out, m.Share[:]...)
}

func (m Message) EncodeEpoch(epoch uint64) []byte {
	return append(appendVarint(nil, epoch), m.Encode()...)
}

func DecodeMessage(payload []byte, playersMax int) (Message, error) {
	r := &reader{data: payload}
	tag, err := r.byte1()
	if err != nil {
		return Message{}, err
	}
	m := Message{Kind: MessageKind(tag)}
	if m.Kind == MessageAck {
		sig, err := r.fixed(SignatureLen)
		if err != nil {
			return Message{}, err
		}
		m.Ack = [SignatureLen]byte(sig)
		return m, r.done()
	}
	if m.Kind != MessageDealer {
		return Message{}, ErrMalformed
	}
	if m.Commitment, err = r.commitment(); err != nil {
		return Message{}, err
	}
	if len(m.Commitment) > playersMax {
		return Message{}, ErrRange
	}
	if m.Share, err = r.scalar(); err != nil {
		return Message{}, err
	}
	return m, r.done()
}

func DecodeEpochMessage(frame []byte, playersMax int) (EpochMessage, error) {
	r := &reader{data: frame}
	epoch, err := r.varint()
	if err != nil {
		return EpochMessage{}, err
	}
	m, err := DecodeMessage(r.data, playersMax)
	if err != nil {
		return EpochMessage{}, err
	}
	return EpochMessage{Epoch: epoch, Message: m}, nil
}

func DecodeOutput(data []byte) (Output, error) {
	r := &reader{data: data}
	o, err := r.output()
	if err != nil {
		return Output{}, err
	}
	return o, r.done()
}

func (s Share) Encode() []byte {
	out := appendVarint(nil, uint64(s.Index))
	return append(out, s.Private[:]...)
}

func DecodeShare(data []byte) (Share, error) {
	r := &reader{data: data}
	index, err := r.varint()
	if err != nil {
		return Share{}, err
	}
	if index > math.MaxUint32 {
		return Share{}, ErrRange
	}
	private, err := r.scalar()
	if err != nil {
		return Share{}, err
	}
	return Share{Index: int(index), Private: private}, r.done()
}
