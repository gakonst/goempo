package consensus

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

type VoteKind uint8

const (
	KindNotarize VoteKind = iota + 1
	KindNullify
	KindFinalize
)

var (
	ErrVote     = errors.New("consensus: vote does not verify")
	ErrQuorum   = errors.New("consensus: not enough votes")
	ErrVoteKind = errors.New("consensus: votes of different kinds")
)

type Vote struct {
	Kind     VoteKind
	Proposal Proposal
	Signer   int
	Vote     [SignatureBytes]byte
	Seed     [SignatureBytes]byte
}

func (v Vote) entries(ns Namespace) []signed {
	r := v.Proposal.Round.Encode()
	seed := signed{namespace: ns.Seed, message: r, signature: v.Seed}
	switch v.Kind {
	case KindNotarize:
		return []signed{{namespace: ns.Notarize, message: v.Proposal.Encode(), signature: v.Vote}, seed}
	case KindNullify:
		return []signed{{namespace: ns.Nullify, message: r, signature: v.Vote}, seed}
	case KindFinalize:
		return []signed{{namespace: ns.Finalize, message: v.Proposal.Encode(), signature: v.Vote}, seed}
	default:
		assert.Unreachable("consensus: unknown vote kind")
	}
	return nil
}

type Signer struct {
	share dkg.Share
	ns    Namespace
}

func NewSigner(share dkg.Share, ns Namespace) Signer {
	return Signer{share: share, ns: ns}
}

func (s Signer) Index() int { return s.share.Index }

func (k VoteKind) Valid() bool { return k >= KindNotarize && k <= KindFinalize }

func (s Signer) Sign(kind VoteKind, p Proposal) Vote {
	assert.Ok(kind.Valid(), "consensus: sign a known vote kind")
	v := Vote{Kind: kind, Proposal: p, Signer: s.share.Index}
	if kind == KindNullify {
		v.Proposal = Proposal{Round: p.Round}
	}
	e := v.entries(s.ns)
	v.Vote = bls12381.SignMinSig(s.share.Private, messageDST, unionUnique(e[0].namespace, e[0].message)).Bytes()
	v.Seed = bls12381.SignMinSig(s.share.Private, messageDST, unionUnique(e[1].namespace, e[1].message)).Bytes()
	return v
}

func NewIdentity(key bls12381.G2) Identity {
	return Identity{key: key, raw: key.Bytes()}
}

func VerifyVote(public bls12381.G2, ns Namespace, v Vote) error {
	if !v.Kind.Valid() {
		return ErrVote
	}
	if err := verifySameSigner(NewIdentity(public), v.entries(ns)); err != nil {
		return errors.Join(ErrVote, err)
	}
	return nil
}

func Assemble(votes []Vote) (Certificate, error) {
	if len(votes) == 0 {
		return Certificate{}, ErrQuorum
	}
	sigs := make([]dkg.Partial, len(votes))
	seeds := make([]dkg.Partial, len(votes))
	for i, v := range votes {
		if v.Kind != votes[0].Kind {
			return Certificate{}, ErrVoteKind
		}
		sig, err := bls12381.DecodeG1(v.Vote[:])
		if err != nil {
			return Certificate{}, errors.Join(ErrSignature, err)
		}
		seed, err := bls12381.DecodeG1(v.Seed[:])
		if err != nil {
			return Certificate{}, errors.Join(ErrSignature, err)
		}
		sigs[i] = dkg.Partial{Index: v.Signer, Signature: sig}
		seeds[i] = dkg.Partial{Index: v.Signer, Signature: seed}
	}
	sig, err := dkg.RecoverG1(sigs)
	if err != nil {
		return Certificate{}, err
	}
	seed, err := dkg.RecoverG1(seeds)
	if err != nil {
		return Certificate{}, err
	}
	return Certificate{Vote: sig.Bytes(), Seed: seed.Bytes()}, nil
}
