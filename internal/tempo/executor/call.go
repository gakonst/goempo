package executor

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

type Simulation struct {
	Sender           Address
	SkipNonceCheck   bool
	SkipBalanceCheck bool
	SkipGasCap       bool
	SkipFeeCharge    bool
}

func (x *Executor) ApplyCall(st State, blk *types.Block, tx *types.Transaction, sim Simulation) (TxOutcome, error) {
	assert.Ok(blk != nil, "a call needs a block header")
	header := &blk.Header
	rules := chain.RulesAt(x.ChainID, header.Inner.Timestamp)
	set := precompiles.NewSet(rules.Fork, rules.EthereumPrecompiles())
	ts := chain.Timestamp{Seconds: header.Inner.Timestamp, MillisPart: header.TimestampMillisPart}
	env := &TxEnv{
		State:  st,
		Rules:  rules,
		Config: rules.Config(ts.Millis(), set),
		Block:  blockContext(header, func(uint64) primitives.Hash { return primitives.Hash{} }),
		Tx:     tx,
		Sim:    &sim,
	}
	return applyTx(env, set)
}
