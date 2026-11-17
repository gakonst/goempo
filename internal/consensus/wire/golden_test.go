package wire_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/consensus/wire"
	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

type vector struct {
	Name    string
	Channel uint64
	Bytes   string
	Epoch   uint64
	View    uint64
	Parent  uint64
	Payload string
	Kind    string
	Signer  int
	ID      uint64
	Key     string
	Height  uint64
	Digest  string
	Cert    string
}

type golden struct {
	Namespace string
	Output    string
	Shares    []string
	Vectors   []vector
}

type world struct {
	g        golden
	ns       consensus.Namespace
	output   dkg.Output
	shares   []dkg.Share
	identity consensus.Identity
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "hex")
	return b
}

func load() world {
	raw, err := os.ReadFile("testdata/rust_golden.json")
	assert.NoError(err, "read golden")
	var w world
	assert.NoError(json.Unmarshal(raw, &w.g), "parse golden")
	w.ns = consensus.NewNamespace(w.g.Namespace)
	w.output, err = dkg.DecodeOutput(unhex(w.g.Output))
	assert.NoError(err, "decode output")
	for _, s := range w.g.Shares {
		share, err := dkg.DecodeShare(unhex(s))
		assert.NoError(err, "decode share")
		assert.Equal(hex.EncodeToString(share.Encode()), s, "share re-encodes")
		w.shares = append(w.shares, share)
	}
	w.identity = consensus.NewIdentity(w.output.Public.Poly.Constant())
	return w
}

func (w world) of(channel uint64) []vector {
	var out []vector
	for _, v := range w.g.Vectors {
		if v.Channel == channel {
			out = append(out, v)
		}
	}
	assert.Ok(len(out) > 0, "golden has vectors for the channel")
	return out
}

var kinds = map[string]consensus.VoteKind{
	"notarize": consensus.KindNotarize, "nullify": consensus.KindNullify, "finalize": consensus.KindFinalize,
	"notarization": consensus.KindNotarize, "nullification": consensus.KindNullify, "finalization": consensus.KindFinalize,
}

func proposalOf(v vector) consensus.Proposal {
	p := consensus.Proposal{Round: consensus.Round{Epoch: v.Epoch, View: v.View}}
	if kinds[v.Kind] == consensus.KindNullify {
		return p
	}
	p.Parent = v.Parent
	p.Payload = consensus.Digest(unhex(v.Payload))
	return p
}

func TestRustVotesDecodeReencodeAndVerify(_ *testing.T) {
	w := load()
	vectors := w.of(wire.ChannelVotes)
	assert.Equal(len(vectors), 36, "3 rounds x 4 signers x 3 kinds")
	for _, v := range vectors {
		raw := unhex(v.Bytes)
		got, err := wire.DecodeVote(raw)
		assert.NoError(err, v.Name)
		assert.Equal(got.Epoch, v.Epoch, v.Name+" mux epoch")
		assert.Equal(got.Vote.Kind, kinds[v.Kind], v.Name+" kind")
		assert.Equal(got.Vote.Signer, v.Signer, v.Name+" signer")
		assert.Equal(got.Vote.Proposal, proposalOf(v), v.Name+" proposal")
		assert.Equal(hex.EncodeToString(wire.EncodeVote(got.Epoch, got.Vote)), v.Bytes, v.Name+" re-encode")
		public := w.output.Public.Poly.Eval(v.Signer)
		assert.NoError(consensus.VerifyVote(public, w.ns, got.Vote), v.Name+" Rust vote verifies in Go")
		mine := consensus.NewSigner(w.shares[v.Signer], w.ns).Sign(kinds[v.Kind], proposalOf(v))
		assert.Equal(hex.EncodeToString(wire.EncodeVote(v.Epoch, mine)), v.Bytes, v.Name+" Go signs the same bytes")
	}
}

var quorums = map[consensus.VoteKind][]int{
	consensus.KindNotarize: {0, 1, 3},
	consensus.KindNullify:  {1, 2, 3},
	consensus.KindFinalize: {0, 2, 3},
}

