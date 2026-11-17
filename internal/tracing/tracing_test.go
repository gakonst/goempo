package tracing_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tracing"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

var (
	sender   = primitives.Address{0x10}
	contract = primitives.Address{0x20}
	callee   = primitives.Address{0x30}
)

var callerCode = []byte{
	0x60, 0x20, 0x60, 0x00, 0x60, 0x04, 0x60, 0x1c, 0x60, 0x00,
	0x73, 0x30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0x61, 0xff, 0xff, 0xf1,
	0x60, 0x00, 0x51, 0x60, 0x01, 0x55,
	0x60, 0x00, 0x60, 0x00, 0xa0,
	0x00,
}

var calleeCode = []byte{0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3}

type tracedRun struct {
	trace *tracing.TxTrace
	res   evm.Result
}

func runTraced(t *testing.T, opts tracing.StepOptions) tracedRun {
	st := memstate.New()
	st.Put(sender, memstate.Account{Balance: u256.FromUint64(1 << 40), Nonce: 7})
	st.Put(contract, memstate.Account{Code: callerCode, Nonce: 1, Storage: map[primitives.Hash]u256.Int{{31: 1}: u256.FromUint64(5)}})
	st.Put(callee, memstate.Account{Code: calleeCode, Nonce: 1})
	session := tracing.NewSession(st, []evm.Address{sender}, opts)
	machine := evm.New(evm.Context{
		State:  session,
		Block:  &evm.BlockContext{Number: 10, GasLimit: 30_000_000},
		Tx:     &evm.TxContext{Origin: sender},
		Config: evm.NewConfig(evm.Osaka, u256.FromUint64(1)),
	})
	res := machine.Execute(evm.Message{Kind: evm.KindCall, Caller: sender, To: contract, Gas: 100_000})
	session.FinalizeTx()
	tr := session.Traces()[0]
	assert.Ok(tr != nil, "transaction traced")
	return tracedRun{trace: tr, res: res}
}

func decode(t *testing.T, b []byte) map[string]any {
	var out map[string]any
	assert.NoError(json.Unmarshal(b, &out), "valid json")
	return out
}

func TestCallTracerTree(t *testing.T) {
	run := runTraced(t, tracing.StepOptions{})
	tr, res := run.trace, run.res
	assert.Equal(res.Status, evm.StatusStop, "status")
	info := tracing.TxInfo{Sender: sender, GasLimit: 120_000, GasUsed: 50_000, Success: true}
	frame := decode(t, tracing.CallJSON(tr.Recorder, info, tracing.CallConfig{WithLog: true}))
	same(frame["gas"], "0x1d4c0", "root gas is the tx gas limit")
	same(frame["gasUsed"], "0xc350", "root gas used is the receipt's")
	same(frame["type"], "CALL", "root type")
	calls := frame["calls"].([]any)
	assert.Equal(len(calls), 1, "one child call")
	child := calls[0].(map[string]any)
	same(child["to"], expect(callee.Hex()), "child target")
	same(child["output"], "0x000000000000000000000000000000000000000000000000000000000000002a", "child output")
	logs := frame["logs"].([]any)
	assert.Equal(len(logs), 1, "root log")
	same(logs[0].(map[string]any)["position"], "0x1", "log after the call")
	top := decode(t, tracing.CallJSON(tr.Recorder, info, tracing.CallConfig{OnlyTopCall: true}))
	_, has := top["calls"]
	assert.Equal(has, false, "onlyTopCall drops children")
}

func TestSyntheticRootForTempoTx(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{}).trace
	info := tracing.TxInfo{AA: true, Sender: sender, GasLimit: 90_000, GasUsed: 60_000, Success: true}
	frame := decode(t, tracing.CallJSON(tr.Recorder, info, tracing.CallConfig{}))
	same(frame["to"], expect(primitives.Address{}.Hex()), "synthetic root targets zero")
	same(frame["input"], "0x", "synthetic root input")
	assert.Equal(len(frame["calls"].([]any)), 1, "each top level call is a child")
	logs := decode(t, tracing.StructJSON(tr.Recorder, info, tracing.StepOptions{}))
	same(logs["failed"], "true", "the reference marks tempo transactions failed")
	assert.Equal(len(logs["structLogs"].([]any)), 0, "no struct logs for tempo transactions")
	var parity []map[string]any
	assert.NoError(json.Unmarshal(tracing.ParityJSON(tr.Recorder, info, tracing.Locator{}), &parity), "parity json")
	assert.Equal(len(parity), 3, "synthetic root, call and child")
	same(parity[0]["subtraces"], "1", "synthetic root subtraces")
	same(parity[2]["traceAddress"], "[0 0]", "grandchild address")
}

