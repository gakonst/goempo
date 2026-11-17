package consensus_test

import (
	"errors"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/kapetan-io/tiger/assert"
)

func extraData(height string) []byte {
	raw, err := os.ReadFile("testdata/mainnet_block_" + height + "_extradata.hex")
	assert.NoError(err, "read extra data")
	return unhex(string(raw))
}

func certified(fx fixture) consensus.Certified {
	var hash consensus.Digest
	copy(hash[:], unhex(fx.Digest))
	return consensus.Certified{Height: fx.Height, Hash: hash, Certificate: unhex(fx.Certificate)}
}

func fixtureAt(height uint64) fixture {
	for _, fx := range loadFixtures() {
		if fx.Height == height {
			return fx
		}
	}
	assert.Unreachable("no fixture at that height")
	return fixture{}
}

func TestEpocher(t *testing.T) {
	e := consensus.Epocher{Length: consensus.MainnetEpochLength}
	assert.Equal(e.Epoch(42098400), uint64(1949), "first block of epoch 1949")
	assert.Equal(e.Epoch(42098399), uint64(1948), "boundary block")
	assert.Ok(e.IsLast(42098399), "42098399 is last of 1948")
	assert.Ok(!e.IsLast(42098400), "42098400 is not last")
	assert.Equal(e.Last(2041), uint64(44107199), "last of 2041")
	assert.Equal(e.First(2042), uint64(44107200), "first of 2042")
}

func TestChainAcrossBoundaries(t *testing.T) {
	c := consensus.NewMainnetChain()
	early := []consensus.Certified{certified(fixtureAt(42098400)), certified(fixtureAt(42100000))}
	var he *consensus.HeightError
	err := c.Verify(early)
	assert.Ok(errors.As(err, &he), "height error")
	assert.Ok(errors.Is(err, consensus.ErrNoIdentity), "epoch 1949 needs its boundary")
	next, err := c.RegisterBoundary(42098399, extraData("42098399"))
	assert.NoError(err, "register 1949")
	assert.Equal(next, uint64(1949), "outcome epoch")
	assert.NoError(c.Verify(early), "epoch 1949 batch verifies")

	_, err = c.RegisterBoundary(44085599, extraData("44085599"))
	assert.NoError(err, "register 2041")
	assert.NoError(c.Verify([]consensus.Certified{certified(fixtureAt(44107199))}), "last block of 2041")
	next, err = c.RegisterBoundary(44107199, extraData("44107199"))
	assert.NoError(err, "the 2041 boundary installs the network identity")
	assert.Equal(next, uint64(2042), "epoch 2042")
	id, _ := c.Identity(2042)
	assert.Equal(id.Bytes(), c.Network().Identity.Bytes(), "outcome identity is the chainspec network identity")

	fresh := consensus.NewMainnetChain()
	mixed := []consensus.Certified{certified(fixtureAt(44107200))}
	assert.NoError(fresh.Verify(mixed), "network identity covers epoch 2042 without a boundary")
}

func TestChainRejects(t *testing.T) {
	c := consensus.NewMainnetChain()
	_, err := c.RegisterBoundary(42098399, extraData("42098399"))
	assert.NoError(err, "register")
	good := certified(fixtureAt(42100000))

	wrongHash := good
	wrongHash.Hash[0] ^= 1
	assert.Ok(errors.Is(c.Verify([]consensus.Certified{wrongHash}), consensus.ErrDigest), "digest must be the block hash")

	wrongHeight := good
	wrongHeight.Height = 44107200
	assert.Ok(errors.Is(c.Verify([]consensus.Certified{wrongHeight}), consensus.ErrEpoch), "epoch must be the block's")

	ctx := consensus.Proposal{Round: consensus.Round{Epoch: 1949, View: 1601}, Parent: 1600}
	withContext := good
	withContext.Context = &ctx
	assert.NoError(c.Verify([]consensus.Certified{withContext}), "matching consensus context")
	otherView := ctx
	otherView.Round.View++
	withContext.Context = &otherView
	assert.Ok(errors.Is(c.Verify([]consensus.Certified{withContext}), consensus.ErrContext), "view must match the header")

	tampered := certified(fixtureAt(42098400))
	tampered.Certificate = append([]byte(nil), tampered.Certificate...)
	vote := len(tampered.Certificate) - 2*consensus.SignatureBytes
	tampered.Certificate[vote], tampered.Certificate[len(tampered.Certificate)-consensus.SignatureBytes] =
		tampered.Certificate[len(tampered.Certificate)-consensus.SignatureBytes], tampered.Certificate[vote]
	err = c.Verify([]consensus.Certified{good, tampered})
	var he *consensus.HeightError
	assert.Ok(errors.As(err, &he), "batch failure is a height error")
	assert.Equal(he.Height, uint64(42098400), "batch failure names the bad block")

	_, err = c.RegisterBoundary(42098400, extraData("42098399"))
	assert.Ok(errors.Is(err, consensus.ErrBoundary), "only an epoch's last block carries the outcome")
	_, err = c.RegisterBoundary(44107199+consensus.MainnetEpochLength, extraData("44107199"))
	assert.Ok(errors.Is(err, consensus.ErrOutcomeEpoch), "outcome epoch must be the next epoch")
	_, err = c.RegisterBoundary(42098399, extraData("42098399")[:100])
	assert.Ok(err != nil, "truncated outcome fails")
}

func TestChainNetworkIdentityMismatch(t *testing.T) {
	network := consensus.MainnetNetworkIdentity()
	network.FromEpoch = 1949
	c := consensus.NewChain(network, consensus.Epocher{Length: consensus.MainnetEpochLength})
	_, err := c.RegisterBoundary(42098399, extraData("42098399"))
	assert.Ok(errors.Is(err, consensus.ErrNetworkIdentity), "an outcome that disagrees with the network identity fails")
}

func BenchmarkChainVerifyBatch(b *testing.B) {
	c := consensus.NewMainnetChain()
	fx := certified(fixtureAt(44107200))
	items := make([]consensus.Certified, 256)
	for i := range items {
		items[i] = fx
	}
	for i := 0; i < b.N; i++ {
		assert.NoError(c.Verify(items), "verify")
	}
}

func TestModeratoNetworkIdentity(_ *testing.T) {
	raw, err := os.ReadFile("testdata/moderato_block_37735199_extradata.hex")
	assert.NoError(err, "read moderato extra data")
	c := consensus.NewModeratoChain()
	assert.Equal(c.Epocher().Length, uint64(consensus.ModeratoEpochLength), "moderato epoch length")
	epoch, err := c.RegisterBoundary(37735199, unhex(string(raw)))
	assert.NoError(err, "moderato epoch 1747 outcome matches the compiled testnet identity")
	assert.Equal(epoch, uint64(consensus.ModeratoIdentityFromEpoch), "outcome installs epoch 1747")
	network := consensus.MainnetNetworkIdentity()
	network.FromEpoch = consensus.ModeratoIdentityFromEpoch
	m := consensus.NewChain(network, consensus.Epocher{Length: consensus.ModeratoEpochLength})
	_, err = m.RegisterBoundary(37735199, unhex(string(raw)))
	assert.Ok(errors.Is(err, consensus.ErrNetworkIdentity), "the mainnet identity does not verify the moderato outcome")
}
