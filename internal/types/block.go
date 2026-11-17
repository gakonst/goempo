package types

import (
	"bytes"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

const (
	blockFieldsMin = 3
	blockFieldsMax = 4
	blockHint      = 1 << 16
)

const _ = uint(blockFieldsMax - blockFieldsMin)

type Withdrawal struct {
	Index          uint64
	ValidatorIndex uint64
	Address        primitives.Address
	Amount         uint64
}

type Block struct {
	Header         Header
	Transactions   []Transaction
	Ommers         []Header
	HasWithdrawals bool
	Withdrawals    []Withdrawal
}

// DecodeBlock decodes a copy of b, so the block never aliases the caller's
// buffer. BlockDecoder decodes in place.
func DecodeBlock(b []byte) (Block, error) {
	if len(b) > encodedBytesMax {
		return Block{}, ErrTooLarge
	}
	return (*BlockDecoder)(nil).decodeBlock(bytes.Clone(b))
}

func (d *BlockDecoder) decodeBlock(b []byte) (Block, error) {
	var blk Block
	if len(b) > encodedBytesMax {
		return blk, ErrTooLarge
	}
	s := rlp.NewStream(b)
	list, err := s.List()
	if err != nil {
		return blk, err
	}
	n, err := list.Count()
	if err != nil {
		return blk, err
	}
	if n < blockFieldsMin {
		return blk, ErrBlockFields
	}
	if n > blockFieldsMax {
		return blk, ErrBlockFields
	}
	if err = blk.decodeFields(&list, n, d); err != nil {
		return blk, err
	}
	if err = list.Finish(); err != nil {
		return blk, err
	}
	return blk, s.Finish()
}

func (blk *Block) decodeFields(list *rlp.Stream, fields int, d *BlockDecoder) error {
	var err error
	if blk.Header, err = decodeHeader(list); err != nil {
		return err
	}
	if blk.Transactions, err = d.decodeTransactions(list); err != nil {
		return err
	}
	if blk.Ommers, err = decodeOmmers(list); err != nil {
		return err
	}
	if fields == blockFieldsMax {
		blk.HasWithdrawals = true
		blk.Withdrawals, err = decodeWithdrawals(list)
	}
	return err
}

func decodeWithdrawal(s *rlp.Stream) (Withdrawal, error) {
	var w Withdrawal
	list, err := s.List()
	if err != nil {
		return w, err
	}
	if w.Index, err = list.Uint64(); err != nil {
		return w, err
	}
	if w.ValidatorIndex, err = list.Uint64(); err != nil {
		return w, err
	}
	if w.Address, err = list.Address(); err != nil {
		return w, err
	}
	if w.Amount, err = list.Uint64(); err != nil {
		return w, err
	}
	return w, list.Finish()
}

func (blk *Block) Encode() []byte {
	e := rlp.NewEncoder(blockHint)
	e.ListStart()
	blk.Header.encodeTo(e)
	e.ListStart()
	for i := range blk.Transactions {
		blk.Transactions[i].encodeInBody(e)
	}
	e.ListEnd()
	e.ListStart()
	for i := range blk.Ommers {
		blk.Ommers[i].encodeTo(e)
	}
	e.ListEnd()
	if blk.HasWithdrawals {
		e.ListStart()
		for i := range blk.Withdrawals {
			w := &blk.Withdrawals[i]
			e.ListStart()
			e.WriteUint64(w.Index)
			e.WriteUint64(w.ValidatorIndex)
			e.WriteBytes(w.Address[:])
			e.WriteUint64(w.Amount)
			e.ListEnd()
		}
		e.ListEnd()
	}
	e.ListEnd()
	return e.Encoded()
}

func (blk *Block) Hash() primitives.Hash {
	return blk.Header.Hash()
}

func (blk *Block) TransactionEnvelopes() [][]byte {
	out := make([][]byte, len(blk.Transactions))
	for i := range blk.Transactions {
		out[i] = blk.Transactions[i].Encode()
	}
	return out
}

// decodeOmmers and decodeWithdrawals are decodeList with a direct call, so
// their list stream stays on the stack: blocks decode millions of them and
// nearly all are empty.
func decodeOmmers(s *rlp.Stream) ([]Header, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	n, err := list.Count()
	if err != nil {
		return nil, err
	}
	if n > listItemsMax {
		return nil, ErrTooManyItems
	}
	out := make([]Header, 0, n)
	for i := 0; i < n; i++ {
		h, err := decodeHeader(&list)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, list.Finish()
}

func decodeWithdrawals(s *rlp.Stream) ([]Withdrawal, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	n, err := list.Count()
	if err != nil {
		return nil, err
	}
	if n > listItemsMax {
		return nil, ErrTooManyItems
	}
	out := make([]Withdrawal, 0, n)
	for i := 0; i < n; i++ {
		w, err := decodeWithdrawal(&list)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, list.Finish()
}