func TestStructLogger(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{Enabled: true, Stack: true, Storage: true, Memory: true}).trace
	info := tracing.TxInfo{Sender: sender, GasUsed: 40_000, Success: true}
	out := decode(t, tracing.StructJSON(tr.Recorder, info, tracing.StepOptions{Enabled: true, Stack: true, Storage: true, Memory: true}))
	logs := out["structLogs"].([]any)
	assert.Equal(len(logs), 22, "caller and callee opcodes in execution order")
	first := logs[0].(map[string]any)
	same(first["op"], "PUSH1", "first op")
	same(first["depth"], "1", "depth starts at one")
	call := logs[7].(map[string]any)
	same(call["op"], "CALL", "call op")
	inner := logs[8].(map[string]any)
	same(inner["depth"], "2", "callee runs one level deeper")
	var sstore map[string]any
	for _, l := range logs {
		if m := l.(map[string]any); m["op"] == "SSTORE" {
			sstore = m
		}
	}
	assert.Ok(sstore != nil, "sstore logged")
	storage := sstore["storage"].(map[string]any)
	same(storage[strings.Repeat("0", 63)+"1"], expect(strings.Repeat("0", 62)+"2a"), "stored value")
	same(out["failed"], "false", "succeeded")
}

func TestPrestateAndDiff(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{}).trace
	pre := decode(t, tracing.PrestateJSON(tr))
	c := pre[contract.Hex()].(map[string]any)
	slot := primitives.Hash{31: 1}
	same(c["storage"].(map[string]any)[slot.Hex()], "0x0000000000000000000000000000000000000000000000000000000000000005", "original slot value")
	s := pre[sender.Hex()].(map[string]any)
	same(s["nonce"], "7", "sender nonce")
	diff := decode(t, tracing.PrestateDiffJSON(tr))
	post := diff["post"].(map[string]any)[contract.Hex()].(map[string]any)
	same(post["storage"].(map[string]any)[slot.Hex()], "0x000000000000000000000000000000000000000000000000000000000000002a", "changed slot")
	_, calleeChanged := diff["post"].(map[string]any)[callee.Hex()]
	assert.Equal(calleeChanged, false, "unchanged accounts are dropped")
}

func TestFourByte(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{}).trace
	out := decode(t, tracing.FourByteJSON(tr.Recorder))
	assert.Equal(len(out), 1, "one call carries a selector")
}

func TestRevertReason(t *testing.T) {
	msg := []byte{0x08, 0xc3, 0x79, 0xa0}
	word := func(n byte) []byte { b := make([]byte, 32); b[31] = n; return b }
	msg = append(msg, word(32)...)
	msg = append(msg, word(2)...)
	msg = append(msg, append([]byte("hi"), make([]byte, 30)...)...)
	got, ok := tracing.RevertReason(msg)
	assert.Equal(ok, true, "decoded")
	assert.Equal(got, "hi", "reason")
	panicOut := append([]byte{0x4e, 0x48, 0x7b, 0x71}, word(0x11)...)
	got, ok = tracing.RevertReason(panicOut)
	assert.Equal(ok, true, "panic decoded")
	assert.Equal(got, "arithmetic underflow or overflow", "panic text")
	_, ok = tracing.RevertReason([]byte{1, 2})
	assert.Equal(ok, false, "short output")
}

type expect string

func same(got any, want expect, msg string) {
	assert.Equal(fmt.Sprint(got), string(want), msg)
}

func TestAccessList(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{Enabled: true, Stack: true}).trace
	list := tracing.AccessListOf(tr.Recorder, tracing.AccessSpec{
		Sender:   sender,
		Excluded: func(evm.Address) bool { return false },
		Initial:  []tracing.AccessTuple{{Address: sender, Keys: []evm.Hash{{1}}}},
	})
	assert.Equal(len(list), 2, "contract slots and the callee")
	assert.Equal(list[0].Address, contract, "contract first")
	assert.Equal(len(list[0].Keys), 1, "stored slot kept even though the callee of the transaction is excluded")
	assert.Equal(list[1].Address, callee, "called address")
	assert.Equal(len(list[1].Keys), 0, "no callee slots")
}
