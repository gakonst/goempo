package state_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

func TestProofVerifiesAgainstRoot(t *testing.T) {
	db, err := state.Open(goio.NewMemFS(), state.Options{})
	assert.NoError(err, "open")
	st := db.State()
	var holder primitives.Address
	for i := range 40 {
		a := primitives.Address{byte(i + 1)}
		st.AddBalance(a, u256.FromUint64(uint64(1000+i)))
		st.SetNonce(a, uint64(i))
		if i == 7 {
			holder = a
			st.SetCode(a, []byte{0x60, 0x00})
			for k := range 30 {
				st.SetStorage(a, primitives.Hash{31: byte(k + 1)}, u256.FromUint64(uint64(k+5)))
			}
		}
	}
	root, err := st.Commit(1)
	assert.NoError(err, "commit")
	p, err := db.Proof(holder, []primitives.Hash{{31: 3}, {31: 200}})
	assert.NoError(err, "proof")
	hashed := keccak.Sum256(holder[:])
	leaf, err := trie.VerifyProof(root, hashed[:], p.AccountProof)
	assert.NoError(err, "account proof verifies")
	enc := rlp.NewEncoder(128)
	bal := p.Balance.Bytes32()
	enc.ListStart()
	enc.WriteUint64(p.Nonce)
	enc.WriteUintBytes(bal[:])
	enc.WriteBytes(p.StorageRoot[:])
	enc.WriteBytes(p.CodeHash[:])
	enc.ListEnd()
	assert.Equal(string(leaf), string(enc.Encoded()), "leaf is the account's RLP")
	assert.Equal(p.Nonce, uint64(7), "nonce")
	k := keccak.Sum256(p.Storage[0].Key[:])
	v, err := trie.VerifyProof(p.StorageRoot, k[:], p.Storage[0].Proof)
	assert.NoError(err, "storage proof verifies")
	assert.Equal(len(v), 1, "slot value rlp")
	assert.Equal(p.Storage[0].Value, u256.FromUint64(7), "slot value")
	k = keccak.Sum256(p.Storage[1].Key[:])
	v, err = trie.VerifyProof(p.StorageRoot, k[:], p.Storage[1].Proof)
	assert.NoError(err, "absent slot proof verifies")
	assert.Equal(len(v), 0, "absent slot")
	missing, err := db.Proof(primitives.Address{0xee}, nil)
	assert.NoError(err, "absent account")
	h := keccak.Sum256(missing.Address[:])
	v, err = trie.VerifyProof(root, h[:], missing.AccountProof)
	assert.NoError(err, "absence proof verifies")
	assert.Equal(len(v), 0, "absent account")
}
