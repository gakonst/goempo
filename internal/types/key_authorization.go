package types

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	selectorBytes     = 4
	keyAuthOptionals  = 6
	keyAuthEncodeHint = 256
	emptyString       = 0x80
)

const _ = uint(keyAuthEncodeHint - keyAuthOptionals)

type TokenLimit struct {
	Token  primitives.Address
	Limit  u256.Int
	Period uint64
}

type SelectorRule struct {
	Selector   [selectorBytes]byte
	Recipients []primitives.Address
}

type CallScope struct {
	Target        primitives.Address
	SelectorRules []SelectorRule
}

type KeyAuthorization struct {
	ChainID         uint64
	KeyType         SignatureKind
	KeyID           primitives.Address
	Expiry          uint64
	HasLimits       bool
	Limits          []TokenLimit
	HasAllowedCalls bool
	AllowedCalls    []CallScope
	HasWitness      bool
	Witness         primitives.Hash
	IsAdmin         bool
	HasAccount      bool
	Account         primitives.Address
}

type SignedKeyAuthorization struct {
	Authorization KeyAuthorization
	Signature     PrimitiveSignature
}

func canonicalOptional(list *rlp.Stream) (bool, error) {
	if list.Empty() {
		return false, nil
	}
	rest := list.Rest()
	if rest[0] != emptyString {
		return true, nil
	}
	if len(rest) == 1 {
		return false, ErrNonCanonical
	}
	return false, list.Skip()
}

func nonZeroUint64(list *rlp.Stream) (uint64, error) {
	v, err := list.Uint64()
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return 0, ErrKeyAuthorization
	}
	return v, nil
}

func decodeTokenLimit(s *rlp.Stream) (TokenLimit, error) {
	var t TokenLimit
	list, err := s.List()
	if err != nil {
		return t, err
	}
	if t.Token, err = list.Address(); err != nil {
		return t, err
	}
	if t.Limit, err = readU256(&list); err != nil {
		return t, err
	}
	present, err := canonicalOptional(&list)
	if err != nil {
		return t, err
	}
	if present {
		if t.Period, err = nonZeroUint64(&list); err != nil {
			return t, err
		}
	}
	return t, list.Finish()
}

func decodeSelectorRule(s *rlp.Stream) (SelectorRule, error) {
	var r SelectorRule
	list, err := s.List()
	if err != nil {
		return r, err
	}
	if err = list.BytesFixed(r.Selector[:]); err != nil {
		return r, err
	}
	if r.Recipients, err = decodeList(&list, decodeAddress); err != nil {
		return r, err
	}
	return r, list.Finish()
}

func decodeCallScope(s *rlp.Stream) (CallScope, error) {
	var c CallScope
	list, err := s.List()
	if err != nil {
		return c, err
	}
	if c.Target, err = list.Address(); err != nil {
		return c, err
	}
	if c.SelectorRules, err = decodeList(&list, decodeSelectorRule); err != nil {
		return c, err
	}
	return c, list.Finish()
}

func decodeKeyAuthorization(s *rlp.Stream) (KeyAuthorization, error) {
	var k KeyAuthorization
	list, err := s.List()
	if err != nil {
		return k, err
	}
	if k.ChainID, err = list.Uint64(); err != nil {
		return k, err
	}
	kind, err := list.Uint64()
	if err != nil {
		return k, err
	}
	if kind > uint64(SignatureAccount) {
		return k, ErrKeyAuthorization
	}
	k.KeyType = SignatureKind(kind)
	if k.KeyID, err = list.Address(); err != nil {
		return k, err
	}
	if err = k.decodeOptionals(&list); err != nil {
		return k, err
	}
	return k, list.Finish()
}

func (k *KeyAuthorization) decodeOptionals(list *rlp.Stream) error {
	present, err := canonicalOptional(list)
	if err != nil {
		return err
	}
	if present {
		if k.Expiry, err = nonZeroUint64(list); err != nil {
			return err
		}
	}
	if present, err = canonicalOptional(list); err != nil {
		return err
	}
	if present {
		k.HasLimits = true
		if k.Limits, err = decodeList(list, decodeTokenLimit); err != nil {
			return err
		}
	}
	if present, err = canonicalOptional(list); err != nil {
		return err
	}
	if present {
		k.HasAllowedCalls = true
		if k.AllowedCalls, err = decodeList(list, decodeCallScope); err != nil {
			return err
		}
	}
	return k.decodeTail(list)
}

