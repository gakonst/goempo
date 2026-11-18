package precompiles

import (
	"encoding/binary"
	"sync/atomic"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/kapetan-io/tiger/assert"
)

// The keccak memo caches keccak256 over short inputs (at most 64 bytes):
// storage slot derivations. A TIP-20 transfer derives the same balance,
// reward and policy slots several times, and every transaction derives the
// fee manager's, nonce manager's and policy registry's slots afresh; before
// the memo those hashes were a third of precompile time.
//
// The table is direct-mapped and keyed by the whole input, so a hit returns
// exactly what hashing returns: the memo changes cost, never results. It is
// allocated once, at package init, and shared by every goroutine. Each entry
// is a seqlock over atomic words: a reader that races a writer sees an odd
// or changed sequence and hashes directly, so no reader ever returns a torn
// entry, and no access takes a lock.

const (
	memoInputMax   = 64
	memoInputWords = memoInputMax / 8
	memoOutWords   = keccak.Size / 8
	memoBits       = 16
	memoEntries    = 1 << memoBits
)

type memoEntry struct {
	seq atomic.Uint64
	// size is the input's length plus one; zero marks an empty entry.
	size atomic.Uint64
	in   [memoInputWords]atomic.Uint64
	out  [memoOutWords]atomic.Uint64
}

var memoTable = make([]memoEntry, memoEntries)

type memoKey struct {
	words [memoInputWords]uint64
	size  uint64
}

func memoKeyOf(data []byte) memoKey {
	assert.Ok(len(data) <= memoInputMax, "memo input fits")
	var buf [memoInputMax]byte
	copy(buf[:], data)
	var k memoKey
	for i := 0; i < memoInputWords; i++ {
		k.words[i] = binary.LittleEndian.Uint64(buf[8*i:])
	}
	k.size = uint64(len(data)) + 1
	return k
}

// index folds the key's words with distinct odd multipliers; slot keys are
// mostly hashes and addresses, already uniform, and small integer keys are
// spread by the multiply.
func (k *memoKey) index() uint64 {
	h := k.size * 0x9e3779b97f4a7c15
	h ^= k.words[0] * 0xbf58476d1ce4e5b9
	h ^= k.words[1] * 0x94d049bb133111eb
	h ^= k.words[2] * 0xd6e8feb86659fd93
	h ^= k.words[3] * 0xa0761d6478bd642f
	h ^= k.words[4] * 0xe7037ed1a0b428db
	h ^= k.words[5] * 0x8ebc6af09c88c6e3
	h ^= k.words[6] * 0x589965cc75374cc3
	h ^= k.words[7] * 0x1d8e4e27c47d124f
	h ^= h >> 29
	h *= 0x9e3779b97f4a7c15
	return h >> (64 - memoBits)
}

func (e *memoEntry) lookup(k *memoKey) (Hash, bool) {
	seq := e.seq.Load()
	if seq&1 != 0 || e.size.Load() != k.size {
		return Hash{}, false
	}
	for i := 0; i < memoInputWords; i++ {
		if e.in[i].Load() != k.words[i] {
			return Hash{}, false
		}
	}
	var out Hash
	for i := 0; i < memoOutWords; i++ {
		binary.LittleEndian.PutUint64(out[8*i:], e.out[i].Load())
	}
	if e.seq.Load() != seq {
		return Hash{}, false
	}
	return out, true
}

func (e *memoEntry) store(k *memoKey, out Hash) {
	seq := e.seq.Load()
	if seq&1 != 0 || !e.seq.CompareAndSwap(seq, seq+1) {
		return
	}
	e.size.Store(k.size)
	for i := 0; i < memoInputWords; i++ {
		e.in[i].Store(k.words[i])
	}
	for i := 0; i < memoOutWords; i++ {
		e.out[i].Store(binary.LittleEndian.Uint64(out[8*i:]))
	}
	e.seq.Store(seq + 2)
}

// memoKeccak returns keccak256(data), from the memo for short inputs.
func memoKeccak(data []byte) Hash {
	if len(data) == 0 || len(data) > memoInputMax {
		return keccak.Sum256(data)
	}
	k := memoKeyOf(data)
	e := &memoTable[k.index()]
	if out, ok := e.lookup(&k); ok {
		return out
	}
	out := keccak.Sum256(data)
	e.store(&k, out)
	return out
}
