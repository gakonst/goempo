package types

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

const (
	bloomBitMask    = BloomLength*8 - 1
	bloomPairs      = 3
	receiptHint     = 512
	topicsPerLogMax = 4
)

const _ = uint(BloomLength*8 - bloomBitMask - 1)

const _ = uint(listItemsMax - topicsPerLogMax)

const _ = uint(scalarBytes - 2*bloomPairs)

type Log struct {
	Address primitives.Address
	Topics  []primitives.Hash
	Data    []byte
}

type Receipt struct {
	Type              TxType
	Success           bool
	CumulativeGasUsed uint64
	Bloom             Bloom
	Logs              []Log
}

func decodeLog(s *rlp.Stream) (Log, error) {
	var l Log
	list, err := s.List()
	if err != nil {
		return l, err
	}
	if l.Address, err = list.Address(); err != nil {
		return l, err
	}
	if l.Topics, err = decodeList(&list, decodeHash); err != nil {
		return l, err
	}
	if len(l.Topics) > topicsPerLogMax {
		return l, ErrReceipt
	}
	if l.Data, err = readBytesCopy(&list); err != nil {
		return l, err
	}
	return l, list.Finish()
}

func DecodeReceipt(envelope []byte) (Receipt, error) {
	var r Receipt
	if len(envelope) == 0 {
		return r, ErrReceipt
	}
	if len(envelope) > encodedBytesMax {
		return r, ErrTooLarge
	}
	body := envelope
	if envelope[0] < listPrefix {
		r.Type = TxType(envelope[0])
		body = envelope[1:]
	}
	s := rlp.NewStream(body)
	list, err := s.List()
	if err != nil {
		return r, err
	}
	if err = r.decodeFields(&list); err != nil {
		return r, err
	}
	if err = list.Finish(); err != nil {
		return r, err
	}
	return r, s.Finish()
}

func (r *Receipt) decodeFields(list *rlp.Stream) error {
	var err error
	if r.Success, err = readBool(list); err != nil {
		return err
	}
	if r.CumulativeGasUsed, err = list.Uint64(); err != nil {
		return err
	}
	if err = list.BytesFixed(r.Bloom[:]); err != nil {
		return err
	}
	r.Logs, err = decodeList(list, decodeLog)
	return err
}

func (r *Receipt) Encode() []byte {
	e := rlp.NewEncoder(receiptHint)
	r.encodeTo(e)
	return e.Encoded()
}

func (r *Receipt) encodeTo(e *rlp.Encoder) {
	if r.Type != TxTypeLegacy {
		e.WriteRaw([]byte{byte(r.Type)})
	}
	e.ListStart()
	e.WriteUint64(rlpBool(r.Success).uint64())
	e.WriteUint64(r.CumulativeGasUsed)
	e.WriteBytes(r.Bloom[:])
	e.ListStart()
	for i := range r.Logs {
		l := &r.Logs[i]
		e.ListStart()
		e.WriteBytes(l.Address[:])
		e.ListStart()
		for k := range l.Topics {
			e.WriteBytes(l.Topics[k][:])
		}
		e.ListEnd()
		e.WriteBytes(l.Data)
		e.ListEnd()
	}
	e.ListEnd()
	e.ListEnd()
}

func (b *Bloom) Add(item []byte) {
	b.addHash(keccak.Sum256(item))
}

func (b *Bloom) addHash(h primitives.Hash) {
	for i := 0; i < bloomPairs; i++ {
		bit := (uint(h[2*i])<<8 | uint(h[2*i+1])) & bloomBitMask
		b[BloomLength-1-bit/8] |= 1 << (bit % 8)
	}
}

func (b *Bloom) Or(other *Bloom) {
	for i := range b {
		b[i] |= other[i]
	}
}

func LogsBloom(logs []Log) Bloom {
	var b Bloom
	for i := range logs {
		b.Add(logs[i].Address[:])
		for k := range logs[i].Topics {
			b.Add(logs[i].Topics[k][:])
		}
	}
	return b
}
