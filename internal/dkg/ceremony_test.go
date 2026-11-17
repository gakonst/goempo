package dkg_test

import (
	"crypto/ed25519"
	"fmt"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

func TestMainnetRoundSummary(_ *testing.T) {
	a, err := dkg.DecodeOutcome(readHex("mainnet_block_44085599_extradata.hex"))
	assert.NoError(err, "decode 2041")
	b, err := dkg.DecodeOutcome(readHex("mainnet_block_44107199_extradata.hex"))
	assert.NoError(err, "decode 2042")
	assert.Ok(a.NextFullDKG, "epoch 2042 is a full dkg")
	info, err := dkg.NewInfo([]byte(dkg.TempoNamespace), dkg.RoundConfig{
		Round: a.Epoch, Reveal: dkg.RevealV1, Dealers: a.Output.Players, Players: a.NextPlayers,
	})
	assert.NoError(err, "info")
	assert.Equal(info.Summary, b.Output.Summary, "the round run in epoch 2041 reproduces 2042's summary")
}

type rng struct{ n *dkg.Noise }

func (r rng) Read(p []byte) (int, error) { return r.n.Read(p) }

func seeded(label string) rng {
	t := dkg.NewTranscript([]byte("test"))
	return rng{n: t.Noise([]byte(label))}
}

func keys(n int, label string) []ed25519.PrivateKey {
	out := make([]ed25519.PrivateKey, n)
	for i := range out {
		seed := make([]byte, ed25519.SeedSize)
		_, err := seeded(fmt.Sprintf("%s-%d", label, i)).Read(seed)
		assert.NoError(err, "seed")
		out[i] = ed25519.NewKeyFromSeed(seed)
	}
	return out
}

func pubs(ks []ed25519.PrivateKey) dkg.KeySet {
	out := make([]dkg.PublicKey, len(ks))
	for i, k := range ks {
		out[i] = dkg.KeyOf(k)
	}
	return dkg.NewKeySet(out)
}

type ceremony struct {
	info    *dkg.Info
	dealers []ed25519.PrivateKey
	players []ed25519.PrivateKey
	shares  map[dkg.PublicKey]dkg.Share
	offline map[int]bool
}

type result struct {
	out    dkg.Output
	byKey  map[dkg.PublicKey]dkg.Share
	shares []dkg.Share
}

func (c ceremony) run() result {
	ps := make([]*dkg.Player, len(c.players))
	for i, k := range c.players {
		p, err := dkg.NewPlayer(c.info, k)
		assert.NoError(err, "player")
		ps[i] = p
	}
	var logs []dkg.DealerEntry
	for _, k := range c.dealers {
		cfg := dkg.DealerConfig{Key: k, Rand: seeded("dealer" + dkg.KeyOf(k).String())}
		if c.info.Previous != nil {
			prev := c.shares[dkg.KeyOf(k)]
			cfg.Previous = &prev
		}
		d, err := dkg.NewDealer(c.info, cfg)
		assert.NoError(err, "dealer")
		for i, p := range ps {
			if c.offline[i] {
				continue
			}
			sig, err := p.Receive(dkg.KeyOf(k), d.Commitment(), d.ShareFor(p.Index()))
			assert.NoError(err, "receive")
			assert.NoError(d.Ack(dkg.KeyOf(c.players[i]), sig), "ack")
		}
		signed := d.Finalize()
		decoded, err := dkg.DecodeSignedDealerLog(signed.Encode())
		assert.NoError(err, "log round trip")
		assert.Ok(c.info.CheckSignedDealerLog(decoded), "log signature")
		logs = append(logs, dkg.DealerEntry{Dealer: decoded.Dealer, Log: decoded.Log})
	}
	sortEntries(logs)
	sel, err := c.info.Select(logs)
	assert.NoError(err, "select")
	out, err := c.info.Reckon(logs, sel)
	assert.NoError(err, "reckon")
	res := result{out: out, byKey: map[dkg.PublicKey]dkg.Share{}}
	for i, p := range ps {
		s, err := p.Finalize(logs, sel)
		assert.NoError(err, "player share, from reveals when offline")
		assert.Ok(s.Public().Equal(out.Public.Poly.Eval(s.Index)), "share matches the public polynomial")
		res.byKey[dkg.KeyOf(c.players[i])] = s
		res.shares = append(res.shares, s)
	}
	return res
}

func sortEntries(e []dkg.DealerEntry) {
	set := make([]dkg.PublicKey, len(e))
	for i := range e {
		set[i] = e[i].Dealer
	}
	sorted := dkg.NewKeySet(set)
	out := make([]dkg.DealerEntry, len(e))
	for _, x := range e {
		i, _ := sorted.Index(x.Dealer)
		out[i] = x
	}
	copy(e, out)
}

func thresholdSign(r result, msg []byte) bls12381.G1 {
	out := r.out
	dst := bls12381.DST("BLS_SIG_BLS12381G1_XMD:SHA-256_SSWU_RO_POP_")
	var partials []dkg.Partial
	for _, s := range r.shares {
		if len(partials) == len(out.Public.Poly) {
			break
		}
		sig := bls12381.SignMinSig(s.Private, dst, msg)
		assert.Ok(bls12381.VerifyMinSig(out.Public.Poly.Eval(s.Index), dst, msg, sig), "partial verifies")
		partials = append(partials, dkg.Partial{Index: s.Index, Signature: sig})
	}
	sig, err := dkg.RecoverG1(partials)
	assert.NoError(err, "recover")
	assert.Ok(bls12381.VerifyMinSig(out.Public.Poly.Constant(), dst, msg, sig), "threshold signature verifies")
	return sig
}

func TestFullDKGThenReshare(_ *testing.T) {
	ns := []byte(dkg.TempoNamespace)
	vals := keys(4, "v")
	info, err := dkg.NewInfo(ns, dkg.RoundConfig{Round: 1, Dealers: pubs(vals), Players: pubs(vals)})
	assert.NoError(err, "info 1")
	r1 := ceremony{info: info, dealers: vals, players: vals}.run()
	out1 := r1.out
	assert.Equal(len(out1.Public.Poly), 3, "quorum of four is three")
	thresholdSign(r1, []byte("epoch 1"))

	next := append(append([]ed25519.PrivateKey{}, vals...), keys(3, "w")...)
	info2, err := dkg.NewInfo(ns, dkg.RoundConfig{Round: 2, Previous: &out1, Reveal: dkg.RevealV1, Dealers: pubs(vals), Players: pubs(next)})
	assert.NoError(err, "info 2")
	r2 := ceremony{info: info2, dealers: vals, players: next, shares: r1.byKey, offline: map[int]bool{6: true}}.run()
	out2 := r2.out
	assert.Ok(out2.Public.Poly.Constant().Equal(out1.Public.Poly.Constant()), "reshare keeps the identity")
	assert.Equal(len(out2.Public.Poly), 5, "quorum of seven is five")
	assert.Equal(len(out2.Revealed), 1, "the offline player's share is revealed")
	thresholdSign(r2, []byte("epoch 2"))
	o := dkg.Outcome{Epoch: 2, Output: out2, NextPlayers: pubs(next)}
	back, err := dkg.DecodeOutcome(o.Encode())
	assert.NoError(err, "outcome round trip")
	assert.Ok(back.Output.Public.Poly.Equal(out2.Public.Poly), "outcome keeps the polynomial")
}
