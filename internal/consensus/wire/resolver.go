package wire

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/kapetan-io/tiger/assert"
)

type FrameKind uint8

const (
	FrameRequest FrameKind = iota
	FrameResponse
	FrameError
)

type Frame struct {
	ID   uint64
	Kind FrameKind
	Body []byte
}

type MarshalKeyKind uint8

const (
	KeyBlock MarshalKeyKind = iota
	KeyFinalized
	KeyNotarized
)

type MarshalKey struct {
	Kind   MarshalKeyKind
	Digest consensus.Digest
	Height uint64
	Round  consensus.Round
}

type ResolverRequest struct {
	Epoch uint64
	ID    uint64
	View  uint64
}

type ResolverResponse struct {
	Epoch     uint64
	ID        uint64
	Certified Certified
}

type ResolverError struct {
	Epoch uint64
	ID    uint64
}

type ResolverMessage struct {
	Epoch uint64
	Frame Frame
}

type MarshalRequest struct {
	ID  uint64
	Key MarshalKey
}

type MarshalResponse struct {
	ID   uint64
	Data []byte
}

type MarshalError struct {
	ID uint64
}

type CertifiedBlock struct {
	Certified Certified
	Block     []byte
}

func AppendFrame(out []byte, f Frame) []byte {
	out = binary.BigEndian.AppendUint64(out, f.ID)
	out = append(out, byte(f.Kind))
	switch f.Kind {
	case FrameRequest:
		return append(out, f.Body...)
	case FrameResponse:
		out = appendVarint(out, uint64(len(f.Body)))
		return append(out, f.Body...)
	case FrameError:
		return out
	default:
		assert.Unreachable("FrameKind: unhandled value")
	}
	return out
}

func DecodeFrame(payload []byte) (Frame, error) {
	r := reader{data: payload}
	id, err := r.uint64BE()
	if err != nil {
		return Frame{}, err
	}
	tag, err := r.byte1()
	if err != nil {
		return Frame{}, err
	}
	f := Frame{ID: id, Kind: FrameKind(tag)}
	if f.Kind == FrameRequest {
		f.Body = r.rest()
		return f, nil
	}
	if f.Kind == FrameError {
		return f, r.done()
	}
	if f.Kind != FrameResponse {
		return Frame{}, ErrTag
	}
	n, err := r.varint()
	if err != nil {
		return Frame{}, err
	}
	if n > uint64(len(r.data)) {
		return Frame{}, ErrShort
	}
	f.Body = r.rest()
	return f, r.done()
}

func viewKey(view uint64) []byte {
	return binary.BigEndian.AppendUint64(make([]byte, 0, uint64Bytes), view)
}

func (q ResolverRequest) Encode() []byte {
	return AppendFrame(appendVarint(nil, q.Epoch), Frame{ID: q.ID, Kind: FrameRequest, Body: viewKey(q.View)})
}

func (q ResolverResponse) Encode() []byte {
	body := AppendCertifiedPayload(nil, q.Certified)
	return AppendFrame(appendVarint(nil, q.Epoch), Frame{ID: q.ID, Kind: FrameResponse, Body: body})
}

func (q ResolverError) Encode() []byte {
	return AppendFrame(appendVarint(nil, q.Epoch), Frame{ID: q.ID, Kind: FrameError})
}

func DecodeResolver(frame []byte) (ResolverMessage, error) {
	m, err := SplitMux(frame)
	if err != nil {
		return ResolverMessage{}, err
	}
	f, err := DecodeFrame(m.Payload)
	if err != nil {
		return ResolverMessage{}, err
	}
	return ResolverMessage{Epoch: m.Subchannel, Frame: f}, nil
}

func (f Frame) View() (uint64, error) {
	if f.Kind != FrameRequest {
		return 0, ErrTag
	}
	r := reader{data: f.Body}
	view, err := r.uint64BE()
	if err != nil {
		return 0, err
	}
	return view, r.done()
}

func (f Frame) Certified() (Certified, error) {
	if f.Kind != FrameResponse {
		return Certified{}, ErrTag
	}
	return DecodeCertifiedPayload(f.Body)
}

func AppendMarshalKey(out []byte, k MarshalKey) []byte {
	out = append(out, byte(k.Kind))
	switch k.Kind {
	case KeyBlock:
		return append(out, k.Digest[:]...)
	case KeyFinalized:
		return appendVarint(out, k.Height)
	case KeyNotarized:
		return append(out, k.Round.Encode()...)
	default:
		assert.Unreachable("MarshalKeyKind: unhandled value")
	}
	return out
}

func DecodeMarshalKey(b []byte) (MarshalKey, error) {
	r := reader{data: b}
	tag, err := r.byte1()
	if err != nil {
		return MarshalKey{}, err
	}
	k := MarshalKey{Kind: MarshalKeyKind(tag)}
	if k.Kind == KeyBlock {
		d, err := r.fixed(consensus.DigestBytes)
		if err != nil {
			return MarshalKey{}, err
		}
		k.Digest = consensus.Digest(d)
		return k, r.done()
	}
	if k.Kind == KeyFinalized {
		if k.Height, err = r.varint(); err != nil {
			return MarshalKey{}, err
		}
		return k, r.done()
	}
	if k.Kind != KeyNotarized {
		return MarshalKey{}, ErrTag
	}
	if k.Round, err = readRound(&r); err != nil {
		return MarshalKey{}, err
	}
	return k, r.done()
}

func (q MarshalRequest) Encode() []byte {
	return AppendFrame(nil, Frame{ID: q.ID, Kind: FrameRequest, Body: AppendMarshalKey(nil, q.Key)})
}

func (q MarshalResponse) Encode() []byte {
	return AppendFrame(nil, Frame{ID: q.ID, Kind: FrameResponse, Body: q.Data})
}

func (q MarshalError) Encode() []byte {
	return AppendFrame(nil, Frame{ID: q.ID, Kind: FrameError})
}

func (f Frame) MarshalKey() (MarshalKey, error) {
	if f.Kind != FrameRequest {
		return MarshalKey{}, ErrTag
	}
	return DecodeMarshalKey(f.Body)
}

func (b CertifiedBlock) Encode() []byte {
	assert.Ok(b.Certified.Kind != consensus.KindNullify, "wire: a block travels with a notarization or finalization")
	out := append([]byte(nil), b.Certified.Proposal.Encode()...)
	out = appendSignatures(out, b.Certified.Certificate)
	return append(out, b.Block...)
}

func DecodeCertifiedBlock(data []byte, kind consensus.VoteKind) (CertifiedBlock, error) {
	if kind != consensus.KindNotarize && kind != consensus.KindFinalize {
		return CertifiedBlock{}, ErrTag
	}
	r := reader{data: data}
	c, err := readCertifiedBody(&r, kind)
	if err != nil {
		return CertifiedBlock{}, err
	}
	block, err := DecodeBlock(r.rest())
	if err != nil {
		return CertifiedBlock{}, err
	}
	return CertifiedBlock{Certified: c, Block: block}, nil
}
