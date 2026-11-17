package evm

import "github.com/gakonst/goempo/internal/u256"

func (e *EVM) GasTable() *GasTable { return e.gas }

func (e *EVM) AccessAccount(addr Address) uint64 { return e.accountAccessCost(addr) }

func (e *EVM) AccessSlot(addr Address, key Hash) uint64 {
	if !e.gas.AccessLists {
		return e.gas.ColdSload
	}
	if e.State.SlotWarm(addr, key) {
		return e.gas.WarmAccess
	}
	e.State.WarmSlot(addr, key)
	return e.gas.ColdSload
}

func WordToAddress(w u256.Int) Address { return wordAddress(w) }

func WordToHash(w u256.Int) Hash { return wordHash(w) }

func AddressToWord(a Address) u256.Int { return addressWord(a) }

func (f *Frame) Code() []byte { return f.code }

func (f *Frame) Input() []byte { return f.input }

func (f *Frame) Value() u256.Int { return f.value }

func (f *Frame) CodeAddress() Address { return f.codeAddress }

func (f *Frame) Kind() CallKind { return f.kind }
