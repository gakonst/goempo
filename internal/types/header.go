package types

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	BloomLength       = 256
	NonceLength       = 8
	ProposerKeyLength = 32
	headerBaseFields  = 15
	headerOptionalMax = 6
	headerFieldsMax   = headerBaseFields + headerOptionalMax
)

const _ = uint(headerFieldsMax - headerBaseFields - headerOptionalMax)

type Bloom [BloomLength]byte

type EthHeader struct {
	ParentHash            primitives.Hash
	OmmersHash            primitives.Hash
	Beneficiary           primitives.Address
	StateRoot             primitives.Hash
	TransactionsRoot      primitives.Hash
	ReceiptsRoot          primitives.Hash
	LogsBloom             Bloom
	Difficulty            u256.Int
	Number                uint64
	GasLimit              uint64
	GasUsed               uint64
	Timestamp             uint64
	ExtraData             []byte
	MixHash               primitives.Hash
	Nonce                 [NonceLength]byte
	OptionalFields        int
	BaseFeePerGas         uint64
	WithdrawalsRoot       primitives.Hash
	BlobGasUsed           uint64
	ExcessBlobGas         uint64
	ParentBeaconBlockRoot primitives.Hash
	RequestsHash          primitives.Hash
}

type ConsensusContext struct {
	Epoch      uint64
	View       uint64
	ParentView uint64
	Proposer   [ProposerKeyLength]byte
}

type Header struct {
	GeneralGasLimit     uint64
	SharedGasLimit      uint64
	TimestampMillisPart uint64
	Inner               EthHeader
	Consensus           *ConsensusContext
}

func DecodeHeader(b []byte) (Header, error) {
	if len(b) > encodedBytesMax {
		return Header{}, ErrTooLarge
	}
	s := rlp.NewStream(b)
	h, err := decodeHeader(&s)
	if err != nil {
		return Header{}, err
	}
	return h, s.Finish()
}

func decodeHeader(s *rlp.Stream) (Header, error) {
	var h Header
	list, err := s.List()
	if err != nil {
		return h, err
	}
	if h.GeneralGasLimit, err = list.Uint64(); err != nil {
		return h, err
	}
	if h.SharedGasLimit, err = list.Uint64(); err != nil {
		return h, err
	}
	if h.TimestampMillisPart, err = list.Uint64(); err != nil {
		return h, err
	}
	if h.Inner, err = decodeEthHeader(&list); err != nil {
		return h, err
	}
	if !list.Empty() {
		c, err := decodeConsensus(&list)
		if err != nil {
			return h, err
		}
		h.Consensus = &c
	}
	return h, list.Finish()
}

func decodeConsensus(s *rlp.Stream) (ConsensusContext, error) {
	var c ConsensusContext
	list, err := s.List()
	if err != nil {
		return c, err
	}
	if c.Epoch, err = list.Uint64(); err != nil {
		return c, err
	}
	if c.View, err = list.Uint64(); err != nil {
		return c, err
	}
	if c.ParentView, err = list.Uint64(); err != nil {
		return c, err
	}
	if err = list.BytesFixed(c.Proposer[:]); err != nil {
		return c, err
	}
	return c, list.Finish()
}

func decodeEthHeader(s *rlp.Stream) (EthHeader, error) {
	var h EthHeader
	list, err := s.List()
	if err != nil {
		return h, err
	}
	if err = decodeEthBase(&list, &h); err != nil {
		if errors.Is(err, rlp.ErrEmpty) {
			return h, ErrHeaderFields
		}
		return h, err
	}
	optional, err := list.Count()
	if err != nil {
		return h, err
	}
	if optional > headerFieldsMax-headerBaseFields {
		return h, ErrHeaderFields
	}
	h.OptionalFields = optional
	if err = decodeEthOptional(&list, &h); err != nil {
		return h, err
	}
	return h, list.Finish()
}

func decodeEthBase(list *rlp.Stream, h *EthHeader) error {
	var err error
	if h.ParentHash, err = list.Hash(); err != nil {
		return err
	}
	if h.OmmersHash, err = list.Hash(); err != nil {
		return err
	}
	if h.Beneficiary, err = list.Address(); err != nil {
		return err
	}
	if h.StateRoot, err = list.Hash(); err != nil {
		return err
	}
	if h.TransactionsRoot, err = list.Hash(); err != nil {
		return err
	}
	if h.ReceiptsRoot, err = list.Hash(); err != nil {
		return err
	}
	if err = list.BytesFixed(h.LogsBloom[:]); err != nil {
		return err
	}
	if h.Difficulty, err = readU256(list); err != nil {
		return err
	}
	return decodeEthCounters(list, h)
}

