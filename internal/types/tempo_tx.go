package types

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	feePayerMagic       = 0x78
	feePayerPlaceholder = 0x00
	listPrefix          = 0xc0
)

type Call struct {
	To    *primitives.Address
	Value u256.Int
	Input []byte
}

type AASignedAuthorization struct {
	ChainID   u256.Int
	Address   primitives.Address
	Nonce     uint64
	Signature TempoSignature
}

type TempoTx struct {
	ChainID              uint64
	MaxPriorityFeePerGas u256.Int
	MaxFeePerGas         u256.Int
	GasLimit             uint64
	Calls                []Call
	AccessList           AccessList
	NonceKey             u256.Int
	Nonce                uint64
	ValidBefore          uint64
	ValidAfter           uint64
	FeeToken             *primitives.Address
	FeePayerSignature    *Signature
	AuthorizationList    []AASignedAuthorization
	KeyAuthorization     *SignedKeyAuthorization
	Signature            TempoSignature
}

type feePayerSlot struct {
	mode   encodeMode
	sender primitives.Address
}

const modeFeePayer encodeMode = modeSigning + 1

func decodeCall(s *rlp.Stream) (Call, error) {
	var c Call
	list, err := s.List()
	if err != nil {
		return c, err
	}
	if c.To, err = decodeTo(&list); err != nil {
		return c, err
	}
	if c.Value, err = readU256(&list); err != nil {
		return c, err
	}
	if c.Input, err = readBytesCopy(&list); err != nil {
		return c, err
	}
	return c, list.Finish()
}

func decodeAAAuthorization(s *rlp.Stream) (AASignedAuthorization, error) {
	var a AASignedAuthorization
	list, err := s.List()
	if err != nil {
		return a, err
	}
	if a.ChainID, err = readU256(&list); err != nil {
		return a, err
	}
	if a.Address, err = list.Address(); err != nil {
		return a, err
	}
	if a.Nonce, err = list.Uint64(); err != nil {
		return a, err
	}
	raw, err := list.Bytes()
	if err != nil {
		return a, err
	}
	if a.Signature, err = ParseTempoSignature(raw); err != nil {
		return a, err
	}
	return a, list.Finish()
}

func decodeTempoTx(s *rlp.Stream) (*TempoTx, error) {
	tx := &TempoTx{}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if err = tx.decodeHead(&list); err != nil {
		return nil, err
	}
	if err = tx.decodeTail(&list); err != nil {
		return nil, err
	}
	raw, err := list.Bytes()
	if err != nil {
		return nil, err
	}
	if tx.Signature, err = ParseTempoSignature(raw); err != nil {
		return nil, err
	}
	if err = list.Finish(); err != nil {
		return nil, err
	}
	return tx, tx.Validate()
}

func (tx *TempoTx) decodeHead(list *rlp.Stream) error {
	var err error
	if tx.ChainID, err = list.Uint64(); err != nil {
		return err
	}
	if tx.MaxPriorityFeePerGas, err = readU128(list); err != nil {
		return err
	}
	if tx.MaxFeePerGas, err = readU128(list); err != nil {
		return err
	}
	if tx.GasLimit, err = list.Uint64(); err != nil {
		return err
	}
	if tx.Calls, err = decodeList(list, decodeCall); err != nil {
		return err
	}
	if tx.AccessList, err = decodeAccessList(list); err != nil {
		return err
	}
	if tx.NonceKey, err = readU256(list); err != nil {
		return err
	}
	if tx.Nonce, err = list.Uint64(); err != nil {
		return err
	}
	if tx.ValidBefore, err = list.Uint64(); err != nil {
		return err
	}
	tx.ValidAfter, err = list.Uint64()
	return err
}

func (tx *TempoTx) decodeTail(list *rlp.Stream) error {
	var err error
	if tx.FeeToken, err = decodeTo(list); err != nil {
		return err
	}
	if err = tx.decodeFeePayer(list); err != nil {
		return err
	}
	if tx.AuthorizationList, err = decodeList(list, decodeAAAuthorization); err != nil {
		return err
	}
	if list.Empty() {
		return rlp.ErrEmpty
	}
	if list.Rest()[0] < listPrefix {
		return nil
	}
	ka, err := decodeSignedKeyAuthorization(list)
	if err != nil {
		return err
	}
	tx.KeyAuthorization = &ka
	return nil
}

func (tx *TempoTx) decodeFeePayer(list *rlp.Stream) error {
	kind, err := list.Kind()
	if err != nil {
		return err
	}
	if kind == rlp.KindString {
		b, err := list.Bytes()
		if err != nil {
			return err
		}
		if len(b) != 0 {
			return ErrSignature
		}
		return nil
	}
	inner, err := list.List()
	if err != nil {
		return err
	}
	sig, err := decodeSignature(&inner)
	if err != nil {
		return err
	}
	tx.FeePayerSignature = &sig
	return inner.Finish()
}

