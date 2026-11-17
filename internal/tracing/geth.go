package tracing

import (
	"bytes"
	"encoding/binary"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type TxInfo struct {
	AA       bool
	Sender   evm.Address
	GasLimit uint64
	GasUsed  uint64
	Success  bool
	Output   []byte
}

type CallConfig struct {
	OnlyTopCall bool
	WithLog     bool
}

type errorText struct {
	geth   string
	parity string
	debug  string
}

var errorTexts = map[evm.Status]errorText{
	evm.StatusRevert:                {"execution reverted", "Reverted", "Revert"},
	evm.StatusOutOfGas:              {"out of gas", "Out of gas", "OutOfGas"},
	evm.StatusInsufficientBalance:   {"insufficient balance for transfer", "Insufficient balance for transfer", "OutOfFunds"},
	evm.StatusInvalidOpcode:         {"invalid opcode", "Bad instruction", "OpcodeNotFound"},
	evm.StatusStackUnderflow:        {"stack underflow", "Stack underflow", "StackUnderflow"},
	evm.StatusStackOverflow:         {"Out of stack", "Out of stack", "StackOverflow"},
	evm.StatusStaticViolation:       {"write protection", "Mutable Call In Static Context", "StateChangeDuringStaticCall"},
	evm.StatusReturnDataOutOfBounds: {"return data out of bounds", "Out of bounds", "OutOfOffset"},
	evm.StatusCodeSizeExceeded:      {"max code size exceeded", "Out of gas", "CreateContractSizeLimit"},
	evm.StatusInitCodeSizeExceeded:  {"max initcode size exceeded", "Out of gas", "CreateInitCodeSizeLimit"},
	evm.StatusInvalidCodePrefix:     {"invalid code: must not begin with 0xef", "Invalid code", "CreateContractStartingWithEF"},
	evm.StatusCreateCollision:       {"contract address collision", "Contract address collision", "CreateCollision"},
	evm.StatusNonceOverflow:         {"nonce uint64 overflow", "Nonce overflow", "NonceOverflow"},
	evm.StatusDepthExceeded:         {"max call depth exceeded", "Max call depth exceeded", "CallTooDeep"},
	evm.StatusInvalidJump:           {"invalid jump destination", "Bad jump destination", "InvalidJump"},
	evm.StatusPrecompileFailure:     {"precompiled failed", "Built-in failed", "PrecompileError"},
}

func success(s evm.Status) bool {
	return s == evm.StatusStop || s == evm.StatusReturn || s == evm.StatusSelfDestruct
}

func halted(s evm.Status) bool {
	return s != evm.StatusContinue && s != evm.StatusCall && s != evm.StatusRevert && !success(s)
}

func errorFor(s evm.Status) (errorText, bool) {
	if success(s) || s == evm.StatusContinue || s == evm.StatusCall {
		return errorText{}, false
	}
	t, ok := errorTexts[s]
	if !ok {
		return errorText{geth: s.String(), parity: s.String(), debug: s.String()}, true
	}
	return t, true
}

var (
	errorSelector = []byte{0x08, 0xc3, 0x79, 0xa0}
	panicSelector = []byte{0x4e, 0x48, 0x7b, 0x71}
)

var panicTexts = map[uint64]string{
	0x00: "generic panic",
	0x01: "assert(false)",
	0x11: "arithmetic underflow or overflow",
	0x12: "division or modulo by zero",
	0x21: "enum overflow",
	0x22: "invalid encoded storage byte array accessed",
	0x31: "out-of-bounds array access; popping on an empty array",
	0x32: "out-of-bounds access of an array or bytesN",
	0x41: "out of memory",
	0x51: "uninitialized function",
}

func RevertReason(out []byte) (string, bool) {
	if len(out) < selectorSize {
		return "", false
	}
	body := out[selectorSize:]
	switch {
	case bytes.Equal(out[:selectorSize], errorSelector):
		s, ok := abiString(body)
		if !ok || len(bytes.Trim([]byte(s), "\x00")) == 0 {
			return "", false
		}
		return s, true
	case bytes.Equal(out[:selectorSize], panicSelector):
		if len(body) != wordBytes {
			return "", false
		}
		code := u256.FromBytes(body)
		if code.IsUint64() {
			if t, ok := panicTexts[code.Uint64()]; ok {
				return t, true
			}
		}
		return "unknown panic code: " + code.Hex(), true
	}
	return "", false
}

func abiString(b []byte) (string, bool) {
	if len(b) < 2*wordBytes {
		return "", false
	}
	off, ok := wordInt(b[:wordBytes])
	if !ok || off > uint64(len(b))-wordBytes {
		return "", false
	}
	n, ok := wordInt(b[off : off+wordBytes])
	start := off + wordBytes
	if !ok || n > uint64(len(b))-start {
		return "", false
	}
	s := b[start : start+n]
	if !utf8.Valid(s) {
		return "", false
	}
	return string(s), true
}

func wordInt(w []byte) (uint64, bool) {
	for _, c := range w[:wordBytes-8] {
		if c != 0 {
			return 0, false
		}
	}
	return binary.BigEndian.Uint64(w[wordBytes-8:]), true
}

func kindName(k evm.CallKind) string {
	switch k {
	case evm.KindCall:
		return "CALL"
	case evm.KindCallCode:
		return "CALLCODE"
	case evm.KindDelegateCall:
		return "DELEGATECALL"
	case evm.KindStaticCall:
		return "STATICCALL"
	case evm.KindCreate:
		return "CREATE"
	case evm.KindCreate2:
		return "CREATE2"
	default:
		assert.Unreachable("CallKind: unhandled value")
	}
	return "CALL"
}

func CallJSON(rec *Recorder, info TxInfo, cfg CallConfig) []byte {
	w := &writer{}
	if info.AA {
		writeSyntheticFrame(w, rec, info, cfg)
		return w.buf
	}
	if len(rec.Roots) == 0 {
		w.raw([]byte("{}"))
		return w.buf
	}
	writeFrames(w, frameJob{rec: rec, cfg: cfg, root: rec.Roots[0], info: &info})
	return w.buf
}

func writeSyntheticFrame(w *writer, rec *Recorder, info TxInfo, cfg CallConfig) {
	w.objOpen()
	w.kAddress("from", info.Sender)
	w.kQuantity("gas", info.GasLimit)
	w.kQuantity("gasUsed", info.GasUsed)
	w.kAddress("to", evm.Address{})
	w.kData("input", nil)
	if !cfg.OnlyTopCall && len(rec.Roots) > 0 {
		w.key("calls")
		w.arrOpen()
		for _, idx := range rec.Roots {
			writeFrames(w, frameJob{rec: rec, cfg: cfg, root: idx})
		}
		w.arrClose()
	}
	w.kQuantity256("value", u256.Int{})
	w.kStr("type", "CALL")
	w.objClose()
}

func (w *writer) kQuantity256(k field, v u256.Int) {
	w.key(k)
	w.quantity256(v)
}

type frameJob struct {
	rec  *Recorder
	cfg  CallConfig
	root int
	info *TxInfo
}

type frameCursor struct {
	idx     int
	next    int
	visible bool
}

func writeFrames(w *writer, job frameJob) {
	rec := job.rec
	stack := []frameCursor{{idx: job.root, next: 0, visible: true}}
	writeFrameHead(w, job, job.root)
	for iter := 0; iter < 2*framesMax && len(stack) > 0; iter++ {
		top := &stack[len(stack)-1]
		n := &rec.Nodes[top.idx]
		if !job.cfg.OnlyTopCall && top.next < len(n.Children) {
			child := n.Children[top.next]
			top.next++
			writeFrameHead(w, job, child)
			stack = append(stack, frameCursor{idx: child, visible: top.visible && n.Success()})
			continue
		}
		writeFrameTail(w, job, frameCursor{idx: top.idx, visible: top.visible && n.Success()})
		stack = stack[:len(stack)-1]
	}
	assert.Ok(len(stack) == 0, "call frames written within bound")
}

func writeFrameHead(w *writer, job frameJob, idx int) {
	n := &job.rec.Nodes[idx]
	ok := n.Success()
	w.objOpen()
	w.kAddress("from", n.From)
	gas, used := n.Gas, n.GasUsed
	if job.info != nil && idx == job.root {
		gas, used = job.info.GasLimit, job.info.GasUsed
	}
	w.kQuantity("gas", gas)
	w.kQuantity("gasUsed", used)
	if ok || !n.Kind.IsCreate() {
		w.kAddress("to", n.To)
	}
	w.kData("input", n.Input)
	if len(n.Output) > 0 {
		w.kData("output", n.Output)
	}
	if !ok {
		if t, bad := errorFor(n.Status); bad {
			w.kStr("error", t.geth)
		}
		if reason, has := RevertReason(n.Output); has {
			w.kStr("revertReason", reason)
		}
	}
	if !job.cfg.OnlyTopCall && len(n.Children) > 0 {
		w.key("calls")
		w.arrOpen()
	}
}

func writeFrameTail(w *writer, job frameJob, cur frameCursor) {
	idx, visible := cur.idx, cur.visible
	n := &job.rec.Nodes[idx]
	if !job.cfg.OnlyTopCall && len(n.Children) > 0 {
		w.arrClose()
	}
	if job.cfg.WithLog && visible && len(n.Logs) > 0 {
		writeFrameLogs(w, n.Logs)
	}
	if n.Kind != evm.KindStaticCall {
		w.kQuantity256("value", n.Value)
	}
	w.kStr("type", kindName(n.Kind))
	w.objClose()
}

func writeFrameLogs(w *writer, logs []Log) {
	w.key("logs")
	w.arrOpen()
	for _, l := range logs {
		w.objOpen()
		w.kAddress("address", l.Log.Address)
		w.key("topics")
		w.arrOpen()
		for _, t := range l.Log.Topics {
			w.hash(t)
		}
		w.arrClose()
		w.kData("data", l.Log.Data)
		w.kQuantity("position", uint64(l.Position))
		w.kQuantity("index", uint64(l.Index))
		w.objClose()
	}
	w.arrClose()
}

func StructJSON(rec *Recorder, info TxInfo, opts StepOptions) []byte {
	w := &writer{}
	w.objOpen()
	w.key("failed")
	w.boolean(jbool(info.AA || !info.Success))
	w.key("gas")
	w.integer(info.GasUsed)
	w.kData("returnValue", returnValue(rec, info))
	w.key("structLogs")
	w.arrOpen()
	if !info.AA {
		job := stepJob{rec: rec, opts: opts, storage: make(map[evm.Address]map[evm.Hash]evm.Hash)}
		for _, idx := range rec.Roots {
			writeSteps(w, job, idx)
		}
	}
	w.arrClose()
	w.objClose()
	return w.buf
}

func TxOutput(rec *Recorder, info TxInfo) []byte { return returnValue(rec, info) }

func returnValue(rec *Recorder, info TxInfo) []byte {
	if info.Output != nil {
		return info.Output
	}
	if len(rec.Roots) == 0 {
		return nil
	}
	return rec.Nodes[rec.Roots[len(rec.Roots)-1]].Output
}

func execAddress(n *Node) evm.Address {
	if delegated(n.Kind) {
		return n.From
	}
	return n.To
}

type stepCursor struct {
	idx  int
	next int
}

type stepJob struct {
	rec     *Recorder
	opts    StepOptions
	storage map[evm.Address]map[evm.Hash]evm.Hash
}

func writeSteps(w *writer, job stepJob, root int) {
	stack := []stepCursor{{idx: root}}
	for iter := 0; iter < stepsMax && len(stack) > 0; iter++ {
		top := &stack[len(stack)-1]
		n := &job.rec.Nodes[top.idx]
		if top.next >= len(n.Steps) {
			stack = stack[:len(stack)-1]
			continue
		}
		s := &n.Steps[top.next]
		top.next++
		writeStep(w, job, n, s)
		if s.Child != noChild {
			stack = append(stack, stepCursor{idx: s.Child})
		}
	}
	assert.Ok(len(stack) == 0, "struct logs written within bound")
}

func writeStep(w *writer, job stepJob, n *Node, s *Step) {
	w.objOpen()
	w.key("pc")
	w.integer(uint64(s.PC))
	w.kStr("op", opName(s.Op))
	w.key("gas")
	w.integer(s.Gas)
	w.key("gasCost")
	w.integer(s.Cost)
	w.key("depth")
	w.integer(uint64(s.Depth + 1))
	if halted(s.Status) {
		t, _ := errorFor(s.Status)
		w.kStr("error", "Some("+t.debug+")")
	}
	if job.opts.Stack {
		w.key("stack")
		w.arrOpen()
		for _, v := range s.Stack {
			w.quantity256(v)
		}
		w.arrClose()
	}
	if job.opts.ReturnData && len(s.ReturnData) > 0 {
		w.kData("returnData", s.ReturnData)
	}
	if job.opts.Memory && len(s.Memory) > 0 {
		w.key("memory")
		w.arrOpen()
		words := len(s.Memory) / wordBytes
		for i := 0; i < words; i++ {
			w.data(s.Memory[i*wordBytes : (i+1)*wordBytes])
		}
		w.arrClose()
	}
	if job.opts.Storage && s.Slot.Set && !halted(s.Status) {
		addr := execAddress(n)
		m := job.storage[addr]
		if m == nil {
			m = make(map[evm.Hash]evm.Hash)
			job.storage[addr] = m
		}
		m[s.Slot.Key] = s.Slot.Value
		writeStorageMap(w, m)
	}
	if s.Refund != 0 {
		w.key("refund")
		w.signed(s.Refund)
	}
	w.objClose()
}

func writeStorageMap(w *writer, m map[evm.Hash]evm.Hash) {
	vals := make(map[evm.Hash]u256.Int, len(m))
	for k, v := range m {
		vals[k] = u256.FromBytes(v[:])
	}
	w.key("storage")
	w.objOpen()
	for _, k := range sortedHashes(vals) {
		w.key(field(hexNoPrefix(k[:])))
		v := m[k]
		w.bareHex(v[:])
	}
	w.objClose()
}

func hexNoPrefix(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

func opName(op evm.OpCode) string {
	s := op.String()
	if len(s) > 2 && s[:2] == "0x" {
		return "opcode " + "0x" + strconv.FormatUint(uint64(op), 16) + " not defined"
	}
	return s
}

func FourByteJSON(rec *Recorder) []byte {
	counts := make(map[string]uint64)
	var order []string
	for i := range rec.Nodes {
		n := &rec.Nodes[i]
		if !countsSelector(n) {
			continue
		}
		k := "0x" + hexNoPrefix(n.Input[:selectorSize]) + "-" + strconv.Itoa(len(n.Input)-selectorSize)
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	slices.Sort(order)
	w := &writer{}
	w.objOpen()
	for _, k := range order {
		w.key(field(k))
		w.integer(counts[k])
	}
	w.objClose()
	return w.buf
}

var ethPrecompiles = evm.EthereumPrecompiles(evm.Osaka)

func isEthPrecompile(a evm.Address) bool {
	_, ok := ethPrecompiles.Lookup(a)
	return ok
}

func countsSelector(n *Node) bool {
	if len(n.Input) < selectorSize {
		return false
	}
	if n.Kind.IsCreate() {
		return false
	}
	return !isEthPrecompile(n.To)
}
