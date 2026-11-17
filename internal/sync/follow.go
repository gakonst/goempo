package sync

import (
	"errors"

	"github.com/gakonst/goempo/internal/p2p"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

const (
	BatchMax     = 128
	sourceTryMax = 4
)

var ErrNoProgress = errors.New("sync: source made no progress")

type Executor interface {
	Execute(block *types.Block) error
	Head() Link
}

type Source interface {
	Tip() (Link, error)
	Blocks(from uint64, count int) ([]Raw, error)
}

type Progress struct {
	Head     Link
	Tip      Link
	Executed int
}

type Follower struct {
	source Source
	exec   Executor
	batch  int
}

func NewFollower(source Source, exec Executor, batch int) *Follower {
	return &Follower{source: source, exec: exec, batch: max(1, min(batch, BatchMax))}
}

func (f *Follower) Step() (Progress, error) {
	head := f.exec.Head()
	p := Progress{Head: head}
	tip, err := f.source.Tip()
	if err != nil {
		return p, err
	}
	p.Tip = tip
	if tip.Number <= head.Number {
		return p, nil
	}
	count := int(min(uint64(f.batch), tip.Number-head.Number))
	raws, err := f.source.Blocks(head.Number+1, count)
	if err != nil {
		return p, err
	}
	if len(raws) == 0 {
		return p, ErrNoProgress
	}
	verified := make([]Verified, 0, len(raws))
	parent := head
	for _, raw := range raws[:min(len(raws), count)] {
		v, err := Verify(parent, raw)
		if err != nil {
			return p, &BlockError{Number: parent.Number + 1, Err: err}
		}
		verified = append(verified, v)
		parent = v.Link
	}
	if parent.Number == tip.Number && parent.Hash != tip.Hash {
		return p, &BlockError{Number: tip.Number, Err: ErrAnchor}
	}
	for i := range verified {
		if err := f.exec.Execute(&verified[i].Block); err != nil {
			return p, &BlockError{Number: verified[i].Link.Number, Err: err}
		}
		p.Head = verified[i].Link
		p.Executed++
	}
	return p, nil
}

type HeaderChain struct {
	head Link
}

func NewHeaderChain(anchor Link) *HeaderChain {
	return &HeaderChain{head: anchor}
}

func (c *HeaderChain) Execute(block *types.Block) error {
	c.head = Link{Number: block.Header.Inner.Number, Hash: block.Hash()}
	return nil
}

func (c *HeaderChain) Head() Link { return c.head }

func hashesOf(headers [][]byte) []primitives.Hash {
	out := make([]primitives.Hash, len(headers))
	for i, h := range headers {
		out[i] = headerHash(h)
	}
	return out
}

const _ = uint(p2p.ItemsMax - BatchMax)

const _ = uint(peersMax - sourceTryMax)

const _ = uint(withdrawalsMax - p2p.ItemsMax)

const _ = uint(BatchMax - peersMax)
