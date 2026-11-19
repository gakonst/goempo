// Package types implements Tempo transaction, block and receipt formats.
// EncodingScratch belongs to one serial operation owner and must not be copied
// or shared concurrently. It recomputes all encodings and hashes; returned bytes
// and hashes are independent values that remain valid after scratch reuse.
package types

import (
	"bytes"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

type EncodingScratch struct {
	encoder *rlp.Encoder
	hasher  *keccak.Hasher
	prefix  [1]byte
	sender  primitives.Address
}

func NewEncodingScratch() *EncodingScratch {
	return &EncodingScratch{encoder: rlp.NewEncoder(txEncodeHint), hasher: keccak.New()}
}

func (s *EncodingScratch) transaction(tx Transaction, mode encodeMode) {
	s.encoder.Reset()
	if tx.Type() != TxTypeLegacy {
		s.prefix[0] = byte(tx.Type())
		s.encoder.WriteRaw(s.prefix[:])
	}
	tx.Data.encodePayload(s.encoder, mode)
}

func (s *EncodingScratch) EncodeTransaction(tx Transaction) []byte {
	s.transaction(tx, modeSigned)
	return bytes.Clone(s.encoder.Encoded())
}

func (s *EncodingScratch) TransactionHash(tx Transaction) primitives.Hash {
	s.transaction(tx, modeSigned)
	return s.hasher.Hash(s.encoder.Encoded())
}

func (s *EncodingScratch) SigningHash(tx Transaction) primitives.Hash {
	s.transaction(tx, modeSigning)
	return s.hasher.Hash(s.encoder.Encoded())
}

func (s *EncodingScratch) UniqueID(tx Transaction, sender primitives.Address) primitives.Hash {
	s.transaction(tx, modeSigning)
	s.hasher.Reset()
	s.hasher.Write(s.encoder.Encoded())
	s.sender = sender
	s.hasher.Write(s.sender[:])
	return s.hasher.Sum()
}

func (s *EncodingScratch) EncodeReceipt(r *Receipt) []byte {
	s.encoder.Reset()
	r.encodeTo(s.encoder)
	return bytes.Clone(s.encoder.Encoded())
}

func (s *EncodingScratch) LogsBloom(logs []Log) Bloom {
	var b Bloom
	for i := range logs {
		b.addHash(s.hasher.Hash(logs[i].Address[:]))
		for k := range logs[i].Topics {
			b.addHash(s.hasher.Hash(logs[i].Topics[k][:]))
		}
	}
	return b
}
