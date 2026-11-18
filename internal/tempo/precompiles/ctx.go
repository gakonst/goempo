package precompiles

import (
	"errors"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type Address = primitives.Address
type Hash = primitives.Hash
type Word = u256.Int

const (
	gasWarmRead         uint64 = 100
	gasColdSloadExtra   uint64 = 2_000
	gasColdAccountExtra uint64 = 2_500
	gasColdSstore       uint64 = 2_100
	gasSstoreReset      uint64 = 2_800
	gasSstoreSetPreT1   uint64 = 19_900
	gasSstoreSetT1      uint64 = 250_000
	gasSstoreSetT7      uint64 = 5_000
	gasCallStipend      uint64 = 2_300
	gasStorageCredit    uint64 = 245_000
	gasLog              uint64 = 375
	gasLogTopic         uint64 = 375
	gasLogByte          uint64 = 8
	gasKeccak           uint64 = 30
	gasKeccakWord       uint64 = 6
	gasInputWordPreT11  uint64 = 6
	gasInputWordT11     uint64 = 30
	gasCodeDepositPreT1 uint64 = 200
	gasCodeDepositT1    uint64 = 1_000
	refundClearPreT7    int64  = 4_800
	refundSetPreT7      int64  = 19_900
	refundSetT7         int64  = 5_000
	refundReset         int64  = 2_800
)

const _ = uint64(gasSstoreSetT1 - gasStorageCredit - gasSstoreSetT7)

var ErrOutOfGas = errOutOfGas

var (
	errOutOfGas = errors.New("precompile: out of gas")
	errFatal    = errors.New("precompile: fatal")
	errStatic   = errors.New("precompile: state change in static call")
)

type Revert struct {
	Data []byte
}

func (r *Revert) Error() string { return "precompile: revert" }

func revert(data []byte) error { return &Revert{Data: data} }

type TxEnv struct {
	FeePayer      Address
	FeeToken      Address
	NonCreditable []SlotRef
	CreditOwners  []Address
}

type SlotRef struct {
	Address Address
	Key     Hash
}

type Context struct {
	State              evm.State
	Fork               hardfork.Fork
	Block              *evm.BlockContext
	Tx                 *evm.TxContext
	Env                *TxEnv
	Caller             Address
	Static             bool
	SkipStorageCredits bool
	KeyAuthGas         bool
	Trace              func(op string, addr Address, key Hash, gasUsed uint64)
	ChainID            Word
	self               Address
	gasLimit           uint64
	gasUsed            uint64
	refund             int64
	creditMinting      bool
}

// contextAlloc holds a Context and its default block, transaction and
// environment together, so NewContext is one allocation, not four.
type contextAlloc struct {
	ctx   Context
	block evm.BlockContext
	tx    evm.TxContext
	env   TxEnv
}

func NewContext(state evm.State, fork hardfork.Fork, gas uint64) *Context {
	assert.Ok(state != nil, "precompile context needs a state")
	a := &contextAlloc{}
	a.ctx = Context{
		State:         state,
		Fork:          fork,
		Block:         &a.block,
		Tx:            &a.tx,
		Env:           &a.env,
		gasLimit:      gas,
		creditMinting: true,
	}
	return &a.ctx
}

func (c *Context) GasUsed() uint64 { return c.gasUsed }
func (c *Context) Refund() int64   { return c.refund }
func (c *Context) GasLeft() uint64 { return c.gasLimit - c.gasUsed }

func (c *Context) at(f hardfork.Fork) bool { return c.Fork >= f }

func (c *Context) outOfGas() error {
	return errOutOfGas
}

func (c *Context) cost(gas uint64) uint64 {
	if c.KeyAuthGas {
		return 0
	}
	return gas
}

func (c *Context) charge(gas uint64) error {
	if c.GasLeft() < gas {
		return c.outOfGas()
	}
	c.gasUsed += gas
	return nil
}

func (c *Context) ensureMutable() error {
	if c.Static {
		return errStatic
	}
	return nil
}

func (c *Context) keccak(data []byte) Hash { return memoKeccak(data) }

func (c *Context) chargeKeccak(n int) error {
	words := (uint64(n) + 31) / 32
	return c.charge(c.cost(gasKeccak + gasKeccakWord*words))
}

func (c *Context) warmSlot(addr Address, s slot) bool {
	warm := c.State.SlotWarm(addr, Hash(s))
	c.warmKnown(addr, s, warm)
	return warm
}

// warmKnown warms addr and, when it was cold, the slot; warm is what
// SlotWarm returned for it, so a warm slot costs no second lookup.
func (c *Context) warmKnown(addr Address, s slot, warm bool) {
	c.State.WarmAddress(addr)
	if !warm {
		c.State.WarmSlot(addr, Hash(s))
	}
}

func (c *Context) sload(addr Address, s slot) (Word, error) {
	warm := c.State.SlotWarm(addr, Hash(s))
	if c.at(hardfork.T4) {
		if err := c.charge(gasWarmRead); err != nil {
			return Word{}, err
		}
		if !warm && c.GasLeft() < c.cost(gasColdSloadExtra) {
			return Word{}, c.outOfGas()
		}
	}
	c.warmKnown(addr, s, warm)
	cold := !warm
	value := c.State.Storage(addr, Hash(s))
	if c.Trace != nil {
		c.Trace("sload", addr, Hash(s), c.gasUsed)
	}
	if !c.at(hardfork.T4) {
		if err := c.charge(gasWarmRead); err != nil {
			return Word{}, err
		}
	}
	if cold {
		if err := c.charge(c.cost(gasColdSloadExtra)); err != nil {
			return Word{}, err
		}
	}
	return value, nil
}

type sstoreValues struct {
	original Word
	present  Word
	next     Word
	cold     bool
}

func (v sstoreValues) crossesZero() bool { return v.present.IsZero() != v.next.IsZero() }

func (v sstoreValues) cleanUpdate() bool {
	if v.next == v.present {
		return false
	}
	return v.original == v.present
}

func (c *Context) setCost() uint64 {
	if c.at(hardfork.T7) {
		return gasSstoreSetT7
	}
	if c.at(hardfork.T1) {
		return gasSstoreSetT1
	}
	return gasSstoreSetPreT1
}

func (c *Context) dynamicSstoreGas(v sstoreValues) uint64 {
	gas := uint64(0)
	if v.cold {
		gas += c.cost(gasColdSstore)
	}
	if !v.cleanUpdate() {
		return gas
	}
	if v.original.IsZero() {
		return gas + c.setCost()
	}
	return gas + c.cost(gasSstoreReset)
}

func (c *Context) sstoreRefund(v sstoreValues) int64 {
	clearing := refundClearPreT7
	setRefund := refundSetPreT7
	if c.at(hardfork.T7) {
		clearing = 0
		setRefund = refundSetT7
	}
	if v.next == v.present {
		return 0
	}
	if v.original == v.present {
		if v.next.IsZero() {
			return clearing
		}
		return 0
	}
	refund := int64(0)
	if !v.original.IsZero() {
		if v.present.IsZero() {
			refund -= clearing
		} else if v.next.IsZero() {
			refund += clearing
		}
	}
	if v.original != v.next {
		return refund
	}
	if v.original.IsZero() {
		return refund + setRefund
	}
	return refund + refundReset
}

func (c *Context) journalWrite(addr Address, s slot, value Word) sstoreValues {
	cold := !c.warmSlot(addr, s)
	v := sstoreValues{
		original: c.State.OriginalStorage(addr, Hash(s)),
		present:  c.State.Storage(addr, Hash(s)),
		next:     value,
		cold:     cold,
	}
	c.State.SetStorage(addr, Hash(s), value)
	return v
}

func (c *Context) sstore(addr Address, s slot, value Word) error {
	if err := c.ensureMutable(); err != nil {
		return err
	}
	if c.at(hardfork.T12) && c.GasLeft() <= gasCallStipend {
		return c.outOfGas()
	}
	if c.at(hardfork.T4) {
		if err := c.charge(c.cost(gasWarmRead)); err != nil {
			return err
		}
		if !c.State.SlotWarm(addr, Hash(s)) && c.GasLeft() < gasColdSloadExtra {
			return c.outOfGas()
		}
	}
	v := c.journalWrite(addr, s, value)
	if c.Trace != nil {
		c.Trace("sstore", addr, Hash(s), c.gasUsed)
	}
	if !c.at(hardfork.T4) {
		if err := c.charge(c.cost(gasWarmRead)); err != nil {
			return err
		}
	}
	if c.at(hardfork.T7) && !c.SkipStorageCredits {
		if err := c.storageCredits(addr, s, v); err != nil {
			return err
		}
	}
	if err := c.charge(c.dynamicSstoreGas(v)); err != nil {
		return err
	}
	if c.at(hardfork.T4) {
		c.refund += c.sstoreRefund(v)
	}
	return nil
}

func (c *Context) tload(addr Address, s slot) (Word, error) {
	if err := c.charge(gasWarmRead); err != nil {
		return Word{}, err
	}
	return c.State.TransientStorage(addr, Hash(s)), nil
}

func (c *Context) tstore(addr Address, s slot, value Word) error {
	if err := c.ensureMutable(); err != nil {
		return err
	}
	if err := c.charge(gasWarmRead); err != nil {
		return err
	}
	c.State.SetTransientStorage(addr, Hash(s), value)
	return nil
}

func (c *Context) emit(addr Address, topics []Hash, data []byte) error {
	if err := c.ensureMutable(); err != nil {
		return err
	}
	cost := gasLog + c.cost(gasLogTopic*uint64(len(topics))+gasLogByte*uint64(len(data)))
	if err := c.charge(cost); err != nil {
		return err
	}
	c.State.AddLog(evm.Log{Address: addr, Topics: topics, Data: data})
	return nil
}

func (c *Context) accountCode(addr Address) ([]byte, error) {
	if c.at(hardfork.T4) {
		if err := c.charge(gasWarmRead); err != nil {
			return nil, err
		}
		if !c.State.AddressWarm(addr) && c.GasLeft() < c.cost(gasColdAccountExtra) {
			return nil, c.outOfGas()
		}
	}
	cold := !c.State.AddressWarm(addr)
	c.State.WarmAddress(addr)
	if !c.at(hardfork.T4) {
		if err := c.charge(gasWarmRead); err != nil {
			return nil, err
		}
	}
	if cold {
		if err := c.charge(c.cost(gasColdAccountExtra)); err != nil {
			return nil, err
		}
	}
	return c.State.Code(addr), nil
}

func (c *Context) setCode(addr Address, code []byte) error {
	if err := c.ensureMutable(); err != nil {
		return err
	}
	perByte := gasCodeDepositPreT1
	if c.at(hardfork.T1) {
		perByte = gasCodeDepositT1
	}
	if err := c.charge(perByte * uint64(len(code))); err != nil {
		return err
	}
	c.State.WarmAddress(addr)
	c.State.SetCode(addr, code)
	return nil
}

func (c *Context) isNonCreditable(addr Address, s slot) bool {
	for _, ref := range c.Env.NonCreditable {
		if ref.Address == addr && ref.Key == Hash(s) {
			return true
		}
	}
	return false
}