func (k *KeyAuthorization) decodeTail(list *rlp.Stream) error {
	present, err := canonicalOptional(list)
	if err != nil {
		return err
	}
	if present {
		k.HasWitness = true
		if k.Witness, err = list.Hash(); err != nil {
			return err
		}
	}
	if present, err = canonicalOptional(list); err != nil {
		return err
	}
	if present {
		marker, err := list.Uint64()
		if err != nil {
			return err
		}
		if marker != 1 {
			return ErrKeyAuthorization
		}
		k.IsAdmin = true
	}
	if present, err = canonicalOptional(list); err != nil {
		return err
	}
	if present {
		k.HasAccount = true
		k.Account, err = list.Address()
	}
	return err
}

func (k *KeyAuthorization) lastOptional() int {
	present := [keyAuthOptionals]bool{
		k.Expiry != 0, k.HasLimits, k.HasAllowedCalls, k.HasWitness, k.IsAdmin, k.HasAccount,
	}
	last := -1
	for i := range present {
		if present[i] {
			last = i
		}
	}
	return last
}

func (k *KeyAuthorization) encodeTo(e *rlp.Encoder) {
	e.ListStart()
	e.WriteUint64(k.ChainID)
	e.WriteUint64(uint64(k.KeyType))
	e.WriteBytes(k.KeyID[:])
	last := k.lastOptional()
	for i := 0; i <= last; i++ {
		k.encodeOptional(e, i)
	}
	e.ListEnd()
}

func (k *KeyAuthorization) encodeOptional(e *rlp.Encoder, index int) {
	switch index {
	case 0:
		e.WriteUint64(k.Expiry)
	case 1:
		k.encodeLimits(e)
	case 2:
		k.encodeCalls(e)
	case 3:
		e.WriteBytes(k.witnessBytes())
	case 4:
		e.WriteUint64(rlpBool(k.IsAdmin).uint64())
	default:
		e.WriteBytes(k.accountBytes())
	}
}

func (k *KeyAuthorization) witnessBytes() []byte {
	if k.HasWitness {
		return k.Witness[:]
	}
	return nil
}

func (k *KeyAuthorization) accountBytes() []byte {
	if k.HasAccount {
		return k.Account[:]
	}
	return nil
}

func (k *KeyAuthorization) encodeLimits(e *rlp.Encoder) {
	if !k.HasLimits {
		e.WriteBytes(nil)
		return
	}
	e.ListStart()
	for i := range k.Limits {
		l := &k.Limits[i]
		e.ListStart()
		e.WriteBytes(l.Token[:])
		writeU256(e, l.Limit)
		if l.Period != 0 {
			e.WriteUint64(l.Period)
		}
		e.ListEnd()
	}
	e.ListEnd()
}

func (k *KeyAuthorization) encodeCalls(e *rlp.Encoder) {
	if !k.HasAllowedCalls {
		e.WriteBytes(nil)
		return
	}
	e.ListStart()
	for i := range k.AllowedCalls {
		c := &k.AllowedCalls[i]
		e.ListStart()
		e.WriteBytes(c.Target[:])
		e.ListStart()
		for j := range c.SelectorRules {
			r := &c.SelectorRules[j]
			e.ListStart()
			e.WriteBytes(r.Selector[:])
			e.ListStart()
			for m := range r.Recipients {
				e.WriteBytes(r.Recipients[m][:])
			}
			e.ListEnd()
			e.ListEnd()
		}
		e.ListEnd()
		e.ListEnd()
	}
	e.ListEnd()
}

func (k *KeyAuthorization) Encode() []byte {
	e := rlp.NewEncoder(keyAuthEncodeHint)
	k.encodeTo(e)
	return e.Encoded()
}

func (k *KeyAuthorization) SigningHash() primitives.Hash {
	return keccak.Sum256(k.Encode())
}

func decodeSignedKeyAuthorization(s *rlp.Stream) (SignedKeyAuthorization, error) {
	var sk SignedKeyAuthorization
	list, err := s.List()
	if err != nil {
		return sk, err
	}
	if sk.Authorization, err = decodeKeyAuthorization(&list); err != nil {
		return sk, err
	}
	raw, err := list.Bytes()
	if err != nil {
		return sk, err
	}
	if sk.Signature, err = ParsePrimitiveSignature(raw); err != nil {
		return sk, err
	}
	return sk, list.Finish()
}

func (sk *SignedKeyAuthorization) encodeTo(e *rlp.Encoder) {
	e.ListStart()
	sk.Authorization.encodeTo(e)
	e.WriteBytes(sk.Signature.AppendBytes(nil))
	e.ListEnd()
}

func (sk *SignedKeyAuthorization) Signer() (primitives.Address, error) {
	return sk.Signature.Recover(sk.Authorization.SigningHash())
}
