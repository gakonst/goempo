// Package sync follows a Tempo chain: it takes headers and bodies from a
// Source (devp2p peers or an upstream node), links each header to its
// parent, checks each body against its header, and hands the blocks in
// order to an Executor.
package sync

import (
	"errors"
	"strconv"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
)

var (
	ErrParent       = errors.New("sync: header does not link to its parent")
	ErrNumber       = errors.New("sync: header number is out of sequence")
	ErrTxRoot       = errors.New("sync: transactions do not match the header's root")
	ErrOmmers       = errors.New("sync: ommers do not match the header's hash")
	ErrWithdrawals  = errors.New("sync: withdrawals do not match the header's root")
	ErrBodyShape    = errors.New("sync: body is not a list of transactions, ommers and withdrawals")
	ErrAnchor       = errors.New("sync: chain does not reach the anchored hash")
	ErrShortReply   = errors.New("sync: source returned fewer blocks than asked")
	ErrHeaderLength = errors.New("sync: header and body counts differ")
)

const withdrawalsMax = 1 << 16

var emptyRoot = trie.OrderedRoot(nil)

type Raw struct {
	Header []byte
	Body   []byte
}

type Link struct {
	Number uint64
	Hash   primitives.Hash
}

type BlockError struct {
	Number uint64
	Err    error
}

func (e *BlockError) Error() string {
	return "sync: block " + strconv.FormatUint(e.Number, 10) + ": " + e.Err.Error()
}

func (e *BlockError) Unwrap() error { return e.Err }

type bodyParts struct {
	ommers      []byte
	withdrawals [][]byte
	hasWithdraw bool
}

func splitBody(body []byte) (bodyParts, error) {
	var parts bodyParts
	outer := rlp.NewStream(body)
	list, err := outer.List()
	if err != nil {
		return parts, errors.Join(ErrBodyShape, err)
	}
	if _, err := list.Raw(); err != nil {
		return parts, errors.Join(ErrBodyShape, err)
	}
	if parts.ommers, err = list.Raw(); err != nil {
		return parts, errors.Join(ErrBodyShape, err)
	}
	if list.Empty() {
		return parts, nil
	}
	parts.hasWithdraw = true
	withdrawals, err := list.List()
	if err != nil {
		return parts, errors.Join(ErrBodyShape, err)
	}
	n, err := withdrawals.Count()
	if err != nil {
		return parts, errors.Join(ErrBodyShape, err)
	}
	if n > withdrawalsMax {
		return parts, ErrBodyShape
	}
	for range n {
		w, err := withdrawals.Raw()
		if err != nil {
			return parts, errors.Join(ErrBodyShape, err)
		}
		parts.withdrawals = append(parts.withdrawals, w)
	}
	if !list.Empty() {
		return parts, ErrBodyShape
	}
	return parts, nil
}

func Assemble(raw Raw) (types.Block, error) {
	outer := rlp.NewStream(raw.Body)
	items, err := outer.List()
	if err != nil {
		return types.Block{}, errors.Join(ErrBodyShape, err)
	}
	return types.DecodeBlock(rlp.EncodeList(raw.Header, items.Rest()))
}

type Verified struct {
	Block types.Block
	Link  Link
}

func Verify(parent Link, raw Raw) (Verified, error) {
	var v Verified
	var err error
	if v.Block, err = Assemble(raw); err != nil {
		return v, err
	}
	inner := &v.Block.Header.Inner
	v.Link = Link{Number: inner.Number, Hash: keccak.Sum256(raw.Header)}
	if inner.Number != parent.Number+1 {
		return v, ErrNumber
	}
	if inner.ParentHash != parent.Hash {
		return v, ErrParent
	}
	if trie.OrderedRoot(v.Block.TransactionEnvelopes()) != inner.TransactionsRoot {
		return v, ErrTxRoot
	}
	parts, err := splitBody(raw.Body)
	if err != nil {
		return v, err
	}
	if keccak.Sum256(parts.ommers) != inner.OmmersHash {
		return v, ErrOmmers
	}
	if !parts.hasWithdraw && inner.WithdrawalsRoot == (primitives.Hash{}) {
		return v, nil
	}
	withdrawalsRoot := emptyRoot
	if parts.hasWithdraw {
		withdrawalsRoot = trie.OrderedRoot(parts.withdrawals)
	}
	if withdrawalsRoot != inner.WithdrawalsRoot {
		return v, ErrWithdrawals
	}
	return v, nil
}
