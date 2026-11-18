package state_test

import (
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/state"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const (
	parBlocks    = 60
	parWorkers   = 3
	parTxsMax    = 24
	parOpsMax    = 8
	parDeltaKeys = 3
)

// parOp is one step of a generated transaction. Kinds below 16 are the
// fuzzer's state operations; parOpDebit and parOpCredit move a counter slot
// the way a token balance moves, inside an affine window.
type parOp struct {
	kind     int
	addr     primitives.Address
	key      primitives.Hash
	val      u256.Int
	code     []byte
	nonce    uint64
	destruct bool
}

const (
	parOpSnapshot = 16 + iota
	parOpRevert
	parOpDebit
	parOpCredit
	parOpKinds
)

// txState is a state transactions run on and finalize.
type txState interface {
	evm.State
	FinalizeTx()
}

const dbgPar = false

var parCounter = primitives.Address{0xc0, 19: 0x01}

func (f *fuzzer) parTx() []parOp {
	n := 1 + f.rng.IntN(parOpsMax)
	ops := make([]parOp, 0, n)
	for range n {
		op := parOp{kind: f.rng.IntN(parOpKinds), addr: f.addr(), key: f.slot(), val: f.value(), nonce: f.rng.Uint64N(4), destruct: f.rng.IntN(4) == 0}
		if f.rng.IntN(3) != 0 {
			op.code = []byte{byte(f.rng.IntN(4)), 0x60}
		}
		if op.kind == parOpDebit || op.kind == parOpCredit {
			op.addr = parCounter
			op.key = primitives.Hash{31: byte(f.rng.IntN(parDeltaKeys))}
			op.val = u256.FromUint64(1 + f.rng.Uint64N(50))
		}
		ops = append(ops, op)
	}
	return ops
}

// runParTx runs a generated transaction; spec is the speculator running it,
// or nil.
func runParTx(s txState, spec *state.State, ops []parOp) {
	var snaps []int
	for i := range ops {
		op := &ops[i]
		switch op.kind {
		case parOpSnapshot:
			snaps = append(snaps, s.Snapshot())
		case parOpRevert:
			if len(snaps) > 0 {
				s.RevertToSnapshot(snaps[len(snaps)-1])
				snaps = snaps[:len(snaps)-1]
			}
		case parOpDebit, parOpCredit:
			runDelta(s, spec, op)
		default:
			runFuzzOp(s, op)
		}
	}
	s.FinalizeTx()
}

func runDelta(s evm.State, spec *state.State, op *parOp) {
	if spec != nil {
		spec.BeginAffine(state.Slot{Address: op.addr, Key: op.key})
		defer spec.EndAffine()
	}
	cur := s.Storage(op.addr, op.key)
	if op.kind == parOpCredit {
		s.SetStorage(op.addr, op.key, cur.Add(op.val))
		return
	}
	if cur.Lt(op.val) {
		// A failed bound check must not be rebased: its outcome would flip.
		if spec != nil {
			spec.ClearAffine()
		}
		return
	}
	s.SetStorage(op.addr, op.key, cur.Sub(op.val))
}

func runFuzzOp(s evm.State, op *parOp) {
	a, k, v := op.addr, op.key, op.val
	switch op.kind {
	case 0, 1:
		s.AddBalance(a, v)
	case 2:
		if s.Balance(a).Cmp(v) >= 0 {
			s.SubBalance(a, v)
		}
	case 3:
		s.SetNonce(a, op.nonce)
	case 4:
		s.SetCode(a, op.code)
	case 5, 6, 7:
		s.SetStorage(a, k, v)
	case 8:
		s.CreateAccount(a)
	case 9:
		if op.destruct {
			s.SelfDestruct(a)
		}
	case 10:
		s.Touch(a)
	case 11:
		s.SetTransientStorage(a, k, v)
	case 12:
		s.WarmSlot(a, k)
	case 13:
		s.WarmAddress(a)
	case 14:
		s.AddRefund(int64(op.nonce) - 1)
	case 15:
		s.AddLog(evm.Log{Address: a, Data: op.code})
	}
}

// TestParallelCommitMatchesSequential speculates generated blocks on worker
// views, commits them in order (applying each valid speculation and
// re-running the rest) and checks every root against the sequential model.
func TestParallelCommitMatchesSequential(t *testing.T) {
	for seed := uint64(1); seed <= 8; seed++ {
		parallelCommitRun(t, seed)
	}
}

func parallelCommitRun(t *testing.T, seed uint64) {
	f := newFuzzer(t, seed, state.Options{})
	setup := []parOp{{kind: 0, addr: parCounter, val: u256.One()}}
	for k := range parDeltaKeys {
		setup = append(setup, parOp{kind: 5, addr: parCounter, key: primitives.Hash{31: byte(k)}, val: u256.FromUint64(500)})
	}
	runParTx(f.model, nil, setup)
	runParTx(f.db.State(), nil, setup)
	_, err := f.db.State().Commit(1)
	assert.NoError(err, "setup commit")
	var reused, rebased, total int
	for n := uint64(2); n <= parBlocks; n++ {
		txs := make([][]parOp, 1+f.rng.IntN(parTxsMax))
		for i := range txs {
			txs[i] = f.parTx()
			runParTx(f.model, nil, txs[i])
		}
		st := f.db.State()
		specs := speculateAll(st, txs)
		st.BeginParallel()
		for i := range txs {
			total++
			if sp := specs[i]; sp != nil && st.ValidateSpec(sp) {
				st.ApplySpec(sp)
				if dbgPar {
					if _, err := st.PendingRoot(); err != nil {
						t.Fatalf("seed %d block %d tx %d applied: %v ops %+v", seed, n, i, err, txs[i])
					}
				}
				reused++
				if sp.Rebased() {
					rebased++
				}
				continue
			}
			runParTx(st, nil, txs[i])
			if dbgPar {
				if _, err := st.PendingRoot(); err != nil {
					t.Fatalf("seed %d block %d tx %d serial: %v", seed, n, i, err)
				}
			}
		}
		st.EndParallel()
		root, err := st.Commit(n)
		assert.NoError(err, "commit")
		if want := modelRoot(f.model); root != want {
			t.Fatalf("seed %d block %d root %s want %s", seed, n, root, want)
		}
	}
	t.Logf("seed %d: reused %d (rebased %d) of %d", seed, reused, rebased, total)
	assert.Ok(reused > total/4, "most speculation is reused")
	assert.Ok(rebased > 0, "some speculation is rebased")
}

func speculateAll(st *state.State, txs [][]parOp) []*state.SpecTx {
	out := make([]*state.SpecTx, len(txs))
	var wg sync.WaitGroup
	for w := range parWorkers {
		sp := st.NewSpeculator()
		wg.Go(func() {
			sp.ResetSpeculation()
			for i := w; i < len(txs); i += parWorkers {
				sp.BeginSpecTx()
				runParTx(sp, sp, txs[i])
				if t := sp.EndSpecTx(); !t.Unsafe() {
					out[i] = t
				}
			}
		})
	}
	wg.Wait()
	return out
}
