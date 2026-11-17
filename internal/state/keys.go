package state

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	prefixHead        = 'h'
	prefixAccount     = 'a'
	prefixSlot        = 's'
	prefixCode        = 'c'
	prefixAccountNode = 'N'
	prefixStorageNode = 'n'
)

const accountBytes = 8 + 32 + 32 + 32

const headBytes = 8 + 32

const pathNibblesMax = 2 * primitives.HashLength

const nodeKeyBytesMax = 1 + primitives.AddressLength + pathNibblesMax/2 + 1

const _ = uint(storage.KeyBytesMax - nodeKeyBytesMax)

const _ = uint(2*trie.KeyBytesMax - pathNibblesMax)

type nibbles []byte

var headKey = []byte{prefixHead}

func accountKey(addr Address) []byte {
	k := make([]byte, 0, 1+primitives.AddressLength)
	k = append(k, prefixAccount)
	return append(k, addr[:]...)
}

func slotKeyBytes(addr Address, slot Hash) []byte {
	k := make([]byte, 0, 1+primitives.AddressLength+primitives.HashLength)
	k = append(k, prefixSlot)
	k = append(k, addr[:]...)
	return append(k, slot[:]...)
}

func slotPrefix(addr Address) []byte {
	k := make([]byte, 0, 1+primitives.AddressLength)
	k = append(k, prefixSlot)
	return append(k, addr[:]...)
}

func codeKey(h Hash) []byte {
	k := make([]byte, 0, 1+primitives.HashLength)
	k = append(k, prefixCode)
	return append(k, h[:]...)
}

func appendPath(dst []byte, path nibbles) []byte {
	assert.Ok(len(path) <= pathNibblesMax, "trie path longer than 64 nibbles")
	for i := 0; i < len(path)/2; i++ {
		dst = append(dst, path[2*i]<<4|path[2*i+1])
	}
	if len(path)%2 == 1 {
		dst = append(dst, path[len(path)-1]<<4)
	}
	return append(dst, byte(len(path)))
}

type flatAccount struct {
	nonce       uint64
	balance     u256.Int
	codeHash    Hash
	storageRoot Hash
}

func (a *flatAccount) encode() []byte {
	out := make([]byte, accountBytes)
	binary.BigEndian.PutUint64(out[0:8], a.nonce)
	a.balance.PutBytes32(out[8:40])
	copy(out[40:72], a.codeHash[:])
	copy(out[72:104], a.storageRoot[:])
	return out
}

func decodeAccount(b []byte) (flatAccount, error) {
	if len(b) != accountBytes {
		return flatAccount{}, ErrCorrupt
	}
	var a flatAccount
	a.nonce = binary.BigEndian.Uint64(b[0:8])
	a.balance = u256.FromBytes(b[8:40])
	copy(a.codeHash[:], b[40:72])
	copy(a.storageRoot[:], b[72:104])
	return a, nil
}

func encodeHead(h Head) []byte {
	out := make([]byte, headBytes)
	binary.BigEndian.PutUint64(out[0:8], h.Number)
	copy(out[8:40], h.Root[:])
	return out
}

func decodeHead(b []byte) (Head, error) {
	if len(b) != headBytes {
		return Head{}, ErrCorrupt
	}
	h := Head{Number: binary.BigEndian.Uint64(b[0:8]), Loaded: true}
	copy(h.Root[:], b[8:40])
	return h, nil
}

func encodeSlot(v u256.Int) []byte {
	b := v.Bytes32()
	return primitives.TrimLeftZeros(b[:])
}

func decodeSlot(b []byte) (u256.Int, error) {
	if len(b) > 32 {
		return u256.Int{}, ErrCorrupt
	}
	return u256.FromBytes(b), nil
}

const keyArenaBytes = 16 << 10

const _ = uint(keyArenaBytes - nodeKeyBytesMax)

type keyArena struct {
	buf []byte
}

func (a *keyArena) alloc(n int) []byte {
	assert.Ok(n > 0, "key arena allocation empty")
	assert.Ok(n <= nodeKeyBytesMax, "key arena allocation longer than nodeKeyBytesMax")
	if cap(a.buf)-len(a.buf) < n {
		a.buf = make([]byte, 0, keyArenaBytes)
	}
	start := len(a.buf)
	a.buf = a.buf[:start+n]
	return a.buf[start : start+n : start+n]
}

func (a *keyArena) accountKey(addr Address) []byte {
	k := a.alloc(1 + primitives.AddressLength)
	k[0] = prefixAccount
	copy(k[1:], addr[:])
	return k
}

func (a *keyArena) slotKey(addr Address, slot Hash) []byte {
	k := a.alloc(1 + primitives.AddressLength + primitives.HashLength)
	k[0] = prefixSlot
	copy(k[1:], addr[:])
	copy(k[1+primitives.AddressLength:], slot[:])
	return k
}

func (a *keyArena) codeKey(h Hash) []byte {
	k := a.alloc(1 + primitives.HashLength)
	k[0] = prefixCode
	copy(k[1:], h[:])
	return k
}
