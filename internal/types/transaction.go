package types

import (
	"bytes"
	"sync"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/kapetan-io/tiger/assert"
)

const txEncodeHint = 512

type TxData interface {
	Type() TxType
	encodePayload(e *rlp.Encoder, mode encodeMode)
	recoverSender(hash primitives.Hash) (primitives.Address, error)
}

type Transaction struct {
	Data TxData
}

// DecodeTransaction decodes a copy of envelope, so the transaction never
// aliases the caller's buffer.
func DecodeTransaction(envelope []byte) (Transaction, error) {
	if len(envelope) == 0 {
		return Transaction{}, ErrEmptyTx
	}
	if len(envelope) > encodedBytesMax {
		return Transaction{}, ErrTooLarge
	}
	return decodeTransaction(bytes.Clone(envelope))
}

// decodeTransaction decodes envelope in place: byte fields alias it.
func decodeTransaction(envelope []byte) (Transaction, error) {
	if len(envelope) == 0 {
		return Transaction{}, ErrEmptyTx
	}
	if len(envelope) > encodedBytesMax {
		return Transaction{}, ErrTooLarge
	}
	if envelope[0] >= listPrefix {
		s := rlp.NewStream(envelope)
		tx, err := decodeLegacy(&s)
		if err != nil {
			return Transaction{}, err
		}
		return Transaction{Data: tx}, s.Finish()
	}
	s := rlp.NewStream(envelope[1:])
	data, err := decodeTyped(TxType(envelope[0]), &s)
	if err != nil {
		return Transaction{}, err
	}
	return Transaction{Data: data}, s.Finish()
}

var typedDecoders = map[TxType]decodeFunc[TxData]{
	TxTypeAccessList: func(s *rlp.Stream) (TxData, error) { return decodeAccessListTx(s) },
	TxTypeDynamicFee: func(s *rlp.Stream) (TxData, error) { return decodeDynamicFeeTx(s) },
	TxTypeSetCode:    func(s *rlp.Stream) (TxData, error) { return decodeSetCodeTx(s) },
	TxTypeTempo:      func(s *rlp.Stream) (TxData, error) { return decodeTempoTx(s) },
}

func decodeTyped(kind TxType, s *rlp.Stream) (TxData, error) {
	decode, ok := typedDecoders[kind]
	if !ok {
		return nil, ErrTxType
	}
	return decode(s)
}

func decodeBodyTransaction(s *rlp.Stream) (Transaction, error) {
	return (*BlockDecoder)(nil).decodeBodyTransaction(s)
}

func (d *BlockDecoder) decodeBodyTransaction(s *rlp.Stream) (Transaction, error) {
	kind, err := s.Kind()
	if err != nil {
		return Transaction{}, err
	}
	if kind == rlp.KindList {
		tx := d.newLegacy()
		if err := tx.decode(s, d); err != nil {
			return Transaction{}, err
		}
		return Transaction{Data: tx}, nil
	}
	envelope, err := s.Bytes()
	if err != nil {
		return Transaction{}, err
	}
	if len(envelope) == 0 {
		return Transaction{}, ErrEmptyTx
	}
	if envelope[0] >= listPrefix {
		return Transaction{}, ErrTxType
	}
	return decodeTransaction(envelope)
}

func (tx Transaction) Type() TxType { return tx.Data.Type() }

func (tx Transaction) Encode() []byte {
	e := rlp.NewEncoder(txEncodeHint)
	tx.Data.encodePayload(e, modeSigned)
	payload := e.Encoded()
	if tx.Type() == TxTypeLegacy {
		return payload
	}
	out := make([]byte, 0, len(payload)+1)
	out = append(out, byte(tx.Type()))
	return append(out, payload...)
}

func (tx Transaction) encodeInBody(e *rlp.Encoder) {
	if tx.Type() == TxTypeLegacy {
		tx.Data.encodePayload(e, modeSigned)
		return
	}
	e.WriteBytes(tx.Encode())
}

func (tx Transaction) Hash() primitives.Hash {
	e := borrowEncoder()
	defer returnEncoder(e)
	tx.Data.encodePayload(e, modeSigned)
	if tx.Type() == TxTypeLegacy {
		return keccak.Sum256(e.Encoded())
	}
	return hashPrefixed(byte(tx.Type()), e.Encoded())
}

func (tx Transaction) SigningHash() primitives.Hash {
	e := borrowEncoder()
	defer returnEncoder(e)
	tx.Data.encodePayload(e, modeSigning)
	if tx.Type() == TxTypeLegacy {
		return keccak.Sum256(e.Encoded())
	}
	return hashPrefixed(byte(tx.Type()), e.Encoded())
}

func (tx Transaction) UniqueID(sender primitives.Address) primitives.Hash {
	e := borrowEncoder()
	defer returnEncoder(e)
	tx.Data.encodePayload(e, modeSigning)
	if tx.Type() == TxTypeLegacy {
		return keccak.Sum256Concat(e.Encoded(), sender[:])
	}
	return keccak.Sum256Concat([]byte{byte(tx.Type())}, e.Encoded(), sender[:])
}

// encoderRetainMax caps the buffer a pooled encoder keeps, so one huge
// transaction does not pin its encoding in the pool.
const encoderRetainMax = 1 << 20

const _ = uint(encoderRetainMax - txEncodeHint)

var encoders = sync.Pool{New: func() any { return rlp.NewEncoder(txEncodeHint) }}

// borrowEncoder returns an empty encoder whose output is only valid until
// returnEncoder: callers hash it and never keep it.
func borrowEncoder() *rlp.Encoder {
	e, ok := encoders.Get().(*rlp.Encoder)
	assert.Ok(ok, "encoder pool holds encoders")
	e.Reset()
	return e
}

func returnEncoder(e *rlp.Encoder) {
	if e.Cap() > encoderRetainMax {
		return
	}
	encoders.Put(e)
}

func (tx Transaction) Sender() (primitives.Address, error) {
	return tx.Data.recoverSender(tx.SigningHash())
}

func hashPrefixed(prefix byte, payload []byte) primitives.Hash {
	return keccak.Sum256Concat([]byte{prefix}, payload)
}
