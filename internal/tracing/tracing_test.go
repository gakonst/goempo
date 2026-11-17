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

func TestStateDiff(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{}).trace
	out := decode(t, tracing.StateDiffJSON(tr))
	c := out[contract.Hex()].(map[string]any)
	same(c["balance"], "=", "unchanged balance")
	slot := primitives.Hash{31: 1}
	change := c["storage"].(map[string]any)[slot.Hex()].(map[string]any)["*"].(map[string]any)
	same(change["from"], "0x0000000000000000000000000000000000000000000000000000000000000005", "slot before")
	same(change["to"], "0x000000000000000000000000000000000000000000000000000000000000002a", "slot after")
	_, touched := out[callee.Hex()]
	assert.Equal(touched, false, "unchanged accounts omitted")
}

func TestFlatTraces(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{}).trace
	flats := tracing.FlatTraces(tr.Recorder, tracing.TxInfo{})
	assert.Equal(len(flats), 2, "root and call")
	assert.Equal(flats[0].Subtraces, 1, "root subtraces")
	assert.Equal(flats[1].To, callee, "child target")
	plain := decode(t, tracing.FlatJSON(tr.Recorder, &flats[1], nil))
	_, hasBlock := plain["blockHash"]
	assert.Equal(hasBlock, false, "plain traces have no locator")
	loc := tracing.Locator{BlockNumber: 9, TxIndex: 2}
	localized := decode(t, tracing.FlatJSON(tr.Recorder, &flats[1], &loc))
	same(localized["blockNumber"], "9", "localized block")
	same(localized["transactionPosition"], "2", "localized index")
}

func TestOpcodeGas(t *testing.T) {
	tr := runTraced(t, tracing.StepOptions{Enabled: true}).trace
	var list []struct {
		Opcode  string `json:"opcode"`
		Count   int    `json:"count"`
		GasUsed uint64 `json:"gasUsed"`
	}
	assert.NoError(json.Unmarshal(tracing.OpcodeGasJSON(tr.Recorder), &list), "valid json")
	counts := map[string]int{}
	gas := map[string]uint64{}
	for _, e := range list {
		_, dup := counts[e.Opcode]
		assert.Ok(!dup, "each opcode listed once")
		counts[e.Opcode] = e.Count
		gas[e.Opcode] = e.GasUsed
	}
	assert.Equal(list[0].Opcode, "PUSH1", "first executed opcode comes first")
	assert.Equal(counts["PUSH1"], 13, "push1 across caller and callee")
	assert.Equal(gas["PUSH1"], uint64(39), "three gas per push1")
	assert.Equal(counts["CALL"], 1, "one call")
	assert.Equal(counts["RETURN"], 1, "callee returns once")
	total := 0
	for _, c := range counts {
		total += c
	}
	assert.Equal(total, 22, "every step counted once")
}

func TestBlockAccessListSystemOnly(t *testing.T) {
	system := []evm.Address{
		{0x00, 0x00, 0x09, 0x61, 0xef, 0x48, 0x0e, 0xb5, 0x5e, 0x80, 0xd1, 0x9a, 0xd8, 0x35, 0x79, 0xa6, 0x4c, 0x00, 0x70, 0x02},
		{0x00, 0x00, 0xbb, 0xdd, 0xc7, 0xce, 0x48, 0x86, 0x42, 0xfb, 0x57, 0x9f, 0x8b, 0x00, 0xf3, 0xa5, 0x90, 0x00, 0x72, 0x51},
		{0x00, 0x00, 0xf9, 0x08, 0x27, 0xf1, 0xc5, 0x3a, 0x10, 0xcb, 0x7a, 0x02, 0x33, 0x5b, 0x17, 0x53, 0x20, 0x00, 0x29, 0x35},
		{0x00, 0x0f, 0x3d, 0xf6, 0xd7, 0x32, 0x80, 0x7e, 0xf1, 0x31, 0x9f, 0xb7, 0xb8, 0xbb, 0x85, 0x22, 0xd0, 0xbe, 0xac, 0x02},
	}
	bal := tracing.BuildAccessList(nil, append([]evm.Address{{}}, system...))
	want := "0xf887da940000000000000000000000000000000000000000c0c0c0c0c0da9400000961ef480eb55e80d19ad83579a64c007002c0c0c0c0c0da940000bbddc7ce488642fb579f8b00f3a590007251c0c0c0c0c0da940000f90827f1c53a10cb7a02335b175320002935c0c0c0c0c0da94000f3df6d732807ef1319fb7b8bb8522d0beac02c0c0c0c0c0"
	assert.Equal(fmt.Sprintf("0x%x", bal.RLP()), want, "mainnet block 2 eth_getBlockAccessListRaw")
}

func TestBlockAccessListChangesAndReads(t *testing.T) {
	addr := evm.Address{0x42}
	read, wrote, same := evm.Hash{31: 1}, evm.Hash{31: 2}, evm.Hash{31: 3}
	acc := &tracing.Account{
		Address:     addr,
		Slots:       []evm.Hash{read, wrote, same},
		Pre:         map[evm.Hash]u256.Int{wrote: u256.FromUint64(1), same: u256.FromUint64(5)},
		Post:        map[evm.Hash]u256.Int{wrote: u256.FromUint64(9), same: u256.FromUint64(5)},
		Balance:     u256.FromUint64(10),
		PostBalance: u256.FromUint64(7),
		Nonce:       1,
		PostNonce:   2,
	}
	bal := tracing.BuildAccessList([]tracing.IndexedTrace{{Index: 1, Trace: &tracing.TxTrace{Accounts: []*tracing.Account{acc}}}}, nil)
	got := string(bal.JSON())
	want := `[{"address":"0x4200000000000000000000000000000000000000","balanceChanges":[{"index":"0x1","value":"0x7"}],"codeChanges":[],"nonceChanges":[{"index":"0x1","value":"0x2"}],"storageChanges":[{"changes":[{"index":"0x1","value":"0x9"}],"key":"0x2"}],"storageReads":["0x1","0x3"]}]`
	assert.Equal(got, want, "written slot is a change, unchanged slots are reads")
}
