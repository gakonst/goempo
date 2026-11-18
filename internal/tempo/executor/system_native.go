package executor

import (
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

// nativeSystemCall applies call natively when it is a system-address call
// into the canonical history storage or beacon roots contract, with the
// same state effects as the interpreter: the callee is touched and its ring
// buffer slots are written. It reports false, changing nothing, otherwise.
func nativeSystemCall(st State, blk *evm.BlockContext, call systemCall) bool {
	if call.caller != chain.SystemAddress || len(call.input) != 32 || call.gas < nativeCallGasMin {
		return false
	}
	value := u256.FromBytes32([32]byte(call.input))
	switch call.to {
	case chain.HistoryStorageAddress:
		// The contract keys by (number - 1) mod 8191 in 256-bit arithmetic;
		// block 0 never runs, so leave it to the interpreter.
		if blk.Number == 0 || st.CodeHash(call.to) != historyStorageCodeHash {
			return false
		}
		st.Touch(call.to)
		st.SetStorage(call.to, slotHash((blk.Number-1)%ringSlots), value)
		return true
	case BeaconRootsAddress:
		if st.CodeHash(call.to) != beaconRootsCodeHash {
			return false
		}
		index := blk.Time % ringSlots
		st.Touch(call.to)
		st.SetStorage(call.to, slotHash(index), u256.FromUint64(blk.Time))
		st.SetStorage(call.to, slotHash(index+ringSlots), value)
		return true
	default:
		return false
	}
}

func slotHash(v uint64) Hash {
	return Hash(u256.FromUint64(v).Bytes32())
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
