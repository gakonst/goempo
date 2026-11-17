package evm

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const frameStepsMax = 1 << 40

const _ = uint64(frameStepsMax - CallDepthLimit)

const instructionsMax = 1 << 40

const _ = uint64(instructionsMax - CallDepthLimit)

type Context struct {
	State   State
	Block   *BlockContext
	Tx      *TxContext
	Config  *Config
	Tracer  Tracer
	Storage StorageHook
}

type StorageWrite struct {
	Address Address
	Key     Hash
	Current u256.Int
	Value   u256.Int
	Gas     uint64
}

type StorageHook interface {
	CrossZero(w StorageWrite) (uint64, bool)
}

type EVM struct {
	State    State
	Block    *BlockContext
	Tx       *TxContext
	Config   *Config
	gas      *GasTable
	table    *JumpTable
	frames   []*Frame
	top      int
	hasher   *keccak.Hasher
	analysis map[Hash][]uint64
	tracer   Tracer
	storage  StorageHook
}

func New(ctx Context) *EVM {
	assert.Ok(ctx.State != nil, "evm needs a state")
	assert.Ok(ctx.Config != nil, "evm needs a config")
	assert.Ok(ctx.Block != nil, "evm needs a block context")
	assert.Ok(ctx.Tx != nil, "evm needs a tx context")
	return &EVM{
		State:    ctx.State,
		Block:    ctx.Block,
		Tx:       ctx.Tx,
		Config:   ctx.Config,
		gas:      ctx.Config.Gas,
		table:    ctx.Config.Table,
		top:      -1,
		hasher:   keccak.New(),
		analysis: make(map[Hash][]uint64),
		tracer:   tracerOf(ctx),
		storage:  ctx.Storage,
	}
}

func (e *EVM) SetTx(tx *TxContext) {
	assert.Ok(tx != nil, "tx context")
	e.Tx = tx
}

type Message struct {
	Kind   CallKind
	Caller Address
	To     Address
	Value  u256.Int
	Input  []byte
	Gas    uint64
	Salt   u256.Int
}

type Result struct {
	Status  Status
	GasLeft uint64
	Output  []byte
	Address Address
}

type frameResult struct {
	status  Status
	gasLeft uint64
	output  []byte
	address Address
}

type callRequest struct {
	kind        CallKind
	caller      Address
	address     Address
	codeAddress Address
	codeSource  Address
	value       u256.Int
	salt        u256.Int
	input       []byte
	gas         uint64
	depth       int
	static      bool
}

func (e *EVM) Execute(msg Message) Result {
	assert.Equal(e.top, -1, "Execute runs one message at a time")
	req := callRequest{
		kind:        msg.Kind,
		caller:      msg.Caller,
		address:     msg.To,
		codeAddress: msg.To,
		codeSource:  msg.To,
		value:       msg.Value,
		salt:        msg.Salt,
		input:       msg.Input,
		gas:         msg.Gas,
		static:      msg.Kind == KindStaticCall,
	}
	if !msg.Kind.IsCreate() {
		req.codeSource = e.resolveDelegation(msg.To)
	}
	res, pushed := e.dispatch(req)
	if pushed && e.tracer != nil {
		res = e.runTraced()
	} else if pushed {
		res = e.run()
	}
	assert.Equal(e.top, -1, "every frame finished")
	return Result{Status: res.status, GasLeft: res.gasLeft, Output: res.output, Address: res.address}
}

func (e *EVM) resolveDelegation(addr Address) Address {
	if e.Config.Fork < Prague {
		return addr
	}
	target, ok := DelegationTarget(e.State.Code(addr))
	if !ok {
		return addr
	}
	if !e.State.AddressWarm(target) {
		e.State.WarmAddress(target)
	}
	return target
}

func (e *EVM) run() frameResult {
	for iter := 0; iter < frameStepsMax; iter++ {
		f := e.frames[e.top]
		status := e.interpret(f)
		if status == StatusCall {
			continue
		}
		res := e.finish(f, status)
		e.top--
		if e.top < 0 {
			return res
		}
		e.deliver(e.frames[e.top], res)
	}
	assert.Fail("evm run reached frameStepsMax")
	return frameResult{}
}

func (e *EVM) interpret(f *Frame) Status {
	table := e.table
	code := f.code
	for steps := 0; steps < instructionsMax; steps++ {
		op := STOP
		if f.pc < len(code) {
			op = OpCode(code[f.pc])
		}
		o := &table[op]
		if !o.Defined {
			return StatusInvalidOpcode
		}
		if f.sp < o.In {
			return StatusStackUnderflow
		}
		need := f.sp - o.In + o.Out
		if need > StackLimit {
			return StatusStackOverflow
		}
		if need > len(f.stack) {
			f.growStack(need)
		}
		if f.gas < o.Gas {
			return StatusOutOfGas
		}
		f.gas -= o.Gas
		f.pc++
		status := o.Exec(e, f)
		if status != StatusContinue {
			return status
		}
	}
	assert.Fail("evm interpret reached instructionsMax")
	return StatusOutOfGas
}

