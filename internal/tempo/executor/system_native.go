package executor

import (
	"encoding/binary"
	"encoding/hex"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

// The canonical EIP-2935 history storage and EIP-4788 beacon roots
// contracts. Called by the system address, each runs a fixed list of SSTOREs
// and stops, so a system call into an account holding exactly this code is
// applied natively instead of through the interpreter.
const (
	historyStorageCodeHex = "3373fffffffffffffffffffffffffffffffffffffffe14604657602036036042575f35600143038111604257611fff81430311604257611fff9006545f5260205ff35b5f5ffd5b5f35611fff60014303065500"
	beaconRootsCodeHex    = "3373fffffffffffffffffffffffffffffffffffffffe14604d57602036146024575f5ffd5b5f35801560495762001fff810690815414603c575f5ffd5b62001fff01545f5260205ff35b5f5ffd5b62001fff42064281555f359062001fff015500"
)

// ringSlots is the length of both contracts' ring buffers.
const ringSlots = 8191

// nativeCallGasMin is the least gas a system call needs for the native path:
// the interpreter path's SSTOREs need far less than this, so with at least
// this much neither path runs out of gas.
const nativeCallGasMin = 1_000_000

var (
	historyStorageCode     = mustHex(historyStorageCodeHex)
	beaconRootsCode        = mustHex(beaconRootsCodeHex)
	historyStorageCodeHash = keccak.Sum256(historyStorageCode)
	beaconRootsCodeHash    = keccak.Sum256(beaconRootsCode)
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "system contract code is valid hex")
	return b
}

// nativeResult says how nativeSystemCall applied a call.
type nativeResult uint8

const (
	// nativeNo: the call needs the interpreter; nothing changed.
	nativeNo nativeResult = iota
	// nativeFinal: the writes are final, with nothing left to finalize.
	nativeFinal
	// nativePending: the writes are pending in the current transaction.
	nativePending
)

// finalWriter is a State that writes a slot between transactions without a
// journal (state.State).
type finalWriter interface {
	SetStorageNow(addr Address, codeHash Hash, keys []Hash, values []u256.Int) bool
}

// nativeSystemCall applies call natively when it is a system-address call
// into the canonical history storage or beacon roots contract, with the
// same state effects as the interpreter: its ring buffer slots are written.
// The interpreter also touches the callee, which changes nothing here: it
// holds code, so it is not empty and EIP-161 never deletes it.
// pending says whether st holds writes of the current transaction already.
func nativeSystemCall(st State, blk *evm.BlockContext, call systemCall, pending bool) nativeResult {
	if call.caller != chain.SystemAddress || len(call.input) != 32 || call.gas < nativeCallGasMin {
		return nativeNo
	}
	value := u256.FromBytes32([32]byte(call.input))
	var slots [2]Hash
	var values [2]u256.Int
	var codeHash Hash
	n := 0
	switch call.to {
	case chain.HistoryStorageAddress:
		// The contract keys by (number - 1) mod 8191 in 256-bit arithmetic;
		// block 0 never runs, so leave it to the interpreter.
		if blk.Number == 0 {
			return nativeNo
		}
		codeHash = historyStorageCodeHash
		slots[0], values[0] = slotHash((blk.Number-1)%ringSlots), value
		n = 1
	case BeaconRootsAddress:
		codeHash = beaconRootsCodeHash
		index := blk.Time % ringSlots
		slots[0], values[0] = slotHash(index), u256.FromUint64(blk.Time)
		slots[1], values[1] = slotHash(index+ringSlots), value
		n = 2
	default:
		return nativeNo
	}
	assert.Ok(n > 0 && n <= len(slots), "a native system call writes one or two slots")
	if fw, ok := st.(finalWriter); ok && !pending {
		// One account lookup checks the code and writes the slots.
		if !fw.SetStorageNow(call.to, codeHash, slots[:n], values[:n]) {
			return nativeNo
		}
		return nativeFinal
	}
	if st.CodeHash(call.to) != codeHash {
		return nativeNo
	}
	for i := 0; i < n; i++ {
		st.SetStorage(call.to, slots[i], values[i])
	}
	return nativePending
}

// slotHash is v as a big-endian storage key.
func slotHash(v uint64) Hash {
	var h Hash
	binary.BigEndian.PutUint64(h[len(h)-8:], v)
	return h
}

// emptyZeroCall reports whether the system transaction's call into the zero
// address stops at once; zeroIsPrecompile is whether the block's precompile
// set holds the zero address.
func emptyZeroCall(st State, zeroIsPrecompile bool) bool {
	return !zeroIsPrecompile && len(st.Code(Address{})) == 0
}

// emptyCall reports whether a value-free call to to would stop at once with
// no effect beyond touching to: to is neither a precompile nor holds code (so
// it delegates nowhere either).
func emptyCall(st State, cfg *evm.Config, to Address) bool {
	assert.Ok(cfg.Precompiles != nil, "a block's EVM config has precompiles")
	if _, ok := cfg.Precompiles.Lookup(to); ok {
		return false
	}
	return len(st.Code(to)) == 0
}
