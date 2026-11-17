package wire_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/kapetan-io/tiger/assert"
)

const (
	rlpListLong         = 0xf7
	rlpListShort        = 0xc0
	mainnetIdentity2042 = "8627d33753b0d90a8eae75363d6b393ab9204076545b3af6caf831903e4bedde4374f1106551b481fb5ea35e59d51c92165c963881c718216e3e4570a307c08885af55c274f19488f709afe021ca3d9e567138ab097860d9146e895407bea182"
)

func blockHash(block []byte) []byte {
	assert.Ok(block[0] >= rlpListShort, "block is a list")
	skip := 1
	if block[0] > rlpListLong {
		skip += int(block[0] - rlpListLong)
	}
	n, err := wire.BlockLen(block[skip:])
	assert.NoError(err, "header length")
	h := keccak.Sum256(block[skip : skip+n])
	return h[:]
}

type finalizedBlock struct {
	Height       uint64
	Epoch        uint64
	View         uint64
	Digest       string
	Finalization string
	Block        string
}

func TestMainnetFinalizedBlockResponse(_ *testing.T) {
	raw, err := os.ReadFile("testdata/mainnet_finalized_blocks.json")
	assert.NoError(err, "read fixture")
	var fixtures []finalizedBlock
	assert.NoError(json.Unmarshal(raw, &fixtures), "parse fixture")
	assert.Equal(len(fixtures), 2, "fixture count")
	ns := consensus.NewNamespace(consensus.TempoNamespace)
	id, err := consensus.DecodeIdentity(unhex(mainnetIdentity2042))
	assert.NoError(err, "identity")
	for _, fx := range fixtures {
		block := unhex(fx.Block)
		assert.Equal(hex.EncodeToString(blockHash(block)), fx.Digest, "mainnet block hash")
		data := append(unhex(fx.Finalization), block...)
		frame := wire.MarshalResponse{ID: fx.Height, Data: data}.Encode()
		f, err := wire.DecodeFrame(frame)
		assert.NoError(err, "frame")
		got, err := wire.DecodeCertifiedBlock(f.Body, consensus.KindFinalize)
		assert.NoError(err, "finalized response")
		c := got.Certified
		assert.Equal(c.Proposal.Round, consensus.Round{Epoch: fx.Epoch, View: fx.View}, "round")
		assert.Equal(hex.EncodeToString(c.Proposal.Payload[:]), fx.Digest, "payload")
		assert.Equal(hex.EncodeToString(got.Block), fx.Block, "block")
		assert.Equal(hex.EncodeToString(got.Encode()), hex.EncodeToString(data), "re-encode")
		cert := wire.EncodeCertificate(fx.Epoch, c.Kind, c.Proposal, c.Certificate)
		back, err := wire.DecodeCertificate(cert)
		assert.NoError(err, "certificate frame")
		assert.Equal(back.Certified, c, "certificate frame round-trips")
		if fx.Epoch < 2042 {
			continue
		}
		fin := consensus.Finalization{Proposal: c.Proposal, Certificate: c.Certificate}
		assert.NoError(consensus.VerifyFinalization(id, ns, fin), "mainnet finalization verifies")
	}
}

func TestDecodeRejects(_ *testing.T) {
	w := load()
	vote := unhex(w.of(wire.ChannelVotes)[0].Bytes)
	_, err := wire.DecodeVote(vote[:len(vote)-1])
	assert.Equal(err, wire.ErrShort, "truncated vote")
	_, err = wire.DecodeVote(append(append([]byte(nil), vote...), 0))
	assert.Equal(err, wire.ErrLeftover, "vote with left over bytes")
	bad := append([]byte(nil), vote...)
	bad[1] = 3
	_, err = wire.DecodeVote(bad)
	assert.Equal(err, wire.ErrTag, "unknown vote tag")
	_, err = wire.DecodeVote([]byte{0x80, 0x00})
	assert.Equal(err, wire.ErrVarint, "non-canonical mux varint")
	_, err = wire.DecodeVotePayload(append([]byte{1, 0, 0, 0x80, 0x80, 0x80, 0x80, 0x10}, make([]byte, 96)...))
	assert.Equal(err, wire.ErrVarint, "signer past u32")
	_, err = wire.DecodeFrame([]byte{0, 0, 0, 0, 0, 0, 0, 1, 3})
	assert.Equal(err, wire.ErrTag, "unknown frame tag")
	_, err = wire.DecodeFrame([]byte{0, 0, 0, 0, 0, 0, 0, 1, 1, 5, 0})
	assert.Equal(err, wire.ErrShort, "response shorter than its length")
	_, err = wire.DecodeMarshalKey([]byte{3})
	assert.Equal(err, wire.ErrTag, "unknown marshal key")
	_, err = wire.DecodeBlock([]byte{0x80})
	assert.Equal(err, wire.ErrBlock, "block is not a list")
	_, err = wire.DecodeBlock([]byte{0xc2, 0x01})
	assert.Equal(err, wire.ErrShort, "block shorter than its header")
	_, err = wire.DecodeBlock([]byte{0xf8, 0x01, 0x00})
	assert.Equal(err, wire.ErrBlock, "non-canonical long list")
	_, err = wire.DecodeCertifiedBlock(nil, consensus.KindNullify)
	assert.Equal(err, wire.ErrTag, "a nullification carries no block")
}