func (w world) verify(c wire.Certified) error {
	switch c.Kind {
	case consensus.KindNotarize:
		return consensus.VerifyNotarization(w.identity, w.ns, c.Proposal, c.Certificate)
	case consensus.KindNullify:
		return consensus.VerifyNullification(w.identity, w.ns, c.Proposal.Round, c.Certificate)
	case consensus.KindFinalize:
		return consensus.VerifyFinalization(w.identity, w.ns, consensus.Finalization{Proposal: c.Proposal, Certificate: c.Certificate})
	default:
		assert.Unreachable("VoteKind: unhandled value")
	}
	return nil
}

func (w world) assemble(kind consensus.VoteKind, p consensus.Proposal) consensus.Certificate {
	var votes []consensus.Vote
	for _, i := range quorums[kind] {
		votes = append(votes, consensus.NewSigner(w.shares[i], w.ns).Sign(kind, p))
	}
	c, err := consensus.Assemble(votes)
	assert.NoError(err, "assemble")
	return c
}

func TestRustCertificatesDecodeReencodeAndVerify(_ *testing.T) {
	w := load()
	vectors := w.of(wire.ChannelCertificates)
	assert.Equal(len(vectors), 9, "3 rounds x 3 kinds")
	for _, v := range vectors {
		got, err := wire.DecodeCertificate(unhex(v.Bytes))
		assert.NoError(err, v.Name)
		c := got.Certified
		assert.Equal(got.Epoch, v.Epoch, v.Name+" mux epoch")
		assert.Equal(c.Kind, kinds[v.Kind], v.Name+" kind")
		assert.Equal(c.Proposal, proposalOf(v), v.Name+" proposal")
		assert.Equal(hex.EncodeToString(wire.EncodeCertificate(got.Epoch, c.Kind, c.Proposal, c.Certificate)), v.Bytes, v.Name+" re-encode")
		assert.NoError(w.verify(c), v.Name+" Rust certificate verifies in Go")
		mine := w.assemble(c.Kind, c.Proposal)
		assert.Equal(mine, c.Certificate, v.Name+" Go recovers the same certificate")
		bad := c
		bad.Proposal.Round.View++
		assert.Ok(w.verify(bad) != nil, v.Name+" round binds")
	}
}

func TestRustResolverMessages(_ *testing.T) {
	w := load()
	for _, v := range w.of(wire.ChannelResolver) {
		got, err := wire.DecodeResolver(unhex(v.Bytes))
		assert.NoError(err, v.Name)
		assert.Equal(got.Epoch, v.Epoch, v.Name+" epoch")
		assert.Equal(got.Frame.ID, v.ID, v.Name+" id")
		switch v.Kind {
		case "request":
			view, err := got.Frame.View()
			assert.NoError(err, v.Name)
			assert.Equal(view, v.View, v.Name+" view")
			again := wire.ResolverRequest{Epoch: v.Epoch, ID: v.ID, View: view}.Encode()
			assert.Equal(hex.EncodeToString(again), v.Bytes, v.Name+" re-encode")
		case "response":
			c, err := got.Frame.Certified()
			assert.NoError(err, v.Name)
			assert.Equal(c.Kind, kinds[v.Cert], v.Name+" certificate kind")
			assert.Equal(c.Proposal.Round.View, v.View, v.Name+" view")
			assert.NoError(w.verify(c), v.Name+" verifies")
			again := wire.ResolverResponse{Epoch: v.Epoch, ID: v.ID, Certified: c}.Encode()
			assert.Equal(hex.EncodeToString(again), v.Bytes, v.Name+" re-encode")
		case "error":
			assert.Equal(got.Frame.Kind, wire.FrameError, v.Name+" kind")
			assert.Equal(hex.EncodeToString(wire.ResolverError{Epoch: v.Epoch, ID: v.ID}.Encode()), v.Bytes, v.Name+" re-encode")
		default:
			assert.Unreachable("resolver vector kind")
		}
	}
}