func (tx *TempoTx) Validate() error {
	if len(tx.Calls) == 0 {
		return ErrCalls
	}
	for i := 1; i < len(tx.Calls); i++ {
		if tx.Calls[i].To == nil {
			return ErrCalls
		}
	}
	if tx.Calls[0].To == nil {
		if len(tx.AuthorizationList) > 0 {
			return ErrCalls
		}
	}
	if tx.ValidBefore != 0 {
		if tx.ValidAfter != 0 {
			if tx.ValidBefore <= tx.ValidAfter {
				return ErrValidity
			}
		}
	}
	return nil
}

func (tx *TempoTx) Type() TxType { return TxTypeTempo }

func (tx *TempoTx) encodePayload(e *rlp.Encoder, mode encodeMode) {
	tx.encodeWith(e, feePayerSlot{mode: mode})
}

func (tx *TempoTx) encodeWith(e *rlp.Encoder, slot feePayerSlot) {
	e.ListStart()
	e.WriteUint64(tx.ChainID)
	writeU256(e, tx.MaxPriorityFeePerGas)
	writeU256(e, tx.MaxFeePerGas)
	e.WriteUint64(tx.GasLimit)
	e.ListStart()
	for i := range tx.Calls {
		c := &tx.Calls[i]
		e.ListStart()
		writeTo(e, c.To)
		writeU256(e, c.Value)
		e.WriteBytes(c.Input)
		e.ListEnd()
	}
	e.ListEnd()
	tx.AccessList.encodeTo(e)
	writeU256(e, tx.NonceKey)
	e.WriteUint64(tx.Nonce)
	e.WriteUint64(tx.ValidBefore)
	e.WriteUint64(tx.ValidAfter)
	tx.encodeFeeFields(e, slot)
	tx.encodeAuthorizations(e)
	if tx.KeyAuthorization != nil {
		tx.KeyAuthorization.encodeTo(e)
	}
	if slot.mode == modeSigned {
		e.WriteBytes(tx.Signature.Bytes())
	}
	e.ListEnd()
}

func (tx *TempoTx) encodeFeeFields(e *rlp.Encoder, slot feePayerSlot) {
	sponsored := tx.FeePayerSignature != nil
	if slot.mode == modeSigning && sponsored {
		e.WriteBytes(nil)
	} else {
		writeTo(e, tx.FeeToken)
	}
	switch {
	case slot.mode == modeFeePayer:
		e.WriteBytes(slot.sender[:])
	case slot.mode == modeSigning && sponsored:
		e.WriteRaw([]byte{feePayerPlaceholder})
	case sponsored:
		e.ListStart()
		tx.FeePayerSignature.encodeTo(e)
		e.ListEnd()
	default:
		e.WriteBytes(nil)
	}
}

func (tx *TempoTx) encodeAuthorizations(e *rlp.Encoder) {
	e.ListStart()
	for i := range tx.AuthorizationList {
		a := &tx.AuthorizationList[i]
		e.ListStart()
		writeU256(e, a.ChainID)
		e.WriteBytes(a.Address[:])
		e.WriteUint64(a.Nonce)
		e.WriteBytes(a.Signature.Bytes())
		e.ListEnd()
	}
	e.ListEnd()
}

func (tx *TempoTx) recoverSender(hash primitives.Hash) (primitives.Address, error) {
	return tx.Signature.Recover(hash)
}

func (tx *TempoTx) FeePayerSigningHash(sender primitives.Address) primitives.Hash {
	e := rlp.NewEncoder(txEncodeHint)
	tx.encodeWith(e, feePayerSlot{mode: modeFeePayer, sender: sender})
	return hashPrefixed(feePayerMagic, e.Encoded())
}

func (tx *TempoTx) FeePayer(sender primitives.Address) (primitives.Address, error) {
	if tx.FeePayerSignature == nil {
		return sender, nil
	}
	return recoverSecp256k1(tx.FeePayerSigningHash(sender), *tx.FeePayerSignature)
}

func (a *AASignedAuthorization) SigningHash() primitives.Hash {
	e := rlp.NewEncoder(authorizationHint)
	e.ListStart()
	writeU256(e, a.ChainID)
	e.WriteBytes(a.Address[:])
	e.WriteUint64(a.Nonce)
	e.ListEnd()
	return hashPrefixed(setCodeMagic, e.Encoded())
}

func (a *AASignedAuthorization) Authority() (primitives.Address, error) {
	return a.Signature.Recover(a.SigningHash())
}
