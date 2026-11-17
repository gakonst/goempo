package sim

import (
	"errors"
	"fmt"
	"math"

	"github.com/gakonst/goempo/internal/consensus"
	tdkg "github.com/gakonst/goempo/internal/tempo/dkg"
	"github.com/gakonst/goempo/internal/types"
)

var (
	ErrLink       = errors.New("sim: block does not link to its parent")
	ErrUncertTail = errors.New("sim: chain ends without a finalization certificate")
)

type Report struct {
	Height      uint64
	Certified   int
	Linked      int
	Transitions int
	Identities  []string
}

type verifier struct {
	chain   *consensus.Chain
	pending []consensus.Certified
	report  Report
}

func (v *verifier) flush() error {
	for range len(v.pending)/consensus.BatchMax + 1 {
		if len(v.pending) == 0 {
			break
		}
		n := min(len(v.pending), consensus.BatchMax)
		if err := v.chain.Verify(v.pending[:n]); err != nil {
			return err
		}
		v.report.Certified += n
		v.pending = v.pending[n:]
	}
	return nil
}

func Verify(r Result, epochLength uint64) (Report, error) {
	g, err := types.DecodeBlock(r.Genesis)
	if err != nil {
		return Report{}, err
	}
	o, err := tdkg.Decode(g.Header.Inner.ExtraData)
	if err != nil {
		return Report{}, err
	}
	id, err := consensus.DecodeIdentity(o.Identity[:])
	if err != nil {
		return Report{}, err
	}
	v := &verifier{chain: consensus.NewChain(consensus.NetworkIdentity{FromEpoch: math.MaxUint64}, consensus.Epocher{Length: epochLength})}
	v.chain.Register(0, id)
	v.report.Identities = append(v.report.Identities, fmt.Sprintf("0:%x", o.Identity[:8]))
	parent := g.Hash()
	uncertified := 0
	for i, raw := range r.Blocks {
		b, err := types.DecodeBlock(raw)
		if err != nil {
			return v.report, err
		}
		h := b.Header.Inner.Number
		if h != uint64(i)+1 || b.Header.Inner.ParentHash != parent || b.Header.Consensus == nil {
			return v.report, &consensus.HeightError{Height: h, Err: ErrLink}
		}
		parent = b.Hash()
		uncertified++
		if cert := r.Certificates[h]; cert != nil {
			c := b.Header.Consensus
			ctx := consensus.Proposal{Round: consensus.Round{Epoch: c.Epoch, View: c.View}, Parent: c.ParentView}
			v.pending = append(v.pending, consensus.Certified{Height: h, Hash: consensus.Digest(parent), Certificate: cert, Context: &ctx})
			v.report.Linked += uncertified - 1
			uncertified = 0
		}
		if err := v.boundary(b, uncertified); err != nil {
			return v.report, err
		}
		v.report.Height = h
	}
	if uncertified > 0 {
		return v.report, ErrUncertTail
	}
	return v.report, v.flush()
}

func (v *verifier) boundary(b types.Block, uncertified int) error {
	h := b.Header.Inner.Number
	if !v.chain.Epocher().IsLast(h) {
		return nil
	}
	if uncertified > 0 {
		return &consensus.HeightError{Height: h, Err: ErrUncertTail}
	}
	if err := v.flush(); err != nil {
		return err
	}
	next, err := v.chain.RegisterBoundary(h, b.Header.Inner.ExtraData)
	if err != nil {
		return err
	}
	id, _ := v.chain.Identity(next)
	raw := id.Bytes()
	v.report.Identities = append(v.report.Identities, fmt.Sprintf("%d:%x", next, raw[:8]))
	v.report.Transitions++
	return nil
}
