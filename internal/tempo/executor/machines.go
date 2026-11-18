package executor

import (
	"sync"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

// machine is an idle EVM with the transaction context it runs under: it
// keeps its call frames, 1024-word stacks, memory, hasher and jumpdest
// analysis, so a transaction or system call borrows one instead of building
// an interpreter from scratch.
type machine struct {
	*evm.EVM
	tx evm.TxContext
}

var machines sync.Pool

// borrowMachine readies a pooled EVM for ctx under tx; ctx.Tx is ignored.
func borrowMachine(ctx evm.Context, tx evm.TxContext) *machine {
	m, _ := machines.Get().(*machine)
	if m == nil {
		m = &machine{}
	}
	m.tx = tx
	ctx.Tx = &m.tx
	if m.EVM == nil {
		m.EVM = evm.New(ctx)
	} else {
		m.Reset(ctx)
	}
	return m
}

func returnMachine(m *machine) {
	assert.Ok(m != nil && m.EVM != nil, "only borrowed machines return")
	m.Release()
	m.tx = evm.TxContext{}
	machines.Put(m)
}

// blockScratch holds what one block's execution builds before its first
// transaction: the precompile set, EVM config, block context and system
// call environment. Execute borrows one per block; nothing in a Result
// points into it.
type blockScratch struct {
	set    precompiles.Set
	setEnv precompiles.TxEnv
	cfg    evm.Config
	blk    evm.BlockContext
	sys    systemEnv
	env    TxEnv
	// built names what set and cfg were built for: a scratch borrowed again
	// for the same chain cache, chain and fork keeps them instead of looking
	// both up for every block.
	built    bool
	builtFor scratchKey
}

type scratchKey struct {
	configs *chain.Cache
	chainID hardfork.ChainID
	fork    hardfork.Fork
}

var blockScratches = sync.Pool{New: func() any { return new(blockScratch) }}

func (x *Executor) borrowScratch(header *types.Header, getHash func(uint64) Hash) *blockScratch {
	s, ok := blockScratches.Get().(*blockScratch)
	assert.Ok(ok, "the block scratch pool holds scratches")
	rules := chain.RulesAt(x.ChainID, header.Inner.Timestamp)
	s.setEnv = precompiles.TxEnv{}
	key := scratchKey{configs: x.Configs, chainID: x.ChainID, fork: rules.Fork}
	if !s.built || s.builtFor != key {
		s.set = precompiles.Set{Fork: rules.Fork, Base: x.Configs.EthereumPrecompiles(rules), Env: &s.setEnv}
		x.Configs.ConfigInto(&s.cfg, rules, &s.set)
		s.built, s.builtFor = true, key
	}
	assert.Ok(s.set.Env == &s.setEnv && s.set.Fork == rules.Fork, "a kept precompile set is the scratch's own")
	assert.Ok(s.cfg.Precompiles == &s.set, "a kept config uses the scratch's precompile set")
	fillBlockContext(&s.blk, header, getHash)
	s.sys = systemEnv{config: &s.cfg, block: &s.blk, header: header, rules: rules}
	s.env = TxEnv{}
	return s
}

func returnScratch(s *blockScratch) {
	s.setEnv = precompiles.TxEnv{}
	s.blk = evm.BlockContext{}
	s.sys = systemEnv{}
	s.env = TxEnv{}
	blockScratches.Put(s)
}
