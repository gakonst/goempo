package evm_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

var (
	sender   = primitives.Address{0x10}
	contract = primitives.Address{0x20}
	other    = primitives.Address{0x30}
)

type program struct {
	code  []byte
	gas   uint64
	input []byte
	extra []deployed
}

type deployed struct {
	addr primitives.Address
	code []byte
}

type run struct {
	state  *memstate.State
	result evm.Result
}

func execute(t *testing.T, p program) run {
	t.Helper()
	st := memstate.New()
	st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40)})
	st.Put(contract, memstate.Account{Code: p.code, Nonce: 1})
	for i := 0; i < len(p.extra); i++ {
		st.Put(p.extra[i].addr, memstate.Account{Code: p.extra[i].code, Nonce: 1})
	}
	st.WarmAddress(sender)
	st.WarmAddress(contract)
	machine := evm.New(evm.Context{
		State:  st,
		Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
		Tx:     &evm.TxContext{Origin: sender},
		Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
	})
	res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Input: p.input, Gas: p.gas})
	return run{state: st, result: res}
}

func slot(n uint64) primitives.Hash {
	return primitives.Uint64ToHash(n)
}

func TestAddAndStore(t *testing.T) {
	code := []byte{0x60, 0x02, 0x60, 0x03, 0x01, 0x60, 0x00, 0x55, 0x00}
	r := execute(t, program{code: code, gas: 100_000})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	assert.Equal(r.state.Storage(contract, slot(0)), u256.FromUint64(5), "stored sum")
	assert.Equal(100_000-r.result.GasLeft, uint64(3+3+3+3+2100+20000), "gas used")
}

func TestRevertRestoresStorageAndKeepsGas(t *testing.T) {
	code := []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x60, 0x00, 0x60, 0x00, 0xfd}
	r := execute(t, program{code: code, gas: 100_000})
	assert.Equal(r.result.Status, evm.StatusRevert, "status")
	assert.Ok(r.state.Storage(contract, slot(0)).IsZero(), "storage reverted")
	assert.Equal(100_000-r.result.GasLeft, uint64(3+3+22100+3+3), "revert keeps unused gas")
}

func TestInvalidJumpConsumesAllGas(t *testing.T) {
	code := []byte{0x60, 0x05, 0x56, 0x00, 0x00, 0x00}
	r := execute(t, program{code: code, gas: 50_000})
	assert.Equal(r.result.Status, evm.StatusInvalidJump, "status")
	assert.Equal(r.result.GasLeft, uint64(0), "halt consumes gas")
}

func TestStackUnderflow(t *testing.T) {
	r := execute(t, program{code: []byte{0x01}, gas: 50_000})
	assert.Equal(r.result.Status, evm.StatusStackUnderflow, "status")
}

func TestCallReturnsData(t *testing.T) {
	callee := []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}
	caller := []byte{
		0x60, 0x20, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00,
		0x73, 0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0x61, 0xff, 0xff, 0xf1,
		0x60, 0x00, 0x51, 0x60, 0x01, 0x55,
		0x60, 0x00, 0x55, 0x00,
	}
	r := execute(t, program{code: caller, gas: 200_000, extra: []deployed{{addr: other, code: callee}}})
	assert.Equal(r.result.Status, evm.StatusStop, "status")
	assert.Equal(r.state.Storage(contract, slot(1)), u256.FromUint64(42), "return data copied to memory")
	assert.Equal(r.state.Storage(contract, slot(0)), u256.One(), "call succeeded")
}

func TestCreateAddress(t *testing.T) {
	creator, err := primitives.HexToAddress("0x6ac7ea33f8831ea9dcc53393aaa88b25a785dbf0")
	assert.NoError(err, "address")
	cases := []struct {
		name  string
		nonce uint64
		want  string
	}{
		{name: "nonce 0", nonce: 0, want: "0xcd234a471b72ba2f1ccf0a70fcaba648a5eecd8d"},
		{name: "nonce 1", nonce: 1, want: "0x343c43a37d37dff08ae8c4a11544c718abb4fcf8"},
		{name: "nonce 2", nonce: 2, want: "0xf778b86fa74e846c4f0a1fbd1335fe81c00a0c91"},
	}
	for i := 0; i < len(cases); i++ {
		got := evm.CreateAddress(creator, cases[i].nonce)
		assert.Equal(got.Hex(), cases[i].want, cases[i].name)
	}
}

func TestDelegationDesignator(t *testing.T) {
	code := evm.DelegationCode(other)
	target, ok := evm.DelegationTarget(code)
	assert.Ok(ok, "designator parses")
	assert.Equal(target, other, "target")
	_, ok = evm.DelegationTarget(code[:22])
	assert.Ok(!ok, "short code is not a designator")
}

func TestOpcodeNames(t *testing.T) {
	assert.Equal(evm.PUSH1.String(), "PUSH1", "push1")
	assert.Equal((evm.PUSH1 + 31).String(), "PUSH32", "push32")
	assert.Equal((evm.DUP1 + 15).String(), "DUP16", "dup16")
	assert.Equal((evm.LOG0 + 4).String(), "LOG4", "log4")
	assert.Equal(evm.OpCode(0x0c).String(), "0x0c", "undefined")
}

func TestMemoryCost(t *testing.T) {
	g := evm.OsakaGas()
	assert.Equal(g.MemoryCost(1), uint64(3), "one word")
	assert.Equal(g.MemoryCost(1024), uint64(3*1024+1024*1024/512), "quadratic")
}
