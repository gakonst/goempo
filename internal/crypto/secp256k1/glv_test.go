package secp256k1_test

import (
	"math/rand/v2"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/kapetan-io/tiger/assert"
)

func TestRecoverRandomKeys(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 12))
	for i := 0; i < 400; i++ {
		var key secp256k1.PrivateKey
		for j := range key {
			key[j] = byte(r.Uint32())
		}
		key[0] &= 0x7f
		if i%7 == 0 {
			clear(key[:16])
		}
		want, err := key.Address()
		assert.Ok(err == nil, "address of a random key")
		var msg [8]byte
		for j := range msg {
			msg[j] = byte(r.Uint32())
		}
		hash := keccak.Sum256(msg[:])
		sig, err := secp256k1.Sign(hash, key)
		assert.Ok(err == nil, "sign with a random key")
		got, err := secp256k1.RecoverAddress(hash, sig)
		if err != nil || got != want {
			t.Fatalf("key %d: recovered %s (%v), want %s", i, got.Hex(), err, want.Hex())
		}
	}
}
