package types

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
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

func DecodeTransaction(envelope []byte) (Transaction, error) {
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
	kind, err := s.Kind()
	if err != nil {
		return Transaction{}, err
	}
	if kind == rlp.KindList {
		tx, err := decodeLegacy(s)
		if err != nil {
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
	return DecodeTransaction(envelope)
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
	return keccak.Sum256(tx.Encode())
}

func (tx Transaction) SigningHash() primitives.Hash {
	e := rlp.NewEncoder(txEncodeHint)
	tx.Data.encodePayload(e, modeSigning)
	if tx.Type() == TxTypeLegacy {
		return keccak.Sum256(e.Encoded())
	}
	return hashPrefixed(byte(tx.Type()), e.Encoded())
}

func (tx Transaction) Sender() (primitives.Address, error) {
	return tx.Data.recoverSender(tx.SigningHash())
}

func hashPrefixed(prefix byte, payload []byte) primitives.Hash {
	return keccak.Sum256Concat([]byte{prefix}, payload)
}
