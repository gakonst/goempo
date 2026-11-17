package consensus

import (
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/gakonst/goempo/internal/tempo/dkg"
	"github.com/kapetan-io/tiger/assert"
)

const (
	MainnetEpochLength       = 21600
	MainnetIdentityFromEpoch = 2042
	MainnetIdentityHex       = "8627d33753b0d90a8eae75363d6b393ab9204076545b3af6caf831903e4bedde" +
		"4374f1106551b481fb5ea35e59d51c92165c963881c718216e3e4570a307c08885" +
		"af55c274f19488f709afe021ca3d9e567138ab097860d9146e895407bea182"
	ModeratoEpochLength       = 21600
	ModeratoIdentityFromEpoch = 1747
	ModeratoIdentityHex       = "967ae1a6d3ddbe5cb0fe5e6fc58e74249787b4a277619b549fed6609d0466054" +
		"0dd2449cd7174def35e40f544e5ad72d15d064191a205ed6e0c49619c68f975108" +
		"b614d0d51def9a8416a10a07c4193bea0cbbc4252ec1b33f21095a5d7aa590"
	BatchMax      = 512
	IdentitiesMax = 64
)

const _ = uint(combinerIndexMax - 2*BatchMax)

const _ = uint(MainnetIdentityFromEpoch - IdentitiesMax)

const _ = uint(ModeratoIdentityFromEpoch - IdentitiesMax)

var (
	ErrEpoch           = errors.New("consensus: certificate epoch is not the block's epoch")
	ErrDigest          = errors.New("consensus: certificate digest is not the block hash")
	ErrNoIdentity      = errors.New("consensus: no identity for the certificate's epoch")
	ErrBoundary        = errors.New("consensus: block is not the last of its epoch")
	ErrOutcomeEpoch    = errors.New("consensus: dkg outcome is not for the next epoch")
	ErrNetworkIdentity = errors.New("consensus: dkg outcome identity is not the network identity")
	ErrContext         = errors.New("consensus: certificate round is not the header's consensus context")
)

type Epocher struct {
	Length uint64
}

func (e Epocher) Epoch(height uint64) uint64 { return height / e.Length }

func (e Epocher) First(epoch uint64) uint64 { return epoch * e.Length }

func (e Epocher) Last(epoch uint64) uint64 { return (epoch+1)*e.Length - 1 }

func (e Epocher) IsLast(height uint64) bool { return (height+1)%e.Length == 0 }

type NetworkIdentity struct {
	FromEpoch uint64
	Identity  Identity
}

func MainnetNetworkIdentity() NetworkIdentity {
	return compiledIdentity(MainnetIdentityHex, MainnetIdentityFromEpoch)
}

func ModeratoNetworkIdentity() NetworkIdentity {
	return compiledIdentity(ModeratoIdentityHex, ModeratoIdentityFromEpoch)
}

func compiledIdentity(text string, from uint64) NetworkIdentity {
	raw, err := hex.DecodeString(text)
	assert.NoError(err, "consensus: compiled identity hex")
	id, err := DecodeIdentity(raw)
	assert.NoError(err, "consensus: compiled identity decodes")
	return NetworkIdentity{FromEpoch: from, Identity: id}
}

type Certified struct {
	Height      uint64
	Hash        Digest
	Certificate []byte
	Context     *Proposal
}

type HeightError struct {
	Height uint64
	Err    error
}

func (e *HeightError) Error() string { return fmt.Sprintf("block %d: %v", e.Height, e.Err) }

func (e *HeightError) Unwrap() error { return e.Err }

type Chain struct {
	ns         Namespace
	epocher    Epocher
	network    NetworkIdentity
	identities map[uint64]Identity
}

func NewChain(network NetworkIdentity, epocher Epocher) *Chain {
	assert.Ok(epocher.Length > 0, "consensus: epoch length is positive")
	return &Chain{ns: NewNamespace(TempoNamespace), epocher: epocher, network: network, identities: map[uint64]Identity{}}
}

func NewMainnetChain() *Chain {
	return NewChain(MainnetNetworkIdentity(), Epocher{Length: MainnetEpochLength})
}

