package types

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/u256"
)

type TxType uint8

const (
	TxTypeLegacy     TxType = 0x00
	TxTypeAccessList TxType = 0x01
	TxTypeDynamicFee TxType = 0x02
	TxTypeSetCode    TxType = 0x04
	TxTypeTempo      TxType = 0x76
)

type encodeMode uint8

const (
	modeSigned encodeMode = iota
	modeSigning
)

const (
	vBase        = 27
	vEIP155Base  = 35
	vEIP155Steps = 2
)

const _ = uint(vEIP155Base - vBase)

type Signature struct {
	R       [scalarBytes]byte
	S       [scalarBytes]byte
	YParity bool
}

type AccessTuple struct {
	Address     primitives.Address
	StorageKeys []primitives.Hash
}

type AccessList []AccessTuple

type LegacyTx struct {
	Nonce    uint64
	GasPrice u256.Int
	Gas      uint64
	To       *primitives.Address
	Value    u256.Int
	Data     []byte
	V        uint64
	R        [scalarBytes]byte
	S        [scalarBytes]byte
}

type AccessListTx struct {
	ChainID    uint64
	Nonce      uint64
	GasPrice   u256.Int
	Gas        uint64
	To         *primitives.Address
	Value      u256.Int
	Data       []byte
	AccessList AccessList
	Sig        Signature
}

type DynamicFeeTx struct {
	ChainID              uint64
	Nonce                uint64
	MaxPriorityFeePerGas u256.Int
	MaxFeePerGas         u256.Int
	Gas                  uint64
	To                   *primitives.Address
	Value                u256.Int
	Data                 []byte
	AccessList           AccessList
	Sig                  Signature
}

type SetCodeAuthorization struct {
	ChainID u256.Int
	Address primitives.Address
	Nonce   uint64
	YParity uint8
	R       [scalarBytes]byte
	S       [scalarBytes]byte
}

type SetCodeTx struct {
	DynamicFeeTx
	AuthorizationList []SetCodeAuthorization
}

func decodeTo(s *rlp.Stream) (*primitives.Address, error) {
	b, err := s.Bytes()
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	if len(b) != primitives.AddressLength {
		return nil, rlp.ErrLength
	}
	a := primitives.BytesToAddress(b)
	return &a, nil
}

func writeTo(e *rlp.Encoder, to *primitives.Address) {
	if to == nil {
		e.WriteBytes(nil)
		return
	}
	e.WriteBytes(to[:])
}

func decodeAccessTuple(s *rlp.Stream) (AccessTuple, error) {
	var t AccessTuple
	list, err := s.List()
	if err != nil {
		return t, err
	}
	if t.Address, err = list.Address(); err != nil {
		return t, err
	}
	if t.StorageKeys, err = decodeList(&list, decodeHash); err != nil {
		return t, err
	}
	return t, list.Finish()
}

func decodeHash(s *rlp.Stream) (primitives.Hash, error) {
	return s.Hash()
}

func decodeAddress(s *rlp.Stream) (primitives.Address, error) {
	return s.Address()
}

func decodeAccessList(s *rlp.Stream) (AccessList, error) {
	return decodeList(s, decodeAccessTuple)
}

func (al AccessList) encodeTo(e *rlp.Encoder) {
	e.ListStart()
	for i := range al {
		e.ListStart()
		e.WriteBytes(al[i].Address[:])
		e.ListStart()
		for k := range al[i].StorageKeys {
			e.WriteBytes(al[i].StorageKeys[k][:])
		}
		e.ListEnd()
		e.ListEnd()
	}
	e.ListEnd()
}

func decodeSignature(s *rlp.Stream) (Signature, error) {
	var sig Signature
	var err error
	if sig.YParity, err = readBool(s); err != nil {
		return sig, err
	}
	if sig.R, err = readScalar(s); err != nil {
		return sig, err
	}
	sig.S, err = readScalar(s)
	return sig, err
}

func (sig *Signature) encodeTo(e *rlp.Encoder) {
	e.WriteUint64(rlpBool(sig.YParity).uint64())
	e.WriteUintBytes(sig.R[:])
	e.WriteUintBytes(sig.S[:])
}

func decodeLegacy(s *rlp.Stream) (*LegacyTx, error) {
	tx := &LegacyTx{}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if tx.Nonce, err = list.Uint64(); err != nil {
		return nil, err
	}
	if tx.GasPrice, err = readU128(&list); err != nil {
		return nil, err
	}
	if tx.Gas, err = list.Uint64(); err != nil {
		return nil, err
	}
	if tx.To, err = decodeTo(&list); err != nil {
		return nil, err
	}
	if tx.Value, err = readU256(&list); err != nil {
		return nil, err
	}
	if tx.Data, err = readBytesCopy(&list); err != nil {
		return nil, err
	}
	if err = tx.decodeVRS(&list); err != nil {
		return nil, err
	}
	return tx, list.Finish()
}

