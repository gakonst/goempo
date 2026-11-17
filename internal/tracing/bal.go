package tracing

import (
	"bytes"
	"maps"
	"slices"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/u256"
)

type BALChange struct {
	Index uint64
	Value u256.Int
}

type BALSlot struct {
	Key     evm.Hash
	Changes []BALChange
}

type BALNonce struct {
	Index uint64
	Value uint64
}

type BALCode struct {
	Index uint64
	Code  []byte
}

type BALAccount struct {
	Address evm.Address
	Storage []BALSlot
	Reads   []evm.Hash
	Balance []BALChange
	Nonce   []BALNonce
	Code    []BALCode
}

type BlockAccessList []BALAccount

type IndexedTrace struct {
	Index uint64
	Trace *TxTrace
}

type balBuilder struct {
	changes map[evm.Hash][]BALChange
	reads   map[evm.Hash]struct{}
	out     BALAccount
}

func BuildAccessList(txs []IndexedTrace, always []evm.Address) BlockAccessList {
	accounts := make(map[evm.Address]*balBuilder)
	get := func(addr evm.Address) *balBuilder {
		if a, ok := accounts[addr]; ok {
			return a
		}
		a := &balBuilder{changes: map[evm.Hash][]BALChange{}, reads: map[evm.Hash]struct{}{}, out: BALAccount{Address: addr}}
		accounts[addr] = a
		return a
	}
	for _, addr := range always {
		get(addr)
	}
	for _, tx := range txs {
		if tx.Trace == nil {
			continue
		}
		for _, acc := range tx.Trace.Accounts {
			addAccountChanges(get(acc.Address), acc, tx.Index)
		}
	}
	addrs := slices.SortedFunc(maps.Keys(accounts), func(x, y evm.Address) int { return bytes.Compare(x[:], y[:]) })
	out := make(BlockAccessList, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, accounts[addr].finish())
	}
	return out
}

func addAccountChanges(b *balBuilder, a *Account, index uint64) {
	for _, k := range a.Slots {
		post, ok := a.Post[k]
		if ok && post != a.Pre[k] {
			b.changes[k] = append(b.changes[k], BALChange{Index: index, Value: post})
			continue
		}
		b.reads[k] = struct{}{}
	}
	if a.PostBalance != a.Balance {
		b.out.Balance = append(b.out.Balance, BALChange{Index: index, Value: a.PostBalance})
	}
	if a.PostNonce != a.Nonce {
		b.out.Nonce = append(b.out.Nonce, BALNonce{Index: index, Value: a.PostNonce})
	}
	if !bytes.Equal(a.PostCode, a.Code) {
		b.out.Code = append(b.out.Code, BALCode{Index: index, Code: a.PostCode})
	}
}

func (b *balBuilder) finish() BALAccount {
	out := b.out
	for _, k := range slices.SortedFunc(maps.Keys(b.changes), func(x, y evm.Hash) int { return bytes.Compare(x[:], y[:]) }) {
		out.Storage = append(out.Storage, BALSlot{Key: k, Changes: b.changes[k]})
		delete(b.reads, k)
	}
	out.Reads = slices.SortedFunc(maps.Keys(b.reads), func(x, y evm.Hash) int { return bytes.Compare(x[:], y[:]) })
	return out
}

func (l BlockAccessList) JSON() []byte {
	w := &writer{}
	w.arrOpen()
	for i := range l {
		a := &l[i]
		w.objOpen()
		w.kAddress("address", a.Address)
		w.key("balanceChanges")
		w.arrOpen()
		for _, c := range a.Balance {
			writeIndexed(w, c.Index, func() { w.quantity256(c.Value) })
		}
		w.arrClose()
		w.key("codeChanges")
		w.arrOpen()
		for _, c := range a.Code {
			w.objOpen()
			w.kQuantity("index", c.Index)
			w.kData("code", c.Code)
			w.objClose()
		}
		w.arrClose()
		w.key("nonceChanges")
		w.arrOpen()
		for _, c := range a.Nonce {
			writeIndexed(w, c.Index, func() { w.quantity(c.Value) })
		}
		w.arrClose()
		w.key("storageChanges")
		w.arrOpen()
		for _, s := range a.Storage {
			w.objOpen()
			w.key("changes")
			w.arrOpen()
			for _, c := range s.Changes {
				writeIndexed(w, c.Index, func() { w.quantity256(c.Value) })
			}
			w.arrClose()
			w.key("key")
			w.bareQuantityHash(s.Key)
			w.objClose()
		}
		w.arrClose()
		w.key("storageReads")
		w.arrOpen()
		for _, k := range a.Reads {
			w.bareQuantityHash(k)
		}
		w.arrClose()
		w.objClose()
	}
	w.arrClose()
	return w.buf
}

func writeIndexed(w *writer, index uint64, value func()) {
	w.objOpen()
	w.kQuantity("index", index)
	w.key("value")
	value()
	w.objClose()
}

func (l BlockAccessList) RLP() []byte {
	e := rlp.NewEncoder(len(l) * 64)
	e.ListStart()
	for i := range l {
		a := &l[i]
		e.ListStart()
		e.WriteBytes(a.Address[:])
		e.ListStart()
		for _, s := range a.Storage {
			e.ListStart()
			e.WriteUintBytes(s.Key[:])
			e.ListStart()
			for _, c := range s.Changes {
				writeRLPChange(e, c)
			}
			e.ListEnd()
			e.ListEnd()
		}
		e.ListEnd()
		e.ListStart()
		for _, k := range a.Reads {
			e.WriteUintBytes(k[:])
		}
		e.ListEnd()
		e.ListStart()
		for _, c := range a.Balance {
			writeRLPChange(e, c)
		}
		e.ListEnd()
		e.ListStart()
		for _, c := range a.Nonce {
			e.ListStart()
			e.WriteUint64(c.Index)
			e.WriteUint64(c.Value)
			e.ListEnd()
		}
		e.ListEnd()
		e.ListStart()
		for _, c := range a.Code {
			e.ListStart()
			e.WriteUint64(c.Index)
			e.WriteBytes(c.Code)
			e.ListEnd()
		}
		e.ListEnd()
		e.ListEnd()
	}
	e.ListEnd()
	return e.Encoded()
}

func writeRLPChange(e *rlp.Encoder, c BALChange) {
	e.ListStart()
	e.WriteUint64(c.Index)
	b := c.Value.Bytes32()
	e.WriteUintBytes(b[:])
	e.ListEnd()
}

func (w *writer) bareQuantityHash(h evm.Hash) {
	w.quantity256(u256.FromBytes32(h))
}
