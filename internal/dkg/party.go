package dkg

import (
	"crypto/ed25519"
	"io"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
)

const scalarWideLen = 64

func KeyOf(key ed25519.PrivateKey) PublicKey {
	return PublicKey(key.Public().(ed25519.PublicKey))
}

func randomScalar(rng io.Reader) (bls12381.Scalar, error) {
	var wide [scalarWideLen]byte
	if _, err := io.ReadFull(rng, wide[:]); err != nil {
		return bls12381.Scalar{}, err
	}
	return bls12381.ScalarFromWide(wide[:]), nil
}

type Dealer struct {
	info       *Info
	key        ed25519.PrivateKey
	me         PublicKey
	poly       Poly
	commitment Commitment
	acks       map[int][SignatureLen]byte
}

type DealerConfig struct {
	Key      ed25519.PrivateKey
	Previous *Share
	Rand     io.Reader
}

func NewDealer(info *Info, cfg DealerConfig) (*Dealer, error) {
	me := KeyOf(cfg.Key)
	if _, ok := info.Dealers.Index(me); !ok {
		return nil, ErrUnknownDealer
	}
	if (info.Previous != nil) != (cfg.Previous != nil) {
		return nil, ErrPreviousShare
	}
	p := make(Poly, info.Degree()+1)
	for i := range p {
		k, err := randomScalar(cfg.Rand)
		if err != nil {
			return nil, err
		}
		p[i] = k
	}
	if cfg.Previous != nil {
		p[0] = cfg.Previous.Private
	}
	d := &Dealer{info: info, key: cfg.Key, me: me, poly: p, commitment: p.Commit(), acks: map[int][SignatureLen]byte{}}
	if !info.checkCommitment(me, d.commitment) {
		return nil, ErrCommitment
	}
	return d, nil
}

func (d *Dealer) Commitment() Commitment { return d.commitment }

func (d *Dealer) ShareFor(player int) bls12381.Scalar { return d.poly.Eval(player) }

func (d *Dealer) Ack(player PublicKey, sig [SignatureLen]byte) error {
	index, ok := d.info.Players.Index(player)
	if !ok {
		return ErrUnknownPlayer
	}
	if !VerifySummary(player, d.info.AckSummary(d.me, d.commitment), sig) {
		return ErrAck
	}
	d.acks[index] = sig
	return nil
}

func (d *Dealer) Finalize() SignedDealerLog {
	log := DealerLog{Commitment: d.commitment}
	res := &DealerResults{Players: d.info.Players, Values: make([]AckOrReveal, len(d.info.Players))}
	reveals := 0
	for i := range res.Values {
		if sig, ok := d.acks[i]; ok {
			res.Values[i] = AckOrReveal{Ack: sig}
			continue
		}
		reveals++
		res.Values[i] = AckOrReveal{Reveal: true, Share: d.poly.Eval(i)}
	}
	if reveals <= d.info.MaxReveals() {
		log.Results = res
	}
	return SignedDealerLog{Dealer: d.me, Log: log, Signature: SignSummary(d.key, d.info.LogSummary(log))}
}

type dealt struct {
	commitment Commitment
	share      bls12381.Scalar
}

type Player struct {
	info     *Info
	key      ed25519.PrivateKey
	index    int
	dealings map[PublicKey]dealt
}

func NewPlayer(info *Info, key ed25519.PrivateKey) (*Player, error) {
	index, ok := info.Players.Index(KeyOf(key))
	if !ok {
		return nil, ErrUnknownPlayer
	}
	return &Player{info: info, key: key, index: index, dealings: map[PublicKey]dealt{}}, nil
}

func (p *Player) Index() int { return p.index }

func (p *Player) Receive(dealer PublicKey, c Commitment, share bls12381.Scalar) ([SignatureLen]byte, error) {
	if _, ok := p.info.Dealers.Index(dealer); !ok {
		return [SignatureLen]byte{}, ErrUnknownDealer
	}
	if !p.info.checkCommitment(dealer, c) {
		return [SignatureLen]byte{}, ErrCommitment
	}
	if !bls12381.G2Generator().Mul(share).Equal(c.Eval(p.index)) {
		return [SignatureLen]byte{}, ErrShare
	}
	p.dealings[dealer] = dealt{commitment: c, share: share}
	return SignSummary(p.key, p.info.AckSummary(dealer, c)), nil
}

func (p *Player) Finalize(logs []DealerEntry, selection []int) (Share, error) {
	weights, err := p.info.combineWeights(logs, selection)
	if err != nil {
		return Share{}, err
	}
	var private bls12381.Scalar
	for k, i := range selection {
		v := logs[i].Log.Results.Values[p.index]
		share := v.Share
		if !v.Reveal {
			got, ok := p.dealings[logs[i].Dealer]
			if !ok {
				return Share{}, ErrNoShare
			}
			if !got.commitment.Equal(logs[i].Log.Commitment) {
				return Share{}, ErrDealingChanged
			}
			share = got.share
		}
		private = private.Add(share.Mul(weights[k]))
	}
	return Share{Index: p.index, Private: private}, nil
}