func (tx *LegacyTx) decodeVRS(list *rlp.Stream) error {
	v, err := list.Uint64()
	if err != nil {
		return err
	}
	if v < vBase {
		v += vBase
	}
	if v > vBase+1 {
		if v < vEIP155Base {
			return ErrSignatureV
		}
	}
	tx.V = v
	if tx.R, err = readScalar(list); err != nil {
		return err
	}
	tx.S, err = readScalar(list)
	return err
}

func (tx *LegacyTx) ChainID() (uint64, bool) {
	if tx.V < vEIP155Base {
		return 0, false
	}
	return (tx.V - vEIP155Base) / vEIP155Steps, true
}

func (tx *LegacyTx) YParity() bool {
	if tx.V < vEIP155Base {
		return tx.V == vBase+1
	}
	return (tx.V-vEIP155Base)%vEIP155Steps == 1
}

func (tx *LegacyTx) IsSystem() bool {
	var zero [scalarBytes]byte
	if tx.R != zero {
		return false
	}
	if tx.S != zero {
		return false
	}
	return !tx.YParity()
}

func (tx *LegacyTx) Type() TxType { return TxTypeLegacy }

func (tx *LegacyTx) encodePayload(e *rlp.Encoder, mode encodeMode) {
	e.ListStart()
	e.WriteUint64(tx.Nonce)
	writeU256(e, tx.GasPrice)
	e.WriteUint64(tx.Gas)
	writeTo(e, tx.To)
	writeU256(e, tx.Value)
	e.WriteBytes(tx.Data)
	if mode == modeSigned {
		e.WriteUint64(tx.V)
		e.WriteUintBytes(tx.R[:])
		e.WriteUintBytes(tx.S[:])
	} else if chainID, ok := tx.ChainID(); ok {
		e.WriteUint64(chainID)
		e.WriteUint64(0)
		e.WriteUint64(0)
	}
	e.ListEnd()
}

func (tx *LegacyTx) recoverSender(hash primitives.Hash) (primitives.Address, error) {
	if tx.IsSystem() {
		return primitives.Address{}, nil
	}
	return recoverSecp256k1(hash, Signature{R: tx.R, S: tx.S, YParity: tx.YParity()})
}

func decodeAccessListTx(s *rlp.Stream) (*AccessListTx, error) {
	tx := &AccessListTx{}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if tx.ChainID, err = list.Uint64(); err != nil {
		return nil, err
	}
	if tx.Nonce, err = list.Uint64(); err != nil {
		return nil, err
	}
	if tx.GasPrice, err = readU128(&list); err != nil {
		return nil, err
	}
	if tx.Gas, err = list.Uint64(); err != nil {
		return nil, err
	}
	if tx.To, err = decodeTo(&list); err != nil {
		return nil, err
	}
	if tx.Value, err = readU256(&list); err != nil {
		return nil, err
	}
	if tx.Data, err = readBytesCopy(&list); err != nil {
		return nil, err
	}
	if tx.AccessList, err = decodeAccessList(&list); err != nil {
		return nil, err
	}
	if tx.Sig, err = decodeSignature(&list); err != nil {
		return nil, err
	}
	return tx, list.Finish()
}

func (tx *AccessListTx) Type() TxType { return TxTypeAccessList }

func (tx *AccessListTx) encodePayload(e *rlp.Encoder, mode encodeMode) {
	e.ListStart()
	e.WriteUint64(tx.ChainID)
	e.WriteUint64(tx.Nonce)
	writeU256(e, tx.GasPrice)
	e.WriteUint64(tx.Gas)
	writeTo(e, tx.To)
	writeU256(e, tx.Value)
	e.WriteBytes(tx.Data)
	tx.AccessList.encodeTo(e)
	if mode == modeSigned {
		tx.Sig.encodeTo(e)
	}
	e.ListEnd()
}

func (tx *AccessListTx) recoverSender(hash primitives.Hash) (primitives.Address, error) {
	return recoverSecp256k1(hash, tx.Sig)
}

