package keccak

import (
	"hash/maphash"
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

// A memo of Keccak-256 over short inputs.
//
// Execution hashes the same short preimages again and again: a TIP-20
// transfer derives the fee payer's balance slot several times, every
// transaction derives the fee manager's slots afresh, and the bloom hashes the
// same token address and Transfer topic for every log. The memo is
// direct-mapped and keyed by the whole input (at most memoInputMax bytes), so
// a hit returns exactly what hashing returns: it changes cost, never results.
//
// A memo is never shared: Hash, Sum256 and Sum256Concat borrow one from a
// pool for the length of one lookup, so it belongs to the borrowing goroutine
// and needs no locking. sync.Pool keeps one per P, so the goroutine executing
// a block keeps finding its own warm memo.

// memoInputMax is the longest input the memo keeps; longer inputs are hashed
// directly. Two words covers mapping slots (key ‖ slot) and addresses.
const memoInputMax = 64

// memoEntries is a power of two. 8 K entries of 97 bytes: under 800 KiB per P.
const memoEntries = 1 << 13

const _ = uint(memoEntries & (memoEntries - 1)) // zero: a power of two

const _ = uint(255 - memoInputMax) // a length fits the entry's byte

type memoEntry struct {
	key    [memoInputMax]byte
	digest primitives.Hash
	// length is the input's length; 0 marks an empty entry (the empty input
	// is never memoised).
	length uint8
}

type memo struct {
	entries [memoEntries]memoEntry
}

var memoSeed = maphash.MakeSeed()

var memos = sync.Pool{New: func() any { return new(memo) }}

func borrowMemo() *memo {
	m, ok := memos.Get().(*memo)
	assert.Ok(ok, "keccak memo pool holds memos")
	return m
}

func memoizable(b []byte) bool {
	return len(b) > 0 && len(b) <= memoInputMax
}

func (m *memo) entry(b []byte) *memoEntry {
	index := maphash.Bytes(memoSeed, b) & (memoEntries - 1)
	return &m.entries[index]
}

func (e *memoEntry) holds(b []byte) bool {
	return int(e.length) == len(b) && string(e.key[:len(b)]) == string(b)
}

// memoHash returns keccak256(b) for a short input, from the memo or by
// hashing with h (which it resets), or with a pooled hasher when h is nil.
func memoHash(h *Hasher, b []byte) primitives.Hash {
	assert.Ok(memoizable(b), "memoHash takes short non-empty inputs")
	m := borrowMemo()
	e := m.entry(b)
	if e.holds(b) {
		digest := e.digest
		memos.Put(m)
		return digest
	}
	// Hash the entry's own copy of the input, so b never reaches the hash
	// state's interface and a caller's stack buffer stays on the stack.
	e.length = uint8(len(b))
	copy(e.key[:], b)
	key := e.key[:len(b)]
	if h == nil {
		p := pooled()
		e.digest = p.hashDirect(key)
		hashers.Put(p)
	} else {
		e.digest = h.hashDirect(key)
	}
	digest := e.digest
	memos.Put(m)
	return digest
}
