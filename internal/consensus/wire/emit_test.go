package wire_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

type emitted struct {
	Name    string `json:"name"`
	Channel uint64 `json:"channel"`
	Bytes   string `json:"bytes"`
}

type emittedAck struct {
	Epoch       uint64 `json:"epoch"`
	Dealer      int    `json:"dealer"`
	Player      int    `json:"player"`
	DealerBytes string `json:"dealer_bytes"`
	AckBytes    string `json:"ack_bytes"`
}

type emittedLog struct {
	Epoch     uint64 `json:"epoch"`
	Dealer    int    `json:"dealer"`
	SignedLog string `json:"signed_log"`
}

type emission struct {
	Vectors []emitted    `json:"vectors"`
	Acks    []emittedAck `json:"dkg_acks"`
	Logs    []emittedLog `json:"dkg_logs"`
}

const keccakBlock = 32

type keccakReader struct {
	seed    []byte
	counter uint64
	buf     []byte
}

func (r *keccakReader) Read(p []byte) (int, error) {
	blocks := (len(p) + len(r.buf)) / keccakBlock
	for range blocks + 1 {
		if len(r.buf) >= len(p) {
			break
		}
		r.counter++
		h := keccak.Sum256Concat(r.seed, []byte{byte(r.counter), byte(r.counter >> 8)})
		r.buf = append(r.buf, h[:]...)
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

type goKeys struct {
	keys    []ed25519.PrivateKey
	players dkg.KeySet
}

func rustKeys() goKeys {
	raw, err := os.ReadFile("testdata/rust_golden.json")
	assert.NoError(err, "read golden")
	var g struct {
		Private []string `json:"ed25519_private"`
	}
	assert.NoError(json.Unmarshal(raw, &g), "parse golden")
	var k goKeys
	for _, s := range g.Private {
		key := ed25519.NewKeyFromSeed(unhex(s))
		k.keys = append(k.keys, key)
		k.players = append(k.players, dkg.KeyOf(key))
	}
	return k
}

var kindNames = map[consensus.VoteKind]string{consensus.KindNotarize: "notarize", consensus.KindNullify: "nullify", consensus.KindFinalize: "finalize"}

func goVotes(w world) []emitted {
	var out []emitted
	rounds := []consensus.Proposal{
		{Round: consensus.Round{Epoch: 7, View: 1 << 33}, Parent: 1<<33 - 5},
		{Round: consensus.Round{Epoch: 1 << 20, View: 2}, Parent: 1},
	}
	for r, p := range rounds {
		p.Payload = consensus.Digest(keccak.Sum256([]byte{byte(r)}))
		for kind := consensus.KindNotarize; kind <= consensus.KindFinalize; kind++ {
			subject := p
			if kind == consensus.KindNullify {
				subject = consensus.Proposal{Round: p.Round}
			}
			for _, share := range w.shares {
				v := consensus.NewSigner(share, w.ns).Sign(kind, subject)
				out = append(out, emitted{Name: "go/vote/" + kindNames[kind], Channel: wire.ChannelVotes, Bytes: hex.EncodeToString(wire.EncodeVote(p.Round.Epoch, v))})
			}
			c := w.assemble(kind, subject)
			out = append(out, emitted{Name: "go/certificate/" + kindNames[kind], Channel: wire.ChannelCertificates, Bytes: hex.EncodeToString(wire.EncodeCertificate(p.Round.Epoch, kind, subject, c))})
		}
	}
	return out
}

func goDKG(w world, k goKeys) emission {
	var e emission
	previous := w.output
	for _, epoch := range []uint64{11, 12} {
		cfg := dkg.RoundConfig{Round: epoch, Reveal: dkg.RevealV1, Dealers: k.players, Players: k.players}
		if epoch == 12 {
			cfg.Previous = &previous
		}
		info, err := dkg.NewInfo([]byte(w.g.Namespace), cfg)
		assert.NoError(err, "info")
		for d, key := range k.keys {
			dc := dkg.DealerConfig{Key: key, Rand: &keccakReader{seed: []byte{byte(epoch), byte(d)}}}
			if epoch == 12 {
				dc.Previous = &w.shares[d]
			}
			dealer, err := dkg.NewDealer(info, dc)
			assert.NoError(err, "dealer")
			for p, pkey := range k.keys {
				player, err := dkg.NewPlayer(info, pkey)
				assert.NoError(err, "player")
				share := dealer.ShareFor(player.Index())
				sig, err := player.Receive(k.players[d], dealer.Commitment(), share)
				assert.NoError(err, "receive")
				assert.NoError(dealer.Ack(k.players[p], sig), "ack")
				dm := dkg.Message{Kind: dkg.MessageDealer, Commitment: dealer.Commitment(), Share: share}
				am := dkg.Message{Kind: dkg.MessageAck, Ack: sig}
				e.Acks = append(e.Acks, emittedAck{Epoch: epoch, Dealer: d, Player: p, DealerBytes: hex.EncodeToString(dm.EncodeEpoch(epoch)), AckBytes: hex.EncodeToString(am.EncodeEpoch(epoch))})
			}
			e.Logs = append(e.Logs, emittedLog{Epoch: epoch, Dealer: d, SignedLog: hex.EncodeToString(dealer.Finalize().Encode())})
		}
	}
	return e
}

func TestEmitGoVectors(_ *testing.T) {
	path := os.Getenv("GOEMPO_WIRE_EMIT")
	w := load()
	e := goDKG(w, rustKeys())
	e.Vectors = goVotes(w)
	assert.Equal(len(e.Vectors), 2*3*5, "2 rounds x 3 kinds x (4 votes + 1 certificate)")
	assert.Equal(len(e.Acks), 2*4*4, "2 rounds x 4 dealers x 4 players")
	if path == "" {
		return
	}
	raw, err := json.MarshalIndent(e, "", " ")
	assert.NoError(err, "marshal")
	assert.NoError(os.WriteFile(path, raw, 0o600), "write")
}
