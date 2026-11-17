package tracing

import (
	"bytes"
	"encoding/hex"
	"maps"
	"slices"
	"strconv"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/u256"
)

type field string

type jbool bool

type writer struct {
	buf   []byte
	comma bool
}

func (w *writer) sep() {
	if w.comma {
		w.buf = append(w.buf, ',')
	}
	w.comma = true
}

func (w *writer) objOpen() {
	w.sep()
	w.buf = append(w.buf, '{')
	w.comma = false
}

func (w *writer) objClose() {
	w.buf = append(w.buf, '}')
	w.comma = true
}

func (w *writer) arrOpen() {
	w.sep()
	w.buf = append(w.buf, '[')
	w.comma = false
}

func (w *writer) arrClose() {
	w.buf = append(w.buf, ']')
	w.comma = true
}

func (w *writer) key(k field) {
	w.sep()
	w.buf = strconv.AppendQuote(w.buf, string(k))
	w.buf = append(w.buf, ':')
	w.comma = false
}

func (w *writer) str(s string) {
	w.sep()
	w.buf = strconv.AppendQuote(w.buf, s)
}

func (w *writer) raw(s []byte) {
	w.sep()
	w.buf = append(w.buf, s...)
}

func (w *writer) boolean(b jbool) {
	w.sep()
	w.buf = strconv.AppendBool(w.buf, bool(b))
}

func (w *writer) integer(n uint64) {
	w.sep()
	w.buf = strconv.AppendUint(w.buf, n, 10)
}

func (w *writer) signed(n int64) {
	w.sep()
	w.buf = strconv.AppendInt(w.buf, n, 10)
}

func (w *writer) quantity(n uint64) {
	w.sep()
	w.buf = append(w.buf, '"', '0', 'x')
	w.buf = strconv.AppendUint(w.buf, n, 16)
	w.buf = append(w.buf, '"')
}

func (w *writer) quantity256(v u256.Int) {
	w.str(v.Hex())
}

func (w *writer) data(b []byte) {
	w.sep()
	w.buf = append(w.buf, '"', '0', 'x')
	w.buf = hex.AppendEncode(w.buf, b)
	w.buf = append(w.buf, '"')
}

func (w *writer) bareHex(b []byte) {
	w.sep()
	w.buf = append(w.buf, '"')
	w.buf = hex.AppendEncode(w.buf, b)
	w.buf = append(w.buf, '"')
}

func (w *writer) address(a evm.Address) { w.data(a[:]) }

func (w *writer) hash(h evm.Hash) { w.data(h[:]) }

func (w *writer) word(v u256.Int) {
	b := v.Bytes32()
	w.data(b[:])
}

func (w *writer) kData(k field, b []byte) {
	w.key(k)
	w.data(b)
}

func (w *writer) kQuantity(k field, n uint64) {
	w.key(k)
	w.quantity(n)
}

func (w *writer) kAddress(k field, a evm.Address) {
	w.key(k)
	w.address(a)
}

func (w *writer) kStr(k field, v string) {
	w.key(k)
	w.str(v)
}

func sortedHashes(m map[evm.Hash]u256.Int) []evm.Hash {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b evm.Hash) int { return bytes.Compare(a[:], b[:]) })
	return keys
}

func sortedAccounts(accts []*Account) []*Account {
	out := append([]*Account(nil), accts...)
	slices.SortStableFunc(out, func(a, b *Account) int { return bytes.Compare(a.Address[:], b.Address[:]) })
	return out
}