func NewModeratoChain() *Chain {
	return NewChain(ModeratoNetworkIdentity(), Epocher{Length: ModeratoEpochLength})
}

func (c *Chain) Epocher() Epocher { return c.epocher }

func (c *Chain) Network() NetworkIdentity { return c.network }

func (c *Chain) Namespace() Namespace { return c.ns }

func (c *Chain) Register(epoch uint64, id Identity) {
	c.identities[epoch] = id
	if len(c.identities) <= IdentitiesMax {
		return
	}
	oldest := epoch
	for e := range c.identities {
		oldest = min(oldest, e)
	}
	delete(c.identities, oldest)
}

func (c *Chain) Identity(epoch uint64) (Identity, bool) {
	if id, ok := c.identities[epoch]; ok {
		return id, true
	}
	if epoch >= c.network.FromEpoch {
		return c.network.Identity, true
	}
	return Identity{}, false
}

func (c *Chain) RegisterBoundary(height uint64, extraData []byte) (uint64, error) {
	if !c.epocher.IsLast(height) {
		return 0, &HeightError{Height: height, Err: ErrBoundary}
	}
	outcome, err := dkg.Decode(extraData)
	if err != nil {
		return 0, &HeightError{Height: height, Err: err}
	}
	next := c.epocher.Epoch(height) + 1
	if outcome.Epoch != next {
		return 0, &HeightError{Height: height, Err: ErrOutcomeEpoch}
	}
	id, err := DecodeIdentity(outcome.Identity[:])
	if err != nil {
		return 0, &HeightError{Height: height, Err: err}
	}
	if next == c.network.FromEpoch && id.raw != c.network.Identity.raw {
		return 0, &HeightError{Height: height, Err: ErrNetworkIdentity}
	}
	c.Register(next, id)
	return next, nil
}

func (c *Chain) Verify(items []Certified) error {
	assert.Ok(len(items) <= BatchMax, "consensus: batch within BatchMax")
	groups := map[uint64][]signed{}
	members := map[uint64][]int{}
	for i, it := range items {
		f, err := DecodeFinalization(it.Certificate)
		if err != nil {
			return &HeightError{Height: it.Height, Err: err}
		}
		epoch := f.Proposal.Round.Epoch
		if epoch != c.epocher.Epoch(it.Height) {
			return &HeightError{Height: it.Height, Err: ErrEpoch}
		}
		if f.Proposal.Payload != it.Hash {
			return &HeightError{Height: it.Height, Err: ErrDigest}
		}
		if err := it.checkContext(proposalCheck{proposal: f.Proposal, boundary: c.epocher.IsLast(it.Height)}); err != nil {
			return &HeightError{Height: it.Height, Err: err}
		}
		groups[epoch] = append(groups[epoch], finalizationEntries(c.ns, f)...)
		members[epoch] = append(members[epoch], i)
	}
	for _, epoch := range slices.Sorted(maps.Keys(groups)) {
		entries := groups[epoch]
		id, ok := c.Identity(epoch)
		if !ok {
			return &HeightError{Height: items[members[epoch][0]].Height, Err: ErrNoIdentity}
		}
		if verifySameSigner(id, entries) == nil {
			continue
		}
		for _, i := range members[epoch] {
			f, err := DecodeFinalization(items[i].Certificate)
			if err != nil {
				return &HeightError{Height: items[i].Height, Err: err}
			}
			if err := VerifyFinalization(id, c.ns, f); err != nil {
				return &HeightError{Height: items[i].Height, Err: err}
			}
		}
		return &HeightError{Height: items[members[epoch][0]].Height, Err: ErrCertificate}
	}
	return nil
}

type proposalCheck struct {
	proposal Proposal
	boundary bool
}

func (c Certified) checkContext(chk proposalCheck) error {
	p := chk.proposal
	if c.Context == nil {
		return nil
	}
	if chk.boundary && c.Context.Round.Epoch == p.Round.Epoch && p.Round.View > c.Context.Round.View {
		return nil
	}
	if c.Context.Round != p.Round {
		return ErrContext
	}
	if c.Context.Parent != p.Parent {
		return ErrContext
	}
	return nil
}
