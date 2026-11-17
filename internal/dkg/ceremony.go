package dkg

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
)

const (
	DKGNamespace   = "_COMMONWARE_CRYPTOGRAPHY_BLS12381_DKG"
	TempoNamespace = "TEMPO"
	RevealV0       = 0
	RevealV1       = 1
	roundLen       = 8
)

var (
	ErrEmptySet       = errors.New("dkg: dealers and players must be non-empty")
	ErrPrevious       = errors.New("dkg: previous output is inconsistent")
	ErrUnknownDealer  = errors.New("dkg: dealer is not in the round")
	ErrUnknownPlayer  = errors.New("dkg: player is not in the round")
	ErrTooFewDealers  = errors.New("dkg: fewer dealers than the previous quorum")
	ErrCommitment     = errors.New("dkg: commitment does not fit the round")
	ErrShare          = errors.New("dkg: share does not match the commitment")
	ErrAck            = errors.New("dkg: invalid ack signature")
	ErrNoShare        = errors.New("dkg: no share from a selected dealer")
	ErrPreviousShare  = errors.New("dkg: a reshare dealer needs its previous share")
	ErrNotEnoughLogs  = errors.New("dkg: not enough available dealer logs")
	ErrDealingChanged = errors.New("dkg: dealer log commits to another polynomial")
)

type RoundConfig struct {
	Round    uint64
	Previous *Output
	Reveal   byte
	Dealers  KeySet
	Players  KeySet
}

type Info struct {
	RoundConfig
	Summary Summary
}

func NewInfo(namespace []byte, cfg RoundConfig) (*Info, error) {
	if len(cfg.Dealers) == 0 || len(cfg.Players) == 0 {
		return nil, ErrEmptySet
	}
	if len(cfg.Dealers) > ParticipantsMax || len(cfg.Players) > ParticipantsMax {
		return nil, ErrRange
	}
	if prior := cfg.Previous; prior != nil {
		if int(prior.Public.Total) != len(prior.Players) || len(prior.Public.Poly) != prior.Players.Quorum() {
			return nil, ErrPrevious
		}
		for _, d := range cfg.Dealers {
			if _, ok := prior.Players.Index(d); !ok {
				return nil, ErrUnknownDealer
			}
		}
		if len(cfg.Dealers) < prior.Players.Quorum() {
			return nil, ErrTooFewDealers
		}
	}
	info := &Info{RoundConfig: cfg}
	info.Summary = info.computeSummary(namespace)
	return info, nil
}

func (info *Info) computeSummary(namespace []byte) Summary {
	t := NewTranscript([]byte(DKGNamespace))
	t.Commit(namespace)
	var round [roundLen]byte
	binary.BigEndian.PutUint64(round[:], info.Round)
	t.Commit(round[:])
	if info.Previous != nil {
		t.Commit(append([]byte{1}, info.Previous.Encode()...))
	} else {
		t.Commit([]byte{0})
	}
	t.Commit(appendKeySet(nil, info.Dealers))
	t.Commit(appendKeySet(nil, info.Players))
	if info.Reveal == RevealV1 {
		t.Commit([]byte{0x80, 0x01})
	}
	return t.Summarize()
}

func (info *Info) Degree() int { return info.Players.Quorum() - 1 }

func (info *Info) RequiredCommitments() int {
	if info.Previous != nil {
		return max(info.Dealers.Quorum(), info.Previous.Players.Quorum())
	}
	return info.Dealers.Quorum()
}

func (info *Info) MaxReveals() int { return info.Players.MaxFaults() }

func (info *Info) RevealThreshold() int {
	if info.Reveal == RevealV0 {
		return info.Players.MaxFaults() + 1
	}
	faults := info.Dealers.MaxFaults()
	if info.Previous != nil {
		faults = info.Previous.Players.MaxFaults()
	}
	return info.RequiredCommitments() - faults
}

func (info *Info) AckSummary(dealer PublicKey, c Commitment) Summary {
	t := ResumeTranscript(info.Summary)
	f := t.Fork([]byte("ack"))
	f.Commit(dealer[:])
	f.Commit(appendCommitment(nil, c))
	return f.Summarize()
}

func (info *Info) LogSummary(log DealerLog) Summary {
	t := ResumeTranscript(info.Summary)
	f := t.Fork([]byte("log"))
	f.Commit(log.Encode())
	return f.Summarize()
}

func (info *Info) checkCommitment(dealer PublicKey, c Commitment) bool {
	if len(c) != info.Degree()+1 {
		return false
	}
	if prior := info.Previous; prior != nil {
		index, ok := prior.Players.Index(dealer)
		if !ok {
			return false
		}
		return c.Constant().Equal(prior.Public.Poly.Eval(index))
	}
	return true
}