func (e *EVM) pushFrame() *Frame {
	e.top++
	if e.top == len(e.frames) {
		e.frames = append(e.frames, new(Frame))
	}
	f := e.frames[e.top]
	f.reset()
	return f
}

func (e *EVM) dispatch(req callRequest) (frameResult, bool) {
	if e.tracer != nil {
		return e.dispatchTraced(req)
	}
	if req.kind.IsCreate() {
		return e.dispatchCreate(req)
	}
	return e.dispatchCall(req)
}

func (e *EVM) dispatchCall(req callRequest) (frameResult, bool) {
	if req.depth > CallDepthLimit {
		return frameResult{status: StatusDepthExceeded, gasLeft: req.gas}, false
	}
	transfers := req.kind == KindCall || req.kind == KindCallCode
	if transfers && !req.value.IsZero() {
		if e.State.Balance(req.caller).Lt(req.value) {
			return frameResult{status: StatusInsufficientBalance, gasLeft: req.gas}, false
		}
	}
	snapshot := e.State.Snapshot()
	if req.kind == KindCall {
		e.transfer(req)
	}
	precompile, isPrecompile := e.Config.Precompiles.Lookup(req.codeAddress)
	if isPrecompile {
		res := e.runPrecompile(precompile, req)
		if !res.status.Success() {
			e.State.RevertToSnapshot(snapshot)
		}
		return res, false
	}
	code := e.State.Code(req.codeSource)
	if len(code) == 0 {
		return frameResult{status: StatusStop, gasLeft: req.gas}, false
	}
	f := e.pushFrame()
	e.fillFrame(f, req)
	f.code = code
	f.jumpdests = e.jumpdestsFor(req.codeSource, code)
	f.input = req.input
	f.snapshot = snapshot
	return frameResult{}, true
}

func (e *EVM) transfer(req callRequest) {
	if req.value.IsZero() {
		e.State.Touch(req.address)
		return
	}
	e.State.SubBalance(req.caller, req.value)
	e.State.AddBalance(req.address, req.value)
}

func (e *EVM) fillFrame(f *Frame, req callRequest) {
	f.kind = req.kind
	f.caller = req.caller
	f.address = req.address
	f.codeAddress = req.codeAddress
	f.value = req.value
	f.gas = req.gas
	f.depth = req.depth
	f.static = req.static
}

func (e *EVM) jumpdestsFor(source Address, code []byte) []uint64 {
	hash := e.State.CodeHash(source)
	bits, ok := e.analysis[hash]
	if ok {
		return bits
	}
	bits = analyzeJumpdests(code)
	e.analysis[hash] = bits
	return bits
}

func (e *EVM) dispatchCreate(req callRequest) (frameResult, bool) {
	if req.depth > CallDepthLimit {
		return frameResult{status: StatusDepthExceeded, gasLeft: req.gas}, false
	}
	if e.State.Balance(req.caller).Lt(req.value) {
		return frameResult{status: StatusInsufficientBalance, gasLeft: req.gas}, false
	}
	nonce := e.State.Nonce(req.caller)
	if nonce == ^uint64(0) {
		return frameResult{status: StatusNonceOverflow, gasLeft: req.gas}, false
	}
	e.State.SetNonce(req.caller, nonce+1)
	addr := e.createAddress(req, nonce)
	if !e.State.AddressWarm(addr) {
		e.State.WarmAddress(addr)
	}
	if e.collides(addr) {
		return frameResult{status: StatusCreateCollision}, false
	}
	snapshot := e.State.Snapshot()
	e.State.CreateAccount(addr)
	e.State.SetNonce(addr, 1)
	if !req.value.IsZero() {
		e.State.SubBalance(req.caller, req.value)
		e.State.AddBalance(addr, req.value)
	}
	req.address = addr
	req.codeAddress = addr
	f := e.pushFrame()
	e.fillFrame(f, req)
	f.code = req.input
	f.jumpdests = analyzeJumpdests(req.input)
	f.input = nil
	f.snapshot = snapshot
	return frameResult{}, true
}

func (e *EVM) collides(addr Address) bool {
	if e.State.Nonce(addr) != 0 {
		return true
	}
	if e.State.CodeSize(addr) != 0 {
		return true
	}
	return e.State.HasStorage(addr)
}

func (e *EVM) createAddress(req callRequest, nonce uint64) Address {
	if req.kind == KindCreate2 {
		return Create2Address(req.caller, Create2Input{Salt: req.salt.Bytes32(), CodeHash: e.hasher.Hash(req.input)})
	}
	return CreateAddress(req.caller, nonce)
}

type Create2Input struct {
	Salt     [32]byte
	CodeHash Hash
}

func Create2Address(caller Address, in Create2Input) Address {
	var buf [1 + 20 + 32 + 32]byte
	buf[0] = 0xff
	copy(buf[1:21], caller[:])
	copy(buf[21:53], in.Salt[:])
	copy(buf[53:], in.CodeHash[:])
	sum := keccak.Sum256(buf[:])
	return Address(sum[12:])
}

