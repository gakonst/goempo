package executor

import (
	"cmp"
	"slices"
	"sync"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type Parallel struct {
	Workers int
	MinTxs  int
	Force   bool
}

const (
	parallelWorkersMax = 64
	parallelMinTxs     = 4
)

type specResult struct {
	out     TxOutcome
	receipt types.Receipt
	spec    *state.SpecTx
	ok      bool
}

type blockRun struct {
	expiring []expiringPlan
	rules    chain.Rules
	block    *evm.BlockContext
	txs      []types.Transaction
	senders  []Sender
}

type shardPlan struct {
	shards [][]int
}

func (p Parallel) minTxs() int {
	if p.MinTxs > 0 {
		return p.MinTxs
	}
	return parallelMinTxs
}

func (x *Executor) planParallel(st State, in Input, rules chain.Rules) (*state.State, shardPlan, bool) {
	p := x.Parallel
	txs := in.Block.Transactions
	if p.Workers < 2 || len(txs) < p.minTxs() || len(in.Senders) != len(txs) {
		return nil, shardPlan{}, false
	}
	ps, ok := st.(*state.State)
	if !ok {
		return nil, shardPlan{}, false
	}
	workers := min(p.Workers, parallelWorkersMax)
	if p.Force {
		return ps, planRoundRobin(txs, in.Senders, workers), true
	}
	plan, ok := planShards(txs, in.Senders, workers, rules.AtLeast(hardfork.T1))
	return ps, plan, ok
}

// planKey groups transactions that must share a worker: a sender's
// sequential nonces (or one 2D nonce key) chain, so they run in order on one
// worker's overlay; an expiring nonce transaction chains with nothing.
type planKey struct {
	sender Address
	key    u256.Int
	index  int
}

func keyOf(tx *types.Transaction, s Sender, i int, expiring bool) planKey {
	k := planKey{sender: s.Address, index: -1}
	if d, ok := tx.Data.(*types.TempoTx); ok {
		if expiring && d.NonceKey == maxWord {
			k.index = i
		}
		k.key = d.NonceKey
	}
	return k
}

type planGroup struct {
	txs []int
}

func planShards(txs []types.Transaction, senders []Sender, workers int, expiring bool) (shardPlan, bool) {
	assert.Ok(workers >= 2, "a parallel plan has workers")
	assert.Ok(len(txs) == len(senders), "every transaction has a sender")
	idx := make(map[planKey]int, len(txs))
	var groups []planGroup
	speculated := 0
	for i := range txs {
		if IsSystemTx(&txs[i]) || senders[i].Err != nil {
			continue
		}
		speculated++
		k := keyOf(&txs[i], senders[i], i, expiring)
		j, ok := idx[k]
		if !ok {
			j = len(groups)
			idx[k] = j
			groups = append(groups, planGroup{})
		}
		groups[j].txs = append(groups[j].txs, i)
	}
	if len(groups) < 2 {
		return shardPlan{}, false
	}
	workers = min(workers, len(groups))
	order := make([]int, len(groups))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(len(groups[b].txs), len(groups[a].txs)) })
	sizes := make([]int, workers)
	plan := shardPlan{shards: make([][]int, workers)}
	for _, g := range order {
		w := 0
		for k := 1; k < workers; k++ {
			if sizes[k] < sizes[w] {
				w = k
			}
		}
		sizes[w] += len(groups[g].txs)
		plan.shards[w] = append(plan.shards[w], groups[g].txs...)
	}
	if slices.Max(sizes)*2 > speculated {
		return shardPlan{}, false
	}
	for w := range plan.shards {
		slices.Sort(plan.shards[w])
	}
	return plan, true
}

func planRoundRobin(txs []types.Transaction, senders []Sender, workers int) shardPlan {
	plan := shardPlan{shards: make([][]int, workers)}
	k := 0
	for i := range txs {
		if IsSystemTx(&txs[i]) || senders[i].Err != nil {
			continue
		}
		plan.shards[k%workers] = append(plan.shards[k%workers], i)
		k++
	}
	return plan
}

type specWorker struct {
	st  *state.State
	set *precompiles.Set
	cfg *evm.Config
}

