package precompiles_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
	"golang.org/x/crypto/sha3"
)

type slotScratchInput struct {
	key  primitives.Hash
	base primitives.Hash
}

func slotScratchOracle(in slotScratchInput) primitives.Hash {
	h := sha3.NewLegacyKeccak256()
	n, err := h.Write(in.key[:])
	assert.NoError(err, "oracle key write")
	assert.Equal(n, len(in.key), "oracle key length")
	n, err = h.Write(in.base[:])
	assert.NoError(err, "oracle base write")
	assert.Equal(n, len(in.base), "oracle base length")
	var out primitives.Hash
	copy(out[:], h.Sum(nil))
	return out
}

func nonceScratchSlot(account primitives.Address, key u256.Int) primitives.Hash {
	var addressWord primitives.Hash
	copy(addressWord[12:], account[:])
	base := slotScratchOracle(slotScratchInput{key: addressWord})
	return slotScratchOracle(slotScratchInput{key: primitives.Hash(key.Bytes32()), base: base})
}

func checkNonceScratch(t *testing.T, c *precompiles.Context, account primitives.Address, key u256.Int, want uint64) {
	t.Helper()
	got, err := precompiles.GetNonce(c, account, key)
	if err != nil || got != want {
		t.Fatalf("nested mapping oracle got %d want %d err %v", got, want, err)
	}
}

func TestMappingScratchReuseKeepsValues(t *testing.T) {
	states := []*memstate.State{memstate.New(), memstate.New()}
	contexts := []*precompiles.Context{
		precompiles.NewContext(states[0], hardfork.T7, 100000000),
		precompiles.NewContext(states[1], hardfork.T7, 100000000),
	}
	for i := 0; i < 256; i++ {
		account := primitives.Address{0: byte(i), 19: byte(255 - i)}
		key := u256.FromUint64(uint64(i + 1))
		nested := nonceScratchSlot(account, key)
		states[i%2].SetStorage(precompiles.NonceManagerAddress, nested, u256.FromUint64(uint64(i+100)))
		checkNonceScratch(t, contexts[i%2], account, key, uint64(i+100))
	}
	for i := 0; i < 256; i++ {
		account := primitives.Address{0: byte(i), 19: byte(255 - i)}
		key := u256.FromUint64(uint64(i + 1))
		checkNonceScratch(t, contexts[i%2], account, key, uint64(i+100))
		checkNonceScratch(t, contexts[(i+1)%2], account, key, 0)
	}
}

func TestMappingScratchIndependentContexts(t *testing.T) {
	for worker := 0; worker < 4; worker++ {
		t.Run(string(rune('A'+worker)), func(t *testing.T) {
			t.Parallel()
			st := memstate.New()
			c := precompiles.NewContext(st, hardfork.T7, 100000000)
			for i := 0; i < 128; i++ {
				account := primitives.Address{0: byte(worker), 19: byte(i)}
				key := u256.FromUint64(uint64(i + 1))
				nested := nonceScratchSlot(account, key)
				st.SetStorage(precompiles.NonceManagerAddress, nested, u256.FromUint64(uint64(i+100)))
				checkNonceScratch(t, c, account, key, uint64(i+100))
			}
		})
	}
}
