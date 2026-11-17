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

type WireMessage struct {
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

func DecodeMessage(b []byte, maxData uint32) (WireMessage, error) {
	if len(b) == 0 {
		return WireMessage{}, ErrMessage
	}
	switch b[0] {
	case prefixPing:
		if len(b) != 1 {
			return WireMessage{}, ErrMessage
		}
		return WireMessage{Kind: KindPing}, nil
	case prefixData:
		return decodeData(b[1:], maxData)
	default:
		return WireMessage{}, ErrMessage
	}
}

func decodeData(b []byte, maxData uint32) (WireMessage, error) {
	ch, err := ReadVarint(b)
	if err != nil {
		return WireMessage{}, err
	}
	b = b[ch.Len:]
	n, err := ReadVarint(b)
	if err != nil {
		return WireMessage{}, err
	}
	if n.Len > varintU32Max {
		return WireMessage{}, ErrVarint
	}
	if n.Value > uint64(maxData) {
		return WireMessage{}, ErrMessageTooLarge
	}
	b = b[n.Len:]
	if uint64(len(b)) != n.Value {
		return WireMessage{}, ErrMessage
	}
	return WireMessage{Kind: KindData, Channel: ch.Value, Data: b}, nil
}
