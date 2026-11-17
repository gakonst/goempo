package state

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/storage"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/u256"
)

type StorageProof struct {
	Key   Hash
	Value u256.Int
	Proof [][]byte
}

type AccountProof struct {
	Address      Address
	Exists       bool
	Balance      u256.Int
	Nonce        uint64
	CodeHash     Hash
	StorageRoot  Hash
	AccountProof [][]byte
	Storage      []StorageProof
}

func (db *DB) Proof(addr Address, keys []Hash) (AccountProof, error) {
	out := AccountProof{Address: addr}
	if err := db.settlePending(); err != nil {
		return AccountProof{}, err
	}
	hashed := keccak.Sum256(addr[:])
	proof, err := db.accounts.Prove(hashed[:])
	if err != nil {
		return AccountProof{}, err
	}
	out.AccountProof = proof
	raw, err := db.store.Get(accountKey(addr))
	switch {
	case err == nil:
		fa, derr := decodeAccount(raw)
		if derr != nil {
			return AccountProof{}, derr
		}
		out.Exists = true
		out.Balance, out.Nonce, out.CodeHash, out.StorageRoot = fa.balance, fa.nonce, fa.codeHash, fa.storageRoot
	case errors.Is(err, storage.ErrNotFound):
		out.Storage = make([]StorageProof, len(keys))
		for i, k := range keys {
			out.Storage[i] = StorageProof{Key: k, Value: u256.Zero()}
		}
		return out, nil
	default:
		return AccountProof{}, err
	}
	st := db.storageTries[addr]
	if st == nil || st.Hash() != out.StorageRoot {
		st = trie.Open(out.StorageRoot, nodeReader{store: db.store, prefix: storageNodePrefix(addr)})
	}
	out.Storage = make([]StorageProof, len(keys))
	for i, k := range keys {
		hk := keccak.Sum256(k[:])
		p, err := st.Prove(hk[:])
		if err != nil {
			return AccountProof{}, err
		}
		v, err := db.readSlot(addr, k)
		if err != nil {
			return AccountProof{}, err
		}
		out.Storage[i] = StorageProof{Key: k, Value: v, Proof: p}
	}
	return out, nil
}
