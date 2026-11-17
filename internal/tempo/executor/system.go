package executor

import (
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
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

func preBlock(st State, env *systemEnv) {
	parent := env.header.Inner.ParentHash
	historyCall(st, env, systemCall{
		caller: chain.SystemAddress,
		to:     chain.HistoryStorageAddress,
		input:  parent[:],
		gas:    env.rules.SystemCallGas(),
	})
	if !env.rules.AtLeast(hardfork.T13) {
		root := env.header.Inner.ParentBeaconBlockRoot
		historyCall(st, env, systemCall{
			caller: chain.SystemAddress,
			to:     BeaconRootsAddress,
			input:  root[:],
			gas:    env.rules.SystemCallGas(),
		})
	}
	deployBoundaryPrecompiles(st, env.rules)
}

func historyCall(st State, env *systemEnv, call systemCall) {
	if len(st.Code(call.to)) == 0 {
		return
	}
	run(st, env.config, env.block, call)
	st.FinalizeTx()
}

func run(st State, cfg *evm.Config, blk *evm.BlockContext, call systemCall) evm.Result {
	st.WarmAddress(call.to)
	machine := evm.New(evm.Context{
		State:  st,
		Block:  blk,
		Tx:     &evm.TxContext{Origin: call.caller},
		Config: cfg,
	})
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
	if *legacy.To != (primitives.Address{}) {
		return false
	}
	return legacy.R == [len(legacy.R)]byte{} && legacy.S == [len(legacy.S)]byte{}
}

func applySystemTx(env *TxEnv) (TxOutcome, error) {
	legacy, ok := env.Tx.Data.(*types.LegacyTx)
	assert.Ok(ok, "a system transaction is legacy")
	res := run(env.State, env.Config, env.Block, systemCall{
		caller: Address{},
		to:     Address{},
		input:  legacy.Data,
		gas:    env.Rules.TempoSystemCallGas(),
	})
	env.State.FinalizeTx()
	if !res.Status.Success() {
		return TxOutcome{}, ErrSystemTx
	}
	return TxOutcome{GasUsed: 0, Success: true}, nil
}
