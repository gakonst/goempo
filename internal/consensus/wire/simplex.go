package wire

import (
	"math"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/kapetan-io/tiger/assert"
)

const tagFinalize byte = 2

type Certified struct {
	Kind        consensus.VoteKind
	Proposal    consensus.Proposal
	Certificate consensus.Certificate
}

type EpochVote struct {
	Epoch uint64
	Vote  consensus.Vote
}

type EpochCertified struct {
	Epoch     uint64
	Certified Certified
}

func kindTag(k consensus.VoteKind) byte {
	assert.Ok(k.Valid(), "wire: encode a known vote kind")
	return byte(k - consensus.KindNotarize)
}

func tagKind(tag byte) (consensus.VoteKind, error) {
	if tag > tagFinalize {
		return 0, ErrTag
	}
	return consensus.VoteKind(tag) + consensus.KindNotarize, nil
}

func appendSubject(out []byte, k consensus.VoteKind, p consensus.Proposal) []byte {
	if k == consensus.KindNullify {
		return append(out, p.Round.Encode()...)
	}
	return append(out, p.Encode()...)
}

func readRound(r *reader) (consensus.Round, error) {
	epoch, err := r.varint()
	if err != nil {
		return consensus.Round{}, err
	}
	view, err := r.varint()
	if err != nil {
		return consensus.Round{}, err
	}
	return consensus.Round{Epoch: epoch, View: view}, nil
}

func readProposal(r *reader) (consensus.Proposal, error) {
	round, err := readRound(r)
	if err != nil {
		return consensus.Proposal{}, err
	}
	parent, err := r.varint()
	if err != nil {
		return consensus.Proposal{}, err
	}
	payload, err := r.fixed(consensus.DigestBytes)
	if err != nil {
		return consensus.Proposal{}, err
	}
	return consensus.Proposal{Round: round, Parent: parent, Payload: consensus.Digest(payload)}, nil
}

func readSubject(r *reader, k consensus.VoteKind) (consensus.Proposal, error) {
	if k == consensus.KindNullify {
		round, err := readRound(r)
		return consensus.Proposal{Round: round}, err
	}
	return readProposal(r)
}

func readSignatures(r *reader) (consensus.Certificate, error) {
	v, err := r.fixed(consensus.SignatureBytes)
	if err != nil {
		return consensus.Certificate{}, err
	}
	s, err := r.fixed(consensus.SignatureBytes)
	if err != nil {
		return consensus.Certificate{}, err
	}
	return consensus.Certificate{Vote: [consensus.SignatureBytes]byte(v), Seed: [consensus.SignatureBytes]byte(s)}, nil
}

func appendSignatures(out []byte, c consensus.Certificate) []byte {
	return append(append(out, c.Vote[:]...), c.Seed[:]...)
}

func AppendVotePayload(out []byte, v consensus.Vote) []byte {
	assert.Ok(v.Signer >= 0, "wire: signer index is not negative")
	assert.Ok(v.Signer <= math.MaxUint32, "wire: signer index fits a u32")
	out = append(out, kindTag(v.Kind))
	out = appendSubject(out, v.Kind, v.Proposal)
	out = appendVarint(out, uint64(v.Signer))
	return appendSignatures(out, consensus.Certificate{Vote: v.Vote, Seed: v.Seed})
}

func DecodeVotePayload(payload []byte) (consensus.Vote, error) {
	r := reader{data: payload}
	tag, err := r.byte1()
	if err != nil {
		return consensus.Vote{}, err
	}
	kind, err := tagKind(tag)
	if err != nil {
		return consensus.Vote{}, err
	}
	p, err := readSubject(&r, kind)
	if err != nil {
		return consensus.Vote{}, err
	}
	signer, err := r.varint32()
	if err != nil {
		return consensus.Vote{}, err
	}
	sigs, err := readSignatures(&r)
	if err != nil {
		return consensus.Vote{}, err
	}
	v := consensus.Vote{Kind: kind, Proposal: p, Signer: int(signer), Vote: sigs.Vote, Seed: sigs.Seed}
	return v, r.done()
}

func EncodeVote(epoch uint64, v consensus.Vote) []byte {
	return AppendVotePayload(appendVarint(nil, epoch), v)
}

func DecodeVote(frame []byte) (EpochVote, error) {
	m, err := SplitMux(frame)
	if err != nil {
		return EpochVote{}, err
	}
	v, err := DecodeVotePayload(m.Payload)
	if err != nil {
		return EpochVote{}, err
	}
	return EpochVote{Epoch: m.Subchannel, Vote: v}, nil
}

func AppendCertifiedPayload(out []byte, c Certified) []byte {
	out = append(out, kindTag(c.Kind))
	out = appendSubject(out, c.Kind, c.Proposal)
	return appendSignatures(out, c.Certificate)
}

func readCertified(r *reader) (Certified, error) {
	tag, err := r.byte1()
	if err != nil {
		return Certified{}, err
	}
	kind, err := tagKind(tag)
	if err != nil {
		return Certified{}, err
	}
	return readCertifiedBody(r, kind)
}

func readCertifiedBody(r *reader, kind consensus.VoteKind) (Certified, error) {
	p, err := readSubject(r, kind)
	if err != nil {
		return Certified{}, err
	}
	sigs, err := readSignatures(r)
	if err != nil {
		return Certified{}, err
	}
	return Certified{Kind: kind, Proposal: p, Certificate: sigs}, nil
}

func DecodeCertifiedPayload(payload []byte) (Certified, error) {
	r := reader{data: payload}
	c, err := readCertified(&r)
	if err != nil {
		return Certified{}, err
	}
	return c, r.done()
}

func EncodeCertificate(epoch uint64, kind consensus.VoteKind, p consensus.Proposal, c consensus.Certificate) []byte {
	return AppendCertifiedPayload(appendVarint(nil, epoch), Certified{Kind: kind, Proposal: p, Certificate: c})
}

func DecodeCertificate(frame []byte) (EpochCertified, error) {
	m, err := SplitMux(frame)
	if err != nil {
		return EpochCertified{}, err
	}
	c, err := DecodeCertifiedPayload(m.Payload)
	if err != nil {
		return EpochCertified{}, err
	}
	return EpochCertified{Epoch: m.Subchannel, Certified: c}, nil
}
