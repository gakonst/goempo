package dkg_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

type rustMessage struct {
	Epoch       uint64
	Dealer      int
	Player      int
	DealerBytes string `json:"dealer_bytes"`
	AckBytes    string `json:"ack_bytes"`
	SignedLog   string `json:"signed_log"`
}

type rustRound struct {
	Epoch    uint64
	Previous bool
	Messages []rustMessage
}

type rustMessages struct {
	Namespace    string
	Participants []string
	Private      []string `json:"ed25519_private"`
	Output       string
	Rounds       []rustRound
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "hex")
	return b
}

func loadRust() rustMessages {
	raw, err := os.ReadFile("testdata/rust_messages.json")
	assert.NoError(err, "read rust messages")
	var m rustMessages
	assert.NoError(json.Unmarshal(raw, &m), "parse rust messages")
	return m
}

func TestRustDealerAndAckMessages(_ *testing.T) {
	m := loadRust()
	keys := make([]ed25519.PrivateKey, len(m.Private))
	players := make(dkg.KeySet, len(m.Private))
	for i, k := range m.Private {
		keys[i] = ed25519.NewKeyFromSeed(mustHex(k))
		players[i] = dkg.KeyOf(keys[i])
		assert.Equal(hex.EncodeToString(players[i][:]), m.Participants[i], "participant order")
	}
	output, err := dkg.DecodeOutput(mustHex(m.Output))
	assert.NoError(err, "decode output")
	assert.Equal(hex.EncodeToString(output.Encode()), m.Output, "output re-encodes")
	assert.Equal(len(m.Rounds), 2, "fresh and reshare rounds")
	checked := 0
	for _, round := range m.Rounds {
		cfg := dkg.RoundConfig{Round: round.Epoch, Reveal: dkg.RevealV1, Dealers: players, Players: players}
		if round.Previous {
			cfg.Previous = &output
		}
		info, err := dkg.NewInfo([]byte(m.Namespace), cfg)
		assert.NoError(err, "info")
		for _, msg := range round.Messages {
			if msg.SignedLog != "" {
				log, err := dkg.DecodeSignedDealerLog(mustHex(msg.SignedLog))
				assert.NoError(err, "signed log")
				assert.Ok(info.CheckSignedDealerLog(log), "Rust dealer log verifies in Go")
				assert.Equal(hex.EncodeToString(log.Encode()), msg.SignedLog, "signed log re-encodes")
				continue
			}
			checkPair(info, players, keys, msg)
			checked++
		}
	}
	assert.Equal(checked, 32, "2 rounds x 4 dealers x 4 players")
}

func checkPair(info *dkg.Info, players dkg.KeySet, keys []ed25519.PrivateKey, msg rustMessage) {
	dealt, err := dkg.DecodeEpochMessage(mustHex(msg.DealerBytes), len(players))
	assert.NoError(err, "dealer message")
	assert.Equal(dealt.Epoch, msg.Epoch, "dealer epoch")
	assert.Equal(dealt.Message.Kind, dkg.MessageDealer, "dealer kind")
	assert.Equal(hex.EncodeToString(dealt.Message.EncodeEpoch(dealt.Epoch)), msg.DealerBytes, "dealer re-encodes")
	acked, err := dkg.DecodeEpochMessage(mustHex(msg.AckBytes), len(players))
	assert.NoError(err, "ack message")
	assert.Equal(acked.Message.Kind, dkg.MessageAck, "ack kind")
	assert.Equal(hex.EncodeToString(acked.Message.EncodeEpoch(acked.Epoch)), msg.AckBytes, "ack re-encodes")
	player, err := dkg.NewPlayer(info, keys[msg.Player])
	assert.NoError(err, "player")
	sig, err := player.Receive(players[msg.Dealer], dealt.Message.Commitment, dealt.Message.Share)
	assert.NoError(err, "Go player accepts the Rust dealing")
	assert.Equal(sig, acked.Message.Ack, "Go ack equals the Rust ack")
	summary := info.AckSummary(players[msg.Dealer], dealt.Message.Commitment)
	assert.Ok(dkg.VerifySummary(players[msg.Player], summary, acked.Message.Ack), "Rust ack verifies in Go")
}

func TestDecodeMessageRejects(_ *testing.T) {
	m := loadRust()
	raw := mustHex(m.Rounds[0].Messages[0].DealerBytes)
	_, err := dkg.DecodeEpochMessage(raw[:len(raw)-1], 4)
	assert.Equal(err, dkg.ErrShort, "truncated dealer")
	_, err = dkg.DecodeEpochMessage(append(append([]byte(nil), raw...), 0), 4)
	assert.Equal(err, dkg.ErrLeftover, "left over")
	_, err = dkg.DecodeEpochMessage(raw, 2)
	assert.Equal(err, dkg.ErrRange, "commitment longer than the player set")
	_, err = dkg.DecodeMessage([]byte{2}, 4)
	assert.Equal(err, dkg.ErrMalformed, "unknown tag")
}
