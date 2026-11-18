package executor

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

var BeaconRootsAddress = Address{
	0x00, 0x0f, 0x3d, 0xf6, 0xd7, 0x32, 0x80, 0x7e, 0xf1, 0x31,
	0x9f, 0xb7, 0xb8, 0xbb, 0x85, 0x22, 0xd0, 0xbe, 0xac, 0x02,
}

type systemEnv struct {
	config *evm.Config
	block  *evm.BlockContext
	header *types.Header
	rules  chain.Rules
}

type systemCall struct {
	caller Address
	to     Address
	input  []byte
	gas    uint64
}

// preBlock runs the block's pre-transaction system steps. The steps applied
// natively (history and beacon roots writes, precompile and zone deploys)
// depend on no transaction boundary between them, so their writes share one
// pending transaction: preBlock reports whether it left writes for the
// caller to finalize (see finishPreBlock).
func preBlock(st State, env *systemEnv) bool {
	pending := historyCall(st, env, systemCall{
		caller: chain.SystemAddress,
		to:     chain.HistoryStorageAddress,
		input:  env.header.Inner.ParentHash[:],
		gas:    env.rules.SystemCallGas(),
	}, false)
	if !env.rules.AtLeast(hardfork.T13) {
		pending = historyCall(st, env, systemCall{
			caller: chain.SystemAddress,
			to:     BeaconRootsAddress,
			input:  env.header.Inner.ParentBeaconBlockRoot[:],
			gas:    env.rules.SystemCallGas(),
		}, pending)
	}
	pending = deployBoundaryPrecompiles(st, env.rules) || pending
	pending = deployZones(st, env.rules) || pending
	return pending
}

// finishPreBlock runs preBlock and finalizes its writes.
func finishPreBlock(st State, env *systemEnv) {
	if preBlock(st, env) {
		st.FinalizeTx()
	}
}

// historyCall applies one system call into a history contract. A native
// call leaves its writes pending and reports true; an interpreted one runs
// as its own transaction, after finalizing what is pending.
func historyCall(st State, env *systemEnv, call systemCall, pending bool) bool {
	if len(st.Code(call.to)) == 0 {
		return pending
	}
	if nativeSystemCall(st, env.block, call) {
		return true
	}
	if pending {
		st.FinalizeTx()
	}
	run(st, env.config, env.block, call)
	st.FinalizeTx()
	return false
}

func run(st State, cfg *evm.Config, blk *evm.BlockContext, call systemCall) evm.Result {
	st.WarmAddress(call.to)
	machine := borrowMachine(evm.Context{
		State:  st,
		Block:  blk,
		Config: cfg,
	}, evm.TxContext{Origin: call.caller})
	defer returnMachine(machine)
	return machine.Execute(evm.Message{
		Kind:   evm.KindCall,
		Caller: call.caller,
		To:     call.to,
		Input:  call.input,
		Gas:    call.gas,
	})
}

func IsSystemTx(tx *types.Transaction) bool {
	legacy, ok := tx.Data.(*types.LegacyTx)
	if !ok {
		return false
	}
	if legacy.To == nil {
		return false
	}
	to, r, s := legacy.To, &legacy.R, &legacy.S
	// Word loads instead of three memequal calls: this runs on every
	// transaction.
	return binary.LittleEndian.Uint64(to[0:])|binary.LittleEndian.Uint64(to[8:])|
		uint64(binary.LittleEndian.Uint32(to[16:]))|
		binary.LittleEndian.Uint64(r[0:])|binary.LittleEndian.Uint64(r[8:])|
		binary.LittleEndian.Uint64(r[16:])|binary.LittleEndian.Uint64(r[24:])|
		binary.LittleEndian.Uint64(s[0:])|binary.LittleEndian.Uint64(s[8:])|
		binary.LittleEndian.Uint64(s[16:])|binary.LittleEndian.Uint64(s[24:]) == 0
}

func applySystemTx(env *TxEnv) (TxOutcome, error) {
	legacy, ok := env.Tx.Data.(*types.LegacyTx)
	assert.Ok(ok, "a system transaction is legacy")
	call := systemCall{
		caller: Address{},
		to:     Address{},
		input:  legacy.Data,
		gas:    env.Rules.TempoSystemCallGas(),
	}
	// The system transaction calls the zero address, which holds no code
	// and is no precompile: the call stops at once and only touches it.
	if emptyCall(env.State, env.Config, call.to) {
		env.State.Touch(call.to)
		env.State.FinalizeTx()
		return TxOutcome{GasUsed: 0, Success: true}, nil
	}
	res := run(env.State, env.Config, env.Block, call)
	env.State.FinalizeTx()
	if !res.Status.Success() {
		return TxOutcome{}, ErrSystemTx
	}
	return TxOutcome{GasUsed: 0, Success: true}, nil
}