func decodeEthCounters(list *rlp.Stream, h *EthHeader) error {
	var err error
	if h.Number, err = list.Uint64(); err != nil {
		return err
	}
	if h.GasLimit, err = list.Uint64(); err != nil {
		return err
	}
	if h.GasUsed, err = list.Uint64(); err != nil {
		return err
	}
	if h.Timestamp, err = list.Uint64(); err != nil {
		return err
	}
	if h.ExtraData, err = readBytesCopy(list); err != nil {
		return err
	}
	if h.MixHash, err = list.Hash(); err != nil {
		return err
	}
	return list.BytesFixed(h.Nonce[:])
}

func decodeEthOptional(list *rlp.Stream, h *EthHeader) error {
	var err error
	k := h.OptionalFields
	if k > 0 {
		if h.BaseFeePerGas, err = list.Uint64(); err != nil {
			return err
		}
	}
	if k > 1 {
		if h.WithdrawalsRoot, err = list.Hash(); err != nil {
			return err
		}
	}
	if k > 2 {
		if h.BlobGasUsed, err = list.Uint64(); err != nil {
			return err
		}
	}
	if k > 3 {
		if h.ExcessBlobGas, err = list.Uint64(); err != nil {
			return err
		}
	}
	if k > 4 {
		if h.ParentBeaconBlockRoot, err = list.Hash(); err != nil {
			return err
		}
	}
	if k > 5 {
		if h.RequestsHash, err = list.Hash(); err != nil {
			return err
		}
	}
	return nil
}

func (h *Header) Encode() []byte {
	e := rlp.NewEncoder(headerEncodedHint)
	h.encodeTo(e)
	return e.Encoded()
}

const headerEncodedHint = 768

func (h *Header) encodeTo(e *rlp.Encoder) {
	e.ListStart()
	e.WriteUint64(h.GeneralGasLimit)
	e.WriteUint64(h.SharedGasLimit)
	e.WriteUint64(h.TimestampMillisPart)
	h.Inner.encodeTo(e)
	if h.Consensus != nil {
		c := h.Consensus
		e.ListStart()
		e.WriteUint64(c.Epoch)
		e.WriteUint64(c.View)
		e.WriteUint64(c.ParentView)
		e.WriteBytes(c.Proposer[:])
		e.ListEnd()
	}
	e.ListEnd()
}

func (h *EthHeader) encodeTo(e *rlp.Encoder) {
	e.ListStart()
	e.WriteBytes(h.ParentHash[:])
	e.WriteBytes(h.OmmersHash[:])
	e.WriteBytes(h.Beneficiary[:])
	e.WriteBytes(h.StateRoot[:])
	e.WriteBytes(h.TransactionsRoot[:])
	e.WriteBytes(h.ReceiptsRoot[:])
	e.WriteBytes(h.LogsBloom[:])
	writeU256(e, h.Difficulty)
	e.WriteUint64(h.Number)
	e.WriteUint64(h.GasLimit)
	e.WriteUint64(h.GasUsed)
	e.WriteUint64(h.Timestamp)
	e.WriteBytes(h.ExtraData)
	e.WriteBytes(h.MixHash[:])
	e.WriteBytes(h.Nonce[:])
	h.encodeOptional(e)
	e.ListEnd()
}

func (h *EthHeader) encodeOptional(e *rlp.Encoder) {
	k := h.OptionalFields
	if k > 0 {
		e.WriteUint64(h.BaseFeePerGas)
	}
	if k > 1 {
		e.WriteBytes(h.WithdrawalsRoot[:])
	}
	if k > 2 {
		e.WriteUint64(h.BlobGasUsed)
	}
	if k > 3 {
		e.WriteUint64(h.ExcessBlobGas)
	}
	if k > 4 {
		e.WriteBytes(h.ParentBeaconBlockRoot[:])
	}
	if k > 5 {
		e.WriteBytes(h.RequestsHash[:])
	}
}

func (h *Header) Hash() primitives.Hash {
	return keccak.Sum256(h.Encode())
}

func (h *Header) TimestampMillis() uint64 {
	return h.Inner.Timestamp*1000 + h.TimestampMillisPart
}
