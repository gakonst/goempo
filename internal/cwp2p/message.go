package cwp2p

import "errors"

const (
	prefixData         = 0
	prefixPing         = 1
	PayloadOverheadMax = 1 + varintBytesMax + varintU32Max
)

const commonwarePayloadOverhead = 16

const _ = uint(commonwarePayloadOverhead - PayloadOverheadMax)

const _ = uint(PayloadOverheadMax - commonwarePayloadOverhead)

var (
	ErrMessage         = errors.New("cwp2p: malformed p2p message")
	ErrMessageTooLarge = errors.New("cwp2p: p2p message exceeds the configured size")
)

type Kind uint8

const (
	KindData Kind = prefixData
	KindPing Kind = prefixPing
)

type Message struct {
	Kind    Kind
	Channel uint64
	Data    []byte
}

func EncodeData(channel uint64, data []byte) []byte {
	out := make([]byte, 0, PayloadOverheadMax+len(data))
	out = append(out, prefixData)
	out = AppendVarint(out, channel)
	out = AppendVarint(out, uint64(len(data)))
	return append(out, data...)
}

func EncodePing() []byte {
	return []byte{prefixPing}
}

func DecodeMessage(b []byte, maxData uint32) (Message, error) {
	if len(b) == 0 {
		return Message{}, ErrMessage
	}
	switch b[0] {
	case prefixPing:
		if len(b) != 1 {
			return Message{}, ErrMessage
		}
		return Message{Kind: KindPing}, nil
	case prefixData:
		return decodeData(b[1:], maxData)
	default:
		return Message{}, ErrMessage
	}
}

func decodeData(b []byte, maxData uint32) (Message, error) {
	ch, err := ReadVarint(b)
	if err != nil {
		return Message{}, err
	}
	b = b[ch.Len:]
	n, err := ReadVarint(b)
	if err != nil {
		return Message{}, err
	}
	if n.Len > varintU32Max {
		return Message{}, ErrVarint
	}
	if n.Value > uint64(maxData) {
		return Message{}, ErrMessageTooLarge
	}
	b = b[n.Len:]
	if uint64(len(b)) != n.Value {
		return Message{}, ErrMessage
	}
	return Message{Kind: KindData, Channel: ch.Value, Data: b}, nil
}
