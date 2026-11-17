package evm

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

type Address = primitives.Address

type Hash = primitives.Hash

type Log struct {
	Address Address
	Topics  []Hash
	Data    []byte
}

type State interface {
	Exist(addr Address) bool
	Empty(addr Address) bool
	CreateAccount(addr Address)
	CreatedInTx(addr Address) bool
	Touch(addr Address)

	Balance(addr Address) u256.Int
	AddBalance(addr Address, amount u256.Int)
	SubBalance(addr Address, amount u256.Int)

	Nonce(addr Address) uint64
	SetNonce(addr Address, nonce uint64)

	Code(addr Address) []byte
	CodeHash(addr Address) Hash
	CodeSize(addr Address) int
	SetCode(addr Address, code []byte)

	Storage(addr Address, key Hash) u256.Int
	OriginalStorage(addr Address, key Hash) u256.Int
	SetStorage(addr Address, key Hash, value u256.Int)
	HasStorage(addr Address) bool

	TransientStorage(addr Address, key Hash) u256.Int
	SetTransientStorage(addr Address, key Hash, value u256.Int)

	SelfDestruct(addr Address)
	HasSelfDestructed(addr Address) bool

	AddressWarm(addr Address) bool
	WarmAddress(addr Address)
	SlotWarm(addr Address, key Hash) bool
	WarmSlot(addr Address, key Hash)

	AddRefund(delta int64)
	Refund() int64

	AddLog(log Log)

	Snapshot() int
	RevertToSnapshot(id int)
}

type BlockContext struct {
	Coinbase    Address
	Number      uint64
	Time        uint64
	GasLimit    uint64
	BaseFee     u256.Int
	BlobBaseFee u256.Int
	PrevRandao  Hash
	GetHash     func(number uint64) Hash
}

type TxContext struct {
	Origin     Address
	GasPrice   u256.Int
	BlobHashes []Hash
}
