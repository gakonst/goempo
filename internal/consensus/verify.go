package consensus

import (
	"crypto/sha256"
	"errors"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
)

const (
	notarizeSuffix   = "_NOTARIZE"
	nullifySuffix    = "_NULLIFY"
	finalizeSuffix   = "_FINALIZE"
	seedSuffix       = "_SEED"
	combinerBytes    = 16
	TempoNamespace   = "TEMPO"
	messageDSTString = "BLS_SIG_BLS12381G1_XMD:SHA-256_SSWU_RO_POP_"
)

var (
	ErrSignature   = errors.New("consensus: signature does not decode")
	ErrCertificate = errors.New("consensus: certificate does not verify")
	ErrIdentity    = errors.New("consensus: identity does not decode")
)

var messageDST = bls12381.DST(messageDSTString)

type Namespace struct {
	Notarize Domain
	Nullify  Domain
	Finalize Domain
	Seed     Domain
}

func NewNamespace(base string) Namespace {
	return Namespace{
		Notarize: Domain(base + notarizeSuffix),
		Nullify:  Domain(base + nullifySuffix),
		Finalize: Domain(base + finalizeSuffix),
		Seed:     Domain(base + seedSuffix),
	}
}

type Identity struct {
	key bls12381.G2
	raw [IdentityBytes]byte
}

func DecodeIdentity(b []byte) (Identity, error) {
	key, err := bls12381.DecodeG2(b)
	if err != nil {
		return Identity{}, errors.Join(ErrIdentity, err)
	}
	return Identity{key: key, raw: key.Bytes()}, nil
}

func (id Identity) Bytes() [IdentityBytes]byte { return id.raw }

func (id Identity) Key() bls12381.G2 { return id.key }

type signed struct {
	namespace Domain
	message   []byte
	signature [SignatureBytes]byte
}

func verifySameSigner(id Identity, entries []signed) error {
	h := sha256.New()
	h.Write(id.raw[:])
	sigs := make([]bls12381.G1, 0, len(entries))
	for _, e := range entries {
		sig, err := bls12381.DecodeG1(e.signature[:])
		if err != nil {
			return errors.Join(ErrSignature, err)
		}
		sigs = append(sigs, sig)
		h.Write(e.signature[:])
		h.Write(unionUnique(e.namespace, e.message))
	}
	seed := h.Sum(nil)
	var aggSig, aggMsg bls12381.G1
	for i, e := range entries {
		k := combiner(seed, i)
		aggSig = aggSig.Add(sigs[i].Mul(k))
		aggMsg = aggMsg.Add(bls12381.HashToG1(messageDST, unionUnique(e.namespace, e.message)).Mul(k))
	}
	if aggSig.IsIdentity() || aggMsg.IsIdentity() {
		return ErrCertificate
	}
	ok := bls12381.PairingIsOne([]bls12381.PairingTerm{
		{A: aggSig, B: bls12381.G2Generator().Neg()},
		{A: aggMsg, B: id.key},
	})
	if !ok {
		return ErrCertificate
	}
	return nil
}

func combiner(seed []byte, i int) bls12381.Scalar {
	if i == 0 {
		return bls12381.ScalarFromUint64(1)
	}
	d := sha256.Sum256(append(append([]byte(nil), seed...), byte(i)))
	return bls12381.ScalarFromWide(d[:combinerBytes])
}

func VerifyFinalization(id Identity, ns Namespace, f Finalization) error {
	return verifySameSigner(id, []signed{
		{namespace: ns.Finalize, message: f.Proposal.Encode(), signature: f.Certificate.Vote},
		{namespace: ns.Seed, message: f.Proposal.Round.Encode(), signature: f.Certificate.Seed},
	})
}

func VerifyNotarization(id Identity, ns Namespace, p Proposal, c Certificate) error {
	return verifySameSigner(id, []signed{
		{namespace: ns.Notarize, message: p.Encode(), signature: c.Vote},
		{namespace: ns.Seed, message: p.Round.Encode(), signature: c.Seed},
	})
}

func VerifyNullification(id Identity, ns Namespace, r Round, c Certificate) error {
	return verifySameSigner(id, []signed{
		{namespace: ns.Nullify, message: r.Encode(), signature: c.Vote},
		{namespace: ns.Seed, message: r.Encode(), signature: c.Seed},
	})
}