func (e *EVM) finish(f *Frame, status Status) frameResult {
	res := frameResult{status: status, gasLeft: f.gas, output: f.output}
	if status == StatusSelfDestruct {
		res.status = StatusStop
	}
	if f.kind.IsCreate() {
		res.address = f.address
		if res.status.Success() {
			res = e.depositCode(f, res)
		}
	}
	if res.status.Halted() {
		e.State.RevertToSnapshot(f.snapshot)
		res.gasLeft = 0
		res.output = nil
	}
	if res.status == StatusRevert {
		e.State.RevertToSnapshot(f.snapshot)
	}
	return res
}

func (e *EVM) depositCode(f *Frame, res frameResult) frameResult {
	code := res.output
	if len(code) > e.gas.MaxCodeSize {
		res.status = StatusCodeSizeExceeded
		return res
	}
	if len(code) > 0 && code[0] == 0xef && e.Config.Fork >= London {
		res.status = StatusInvalidCodePrefix
		return res
	}
	cost := uint64(len(code)) * e.gas.CodeDepositByte
	if res.gasLeft < cost {
		res.status = StatusOutOfGas
		return res
	}
	res.gasLeft -= cost
	e.State.SetCode(f.address, code)
	res.output = nil
	return res
}

func (e *EVM) deliver(parent *Frame, res frameResult) {
	parent.gas += res.gasLeft
	if parent.pending.kind.IsCreate() {
		parent.returnData = nil
		if res.status.Success() {
			parent.Push(u256.FromBytes(res.address[:]))
			return
		}
		if res.status == StatusRevert {
			parent.returnData = res.output
		}
		parent.Push(u256.Zero())
		return
	}
	parent.returnData = res.output
	size := min(uint64(len(res.output)), parent.pending.retSize)
	if size > 0 {
		offset := parent.pending.retOffset
		copy(parent.memory[offset:offset+size], res.output)
	}
	if res.status.Success() {
		parent.Push(u256.One())
		return
	}
	parent.Push(u256.Zero())
}

func (e *EVM) runPrecompile(p Precompile, req callRequest) frameResult {
	out := p.Run(&PrecompileCall{
		EVM:         e,
		Kind:        req.kind,
		Caller:      req.caller,
		Address:     req.address,
		CodeAddress: req.codeAddress,
		Value:       req.value,
		Input:       req.input,
		Gas:         req.gas,
		Static:      req.static,
	})
	assert.Ok(out.GasUsed <= req.gas, "precompile used more gas than it had")
	res := frameResult{status: out.Status, gasLeft: req.gas - out.GasUsed, output: out.Output}
	if res.status.Halted() {
		res.gasLeft = 0
		res.output = nil
	}
	return res
}

func analyzeJumpdests(code []byte) []uint64 {
	bits := make([]uint64, (len(code)+63)/64)
	skip := 0
	for pc := 0; pc < len(code); pc++ {
		if skip > 0 {
			skip--
			continue
		}
		op := OpCode(code[pc])
		if op == JUMPDEST {
			bits[pc/64] |= 1 << (uint(pc) % 64)
		}
		if op >= PUSH1 && op <= PUSH32 {
			skip = int(op-PUSH1) + 1
		}
	}
	return bits
}

func validJumpdest(f *Frame, dest u256.Int) bool {
	if !dest.IsUint64() {
		return false
	}
	d := dest.Uint64()
	if d >= uint64(len(f.code)) {
		return false
	}
	return f.jumpdests[d/64]&(1<<(d%64)) != 0
}

const delegationLength = 23

func DelegationTarget(code []byte) (Address, bool) {
	if len(code) != delegationLength {
		return Address{}, false
	}
	if code[0] != 0xef || code[1] != 0x01 || code[2] != 0x00 {
		return Address{}, false
	}
	return Address(code[3:]), true
}

func DelegationCode(target Address) []byte {
	code := make([]byte, 0, delegationLength)
	code = append(code, 0xef, 0x01, 0x00)
	return append(code, target[:]...)
}

func CreateAddress(caller Address, nonce uint64) Address {
	var buf [1 + 21 + 9]byte
	payload := buf[1:1]
	payload = append(payload, 0x80+20)
	payload = append(payload, caller[:]...)
	if nonce == 0 {
		payload = append(payload, 0x80)
	} else if nonce < 0x80 {
		payload = append(payload, byte(nonce))
	} else {
		var nb [8]byte
		n := 0
		for v := nonce; v > 0 && n < 8; v >>= 8 {
			n++
		}
		for i := 0; i < n; i++ {
			nb[i] = byte(nonce >> (8 * uint(n-1-i)))
		}
		payload = append(payload, byte(0x80+n))
		payload = append(payload, nb[:n]...)
	}
	buf[0] = byte(0xc0 + len(payload))
	sum := keccak.Sum256(buf[:1+len(payload)])
	return Address(sum[12:])
}