func summaryMessage(s Summary) []byte {
	return append([]byte{0}, s[:]...)
}

func SignSummary(key ed25519.PrivateKey, s Summary) [SignatureLen]byte {
	return [SignatureLen]byte(ed25519.Sign(key, summaryMessage(s)))
}

func VerifySummary(pk PublicKey, s Summary, sig [SignatureLen]byte) bool {
	return ed25519.Verify(pk[:], summaryMessage(s), sig[:])
}

type LogStatus int

const (
	LogAvailable LogStatus = iota
	LogUnavailable
	LogFaulty
	LogUnexpectedDealer
)

func (info *Info) CheckDealerLog(dealer PublicKey, log DealerLog) LogStatus {
	if _, ok := info.Dealers.Index(dealer); !ok {
		return LogUnexpectedDealer
	}
	if !info.checkCommitment(dealer, log.Commitment) {
		return LogFaulty
	}
	if log.Results == nil {
		return LogUnavailable
	}
	if !log.Results.Players.Equal(info.Players) {
		return LogFaulty
	}
	ack := info.AckSummary(dealer, log.Commitment)
	reveals := 0
	g := bls12381.G2Generator()
	for i, v := range log.Results.Values {
		if !v.Reveal {
			if !VerifySummary(info.Players[i], ack, v.Ack) {
				return LogFaulty
			}
			continue
		}
		reveals++
		if reveals > info.MaxReveals() || !g.Mul(v.Share).Equal(log.Commitment.Eval(i)) {
			return LogFaulty
		}
	}
	return LogAvailable
}

func (info *Info) CheckSignedDealerLog(s SignedDealerLog) bool {
	return VerifySummary(s.Dealer, info.LogSummary(s.Log), s.Signature)
}

type DealerEntry struct {
	Dealer PublicKey
	Log    DealerLog
}

func (info *Info) Select(logs []DealerEntry) ([]int, error) {
	required := info.RequiredCommitments()
	out := make([]int, 0, required)
	for i, e := range logs {
		if len(out) == required {
			break
		}
		if info.CheckDealerLog(e.Dealer, e.Log) == LogAvailable {
			out = append(out, i)
		}
	}
	if len(out) != required {
		return nil, ErrNotEnoughLogs
	}
	return out, nil
}

func (info *Info) reshareWeights(logs []DealerEntry, selection []int) ([]bls12381.Scalar, error) {
	indices := make([]int, len(selection))
	for k, i := range selection {
		index, ok := info.Previous.Players.Index(logs[i].Dealer)
		if !ok {
			return nil, ErrUnknownDealer
		}
		indices[k] = index
	}
	return LagrangeAtZero(indices)
}

func (info *Info) Reckon(logs []DealerEntry, selection []int) (Output, error) {
	out := Output{Summary: info.Summary, Players: info.Players, Dealers: KeySet{}, Revealed: KeySet{}}
	counts := make([]int, len(info.Players))
	for _, i := range selection {
		for p, v := range logs[i].Log.Results.Values {
			if v.Reveal {
				counts[p]++
			}
		}
		out.Dealers = append(out.Dealers, logs[i].Dealer)
	}
	for p, n := range counts {
		if n >= info.RevealThreshold() {
			out.Revealed = append(out.Revealed, info.Players[p])
		}
	}
	out.Public.Total = uint32(len(info.Players))
	weights, err := info.combineWeights(logs, selection)
	if err != nil {
		return Output{}, err
	}
	out.Public.Poly = make(Commitment, info.Degree()+1)
	for c := range out.Public.Poly {
		for k, i := range selection {
			term := logs[i].Log.Commitment[c].Mul(weights[k])
			if k == 0 {
				out.Public.Poly[c] = term
				continue
			}
			out.Public.Poly[c] = out.Public.Poly[c].Add(term)
		}
	}
	return out, nil
}

func (info *Info) combineWeights(logs []DealerEntry, selection []int) ([]bls12381.Scalar, error) {
	if info.Previous != nil {
		return info.reshareWeights(logs, selection)
	}
	ones := make([]bls12381.Scalar, len(selection))
	for i := range ones {
		ones[i] = bls12381.ScalarFromUint64(1)
	}
	return ones, nil
}

type Share struct {
	Index   int
	Private bls12381.Scalar
}

func (s Share) Public() bls12381.G2 { return bls12381.G2Generator().Mul(s.Private) }