func decodeDynamicFeeFields(list *rlp.Stream, tx *DynamicFeeTx) error {
	var err error
	if tx.ChainID, err = list.Uint64(); err != nil {
		return err
	}
	if tx.Nonce, err = list.Uint64(); err != nil {
		return err
	}
	if tx.MaxPriorityFeePerGas, err = readU128(list); err != nil {
		return err
	}
	if tx.MaxFeePerGas, err = readU128(list); err != nil {
		return err
	}
	if tx.Gas, err = list.Uint64(); err != nil {
		return err
	}
	if tx.To, err = decodeTo(list); err != nil {
		return err
	}
	if tx.Value, err = readU256(list); err != nil {
		return err
	}
	if tx.Data, err = readBytesCopy(list); err != nil {
		return err
	}
	tx.AccessList, err = decodeAccessList(list)
	return err
}

func decodeDynamicFeeTx(s *rlp.Stream) (*DynamicFeeTx, error) {
	tx := &DynamicFeeTx{}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if err = decodeDynamicFeeFields(&list, tx); err != nil {
		return nil, err
	}
	if tx.Sig, err = decodeSignature(&list); err != nil {
		return nil, err
	}
	return tx, list.Finish()
}

func (tx *DynamicFeeTx) Type() TxType { return TxTypeDynamicFee }

func (tx *DynamicFeeTx) encodeFields(e *rlp.Encoder) {
	e.WriteUint64(tx.ChainID)
	e.WriteUint64(tx.Nonce)
	writeU256(e, tx.MaxPriorityFeePerGas)
	writeU256(e, tx.MaxFeePerGas)
	e.WriteUint64(tx.Gas)
	writeTo(e, tx.To)
	writeU256(e, tx.Value)
	e.WriteBytes(tx.Data)
	tx.AccessList.encodeTo(e)
}

func (tx *DynamicFeeTx) encodePayload(e *rlp.Encoder, mode encodeMode) {
	e.ListStart()
	tx.encodeFields(e)
	if mode == modeSigned {
		tx.Sig.encodeTo(e)
	}
	e.ListEnd()
}

func (tx *DynamicFeeTx) recoverSender(hash primitives.Hash) (primitives.Address, error) {
	return recoverSecp256k1(hash, tx.Sig)
}

func decodeSetCodeAuthorization(s *rlp.Stream) (SetCodeAuthorization, error) {
	var a SetCodeAuthorization
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
	if a.YParity, err = readByte(&list); err != nil {
		return a, err
	}
	if a.R, err = readScalar(&list); err != nil {
		return a, err
	}
	if a.S, err = readScalar(&list); err != nil {
		return a, err
	}
	return a, list.Finish()
}

func decodeSetCodeTx(s *rlp.Stream) (*SetCodeTx, error) {
	tx := &SetCodeTx{}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	if err = decodeDynamicFeeFields(&list, &tx.DynamicFeeTx); err != nil {
		return nil, err
	}
	if tx.To == nil {
		return nil, ErrCalls
	}
	if tx.AuthorizationList, err = decodeList(&list, decodeSetCodeAuthorization); err != nil {
		return nil, err
	}
	if tx.Sig, err = decodeSignature(&list); err != nil {
		return nil, err
	}
	return tx, list.Finish()
}

func (tx *SetCodeTx) Type() TxType { return TxTypeSetCode }

func (tx *SetCodeTx) encodePayload(e *rlp.Encoder, mode encodeMode) {
	e.ListStart()
	tx.encodeFields(e)
	e.ListStart()
	for i := range tx.AuthorizationList {
		a := &tx.AuthorizationList[i]
		e.ListStart()
		writeU256(e, a.ChainID)
		e.WriteBytes(a.Address[:])
		e.WriteUint64(a.Nonce)
		e.WriteUint64(uint64(a.YParity))
		e.WriteUintBytes(a.R[:])
		e.WriteUintBytes(a.S[:])
		e.ListEnd()
	}
	e.ListEnd()
	if mode == modeSigned {
		tx.Sig.encodeTo(e)
	}
	e.ListEnd()
}

func (a *SetCodeAuthorization) SigningHash() primitives.Hash {
	e := rlp.NewEncoder(authorizationHint)
	e.ListStart()
	writeU256(e, a.ChainID)
	e.WriteBytes(a.Address[:])
	e.WriteUint64(a.Nonce)
	e.ListEnd()
	return hashPrefixed(setCodeMagic, e.Encoded())
}

const (
	setCodeMagic      = 0x05
	authorizationHint = 96
)

func (a *SetCodeAuthorization) Authority() (primitives.Address, error) {
	if a.YParity > 1 {
		return primitives.Address{}, ErrSignatureV
	}
	return recoverSecp256k1(a.SigningHash(), Signature{R: a.R, S: a.S, YParity: a.YParity == 1})
}
