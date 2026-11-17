package tracing

import (
	"bytes"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type deltaKind int

const (
	deltaSame deltaKind = iota
	deltaAdded
	deltaRemoved
	deltaChanged
)

type accountDiff struct {
	addr    evm.Address
	balance deltaKind
	nonce   deltaKind
	code    deltaKind
	slots   []evm.Hash
	added   bool
}

func StateDiffJSON(t *TxTrace) []byte {
	w := &writer{}
	w.objOpen()
	for _, a := range sortedAccounts(t.Accounts) {
		d, ok := diffAccount(a)
		if ok {
			writeAccountDiff(w, a, &d)
		}
	}
	w.objClose()
	return w.buf
}

func changedSlots(a *Account) []evm.Hash {
	var out []evm.Hash
	for _, k := range a.Slots {
		if a.Pre[k] != a.Post[k] {
			out = append(out, k)
		}
	}
	return sortedSlotList(out)
}

func sortedSlotList(keys []evm.Hash) []evm.Hash {
	m := make(map[evm.Hash]u256.Int, len(keys))
	for _, k := range keys {
		m[k] = u256.Int{}
	}
	return sortedHashes(m)
}

func postEmpty(a *Account) bool {
	return a.PostNonce == 0 && a.PostBalance.IsZero() && len(a.PostCode) == 0
}

func diffAccount(a *Account) (accountDiff, bool) {
	d := accountDiff{addr: a.Address}
	if a.Destroyed {
		if !a.Existed {
			return d, false
		}
		d.balance, d.nonce, d.code = deltaRemoved, deltaRemoved, deltaRemoved
		return d, true
	}
	d.slots = changedSlots(a)
	if !a.Existed && !postEmpty(a) {
		d.balance, d.nonce, d.code, d.added = deltaAdded, deltaAdded, deltaAdded, true
		return d, true
	}
	if !bytes.Equal(a.Code, a.PostCode) {
		d.code = deltaChanged
	}
	same := a.Balance == a.PostBalance && a.Nonce == a.PostNonce && d.code == deltaSame
	if len(d.slots) == 0 && same {
		return d, false
	}
	if a.Balance != a.PostBalance {
		d.balance = deltaChanged
	}
	if a.Nonce != a.PostNonce {
		d.nonce = deltaChanged
	}
	return d, true
}

type deltaValue struct {
	kind deltaKind
	from func(w *writer)
	to   func(w *writer)
}

func writeDelta(w *writer, k field, v deltaValue) {
	w.key(k)
	switch v.kind {
	case deltaSame:
		w.str("=")
	case deltaAdded:
		w.objOpen()
		w.key("+")
		v.to(w)
		w.objClose()
	case deltaRemoved:
		w.objOpen()
		w.key("-")
		v.from(w)
		w.objClose()
	case deltaChanged:
		w.objOpen()
		w.key("*")
		w.objOpen()
		w.key("from")
		v.from(w)
		w.key("to")
		v.to(w)
		w.objClose()
		w.objClose()
	default:
		assert.Unreachable("deltaKind: unhandled value")
	}
}

func writeAccountDiff(w *writer, a *Account, d *accountDiff) {
	w.key(field(a.Address.Hex()))
	w.objOpen()
	writeDelta(w, "balance", deltaValue{kind: d.balance,
		from: func(w *writer) { w.quantity256(a.Balance) },
		to:   func(w *writer) { w.quantity256(a.PostBalance) }})
	writeDelta(w, "code", deltaValue{kind: d.code,
		from: func(w *writer) { w.data(a.Code) },
		to:   func(w *writer) { w.data(a.PostCode) }})
	writeDelta(w, "nonce", deltaValue{kind: d.nonce,
		from: func(w *writer) { w.quantity(a.Nonce) },
		to:   func(w *writer) { w.quantity(a.PostNonce) }})
	w.key("storage")
	w.objOpen()
	slotKind := deltaChanged
	if d.added {
		slotKind = deltaAdded
	}
	for _, k := range d.slots {
		pre, post := a.Pre[k], a.Post[k]
		writeDelta(w, field(k.Hex()), deltaValue{kind: slotKind,
			from: func(w *writer) { w.word(pre) },
			to:   func(w *writer) { w.word(post) }})
	}
	w.objClose()
	w.objClose()
}