func (w *specWorker) run(b *blockRun, idx []int, results []specResult) {
	w.st.ResetSpeculation()
	for _, i := range idx {
		w.set.Env.CreditOwners = nil
		w.st.BeginSpecTx()
		env := &TxEnv{State: w.st, Rules: b.rules, Config: w.cfg, Block: b.block, Tx: &b.txs[i], Index: i, Sender: &b.senders[i], Spec: w.st, expiring: b.expiring[i]}
		out, err := applyTx(env, w.set)
		t := w.st.EndSpecTx()
		if err != nil {
			w.st.ResetSpeculation()
			continue
		}
		if t.Unsafe() {
			continue
		}
		results[i] = specResult{out: out, receipt: receiptOf(b.txs[i].Type(), out, 0), spec: t, ok: true}
	}
}

func planExpiring(ps *state.State, b *blockRun) {
	b.expiring = make([]expiringPlan, len(b.txs))
	if !b.rules.AtLeast(hardfork.T1) {
		return
	}
	p := ps.Storage(precompiles.NonceManagerAddress, expiringPtr())
	if !p.IsUint64() {
		return
	}
	rank := 0
	for i := range b.txs {
		d, ok := b.txs[i].Data.(*types.TempoTx)
		if !ok || d.NonceKey != maxWord {
			continue
		}
		b.expiring[i] = expiringPlan{ok: true, base: p.Uint64(), rank: rank}
		rank++
	}
}

func (x *Executor) speculate(ps *state.State, b *blockRun, plan shardPlan) []specResult {
	planExpiring(ps, b)
	results := make([]specResult, len(b.txs))
	workers := make([]specWorker, len(plan.shards))
	for w := range workers {
		set := precompiles.NewSet(b.rules.Fork, x.Configs.EthereumPrecompiles(b.rules))
		workers[w] = specWorker{st: ps.NewSpeculator(), set: set, cfg: x.Configs.Config(b.rules, set)}
	}
	var wg sync.WaitGroup
	for w := range workers {
		wk := &workers[w]
		shard := plan.shards[w]
		wg.Go(func() { wk.run(b, shard, results) })
	}
	wg.Wait()
	return results
}

type ParallelStats struct {
	Blocks  uint64
	Txs     uint64
	Reused  uint64
	Rebased uint64
	Reexec  uint64
}

func (x *Executor) executeParallel(ps *state.State, b *blockRun, plan shardPlan, set *precompiles.Set, cfg *evm.Config) ([]types.Receipt, uint64, error) {
	results := x.speculate(ps, b, plan)
	ps.BeginParallel()
	defer ps.EndParallel()
	receipts := make([]types.Receipt, 0, len(b.txs))
	var cumulative uint64
	x.Stats.Blocks++
	for i := range b.txs {
		r := &results[i]
		x.Stats.Txs++
		var out TxOutcome
		reused := false
		if r.ok && ps.ValidateSpec(r.spec) && r.expiringReplays(ps, b) {
			ps.ApplySpec(r.spec)
			if r.out.expiring != nil {
				r.out.expiring.apply(ps)
			}
			out = r.out
			reused = true
			x.Stats.Reused++
			if r.spec.Rebased() {
				x.Stats.Rebased++
			}
		} else {
			env := &TxEnv{State: ps, Rules: b.rules, Config: cfg, Block: b.block, Tx: &b.txs[i], Index: i, Sender: &b.senders[i]}
			var err error
			out, err = apply(env, set, len(b.txs))
			if err != nil {
				return nil, 0, txError(i, err)
			}
			x.Stats.Reexec++
		}
		cumulative += out.GasUsed
		if reused {
			rc := r.receipt
			rc.CumulativeGasUsed = cumulative
			receipts = append(receipts, rc)
			continue
		}
		receipts = append(receipts, receiptOf(b.txs[i].Type(), out, cumulative))
	}
	return receipts, cumulative, nil
}

func (r *specResult) expiringReplays(ps *state.State, b *blockRun) bool {
	e := r.out.expiring
	return e == nil || e.check(ps, b.block.Time, expiringCapacity(b.rules))
}