func TestRustBroadcastAndMarshal(_ *testing.T) {
	w := load()
	broadcast := w.of(wire.ChannelBroadcast)
	assert.Equal(len(broadcast), 1, "one block")
	block, err := wire.DecodeBlock(unhex(broadcast[0].Bytes))
	assert.NoError(err, "decode block")
	assert.Equal(hex.EncodeToString(wire.EncodeBlock(block)), broadcast[0].Bytes, "block re-encodes")
	assert.Equal(hex.EncodeToString(blockHash(block)), broadcast[0].Digest, "block digest")
	for _, v := range w.of(wire.ChannelMarshal) {
		f, err := wire.DecodeFrame(unhex(v.Bytes))
		assert.NoError(err, v.Name)
		assert.Equal(f.ID, v.ID, v.Name+" id")
		switch v.Kind {
		case "request":
			k, err := f.MarshalKey()
			assert.NoError(err, v.Name)
			checkKey(v, k)
			assert.Equal(hex.EncodeToString(wire.MarshalRequest{ID: v.ID, Key: k}.Encode()), v.Bytes, v.Name+" re-encode")
		case "response":
			checkResponse(w, v, f, block)
			assert.Equal(hex.EncodeToString(wire.MarshalResponse{ID: v.ID, Data: f.Body}.Encode()), v.Bytes, v.Name+" re-encode")
		case "error":
			assert.Equal(f.Kind, wire.FrameError, v.Name+" kind")
			assert.Equal(hex.EncodeToString(wire.MarshalError{ID: v.ID}.Encode()), v.Bytes, v.Name+" re-encode")
		default:
			assert.Unreachable("marshal vector kind")
		}
	}
}

func checkKey(v vector, k wire.MarshalKey) {
	switch v.Key {
	case "block":
		assert.Equal(k.Kind, wire.KeyBlock, v.Name)
		assert.Equal(hex.EncodeToString(k.Digest[:]), v.Digest, v.Name+" digest")
	case "finalized":
		assert.Equal(k.Kind, wire.KeyFinalized, v.Name)
		assert.Equal(k.Height, v.Height, v.Name+" height")
	case "notarized":
		assert.Equal(k.Kind, wire.KeyNotarized, v.Name)
		assert.Equal(k.Round, consensus.Round{Epoch: v.Epoch, View: v.View}, v.Name+" round")
	default:
		assert.Unreachable("marshal key kind")
	}
}

func checkResponse(w world, v vector, f wire.Frame, block []byte) {
	if v.Key == "block" {
		got, err := wire.DecodeBlock(f.Body)
		assert.NoError(err, v.Name)
		assert.Equal(hex.EncodeToString(got), hex.EncodeToString(block), v.Name+" block")
		return
	}
	kind := consensus.KindFinalize
	if v.Key == "notarized" {
		kind = consensus.KindNotarize
	}
	got, err := wire.DecodeCertifiedBlock(f.Body, kind)
	assert.NoError(err, v.Name)
	assert.Equal(hex.EncodeToString(got.Block), hex.EncodeToString(block), v.Name+" block")
	assert.Equal(got.Certified.Proposal.Payload, consensus.Digest(blockHash(block)), v.Name+" payload is the block hash")
	assert.NoError(w.verify(got.Certified), v.Name+" verifies")
	assert.Equal(hex.EncodeToString(got.Encode()), hex.EncodeToString(f.Body), v.Name+" re-encode")
}

func TestGoVotesForNewRoundsVerify(_ *testing.T) {
	w := load()
	p := consensus.Proposal{Round: consensus.Round{Epoch: 9, View: 1 << 40}, Parent: 1<<40 - 1}
	p.Payload[0] = 0x42
	for i, share := range w.shares {
		v := consensus.NewSigner(share, w.ns).Sign(consensus.KindNotarize, p)
		got, err := wire.DecodeVote(wire.EncodeVote(9, v))
		assert.NoError(err, "decode own vote")
		assert.Equal(got.Vote, v, "own vote round-trips")
		assert.NoError(consensus.VerifyVote(w.output.Public.Poly.Eval(i), w.ns, got.Vote), "own vote verifies")
	}
	recovered := w.assemble(consensus.KindFinalize, p)
	_, err := bls12381.DecodeG1(recovered.Vote[:])
	assert.NoError(err, "recovered signature decodes")
}
