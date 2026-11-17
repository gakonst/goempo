package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const healsMax = 1 << 20

const _ = uint(healsMax - diagReceiptsMax)

var errHealRoot = errors.New("state after applying the trace diff does not match the header")

type healer struct {
	client    *rpcClient
	remaining int
	first     *mismatch
	firstRes  *executor.Result
	healed    []mismatch
	verbose   bool
}

func (r *replayer) stepOrHeal(blk *types.Block) bool {
	if r.step(blk) {
		return true
	}
	if r.heal == nil || r.heal.remaining == 0 {
		return false
	}
	if r.heal.first == nil {
		r.heal.first = r.failed
		r.heal.firstRes = r.result
		r.firstHead = r.head
	}
	r.heal.remaining--
	r.heal.healed = append(r.heal.healed, *r.failed)
	fmt.Fprintf(os.Stderr, "heal: %s\n", r.failed.String())
	if r.heal.verbose && r.result != nil {
		r.diagnoseReceipts(r.heal.client)
	}
	if err := r.applyTrace(blk); err != nil {
		fmt.Fprintf(os.Stderr, "heal: block %d: %v\n", blk.Header.Inner.Number, err)
		r.failed = &mismatch{number: blk.Header.Inner.Number, field: "heal", got: err.Error(), want: "ok"}
		return false
	}
	r.failed = nil
	return true
}

func (r *replayer) reportHeals() {
	if r.heal == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "heal: %d blocks healed, executed through %d\n", len(r.heal.healed), r.head)
}

func (r *replayer) applyTrace(blk *types.Block) error {
	h := &blk.Header.Inner
	st := r.db.State()
	st.Discard()
	var traces []traceResult
	opts := map[string]any{"tracer": "prestateTracer", "tracerConfig": map[string]any{"diffMode": true}}
	q := rpcQuery{method: "debug_traceBlockByNumber", params: []any{blockTag(h.Number), opts}}
	if err := callInto(r.heal.client, q, &traces); err != nil {
		return err
	}
	count := min(len(traces), diagReceiptsMax)
	for i := 0; i < count; i++ {
		if err := applyDiff(st, &traces[i].Result); err != nil {
			return err
		}
	}
	root, err := st.Commit(h.Number)
	if err != nil {
		return err
	}
	if root != h.StateRoot {
		return errHealRoot
	}
	r.hashes[h.Number%hashWindow] = blk.Header.Hash()
	r.head = h.Number
	return nil
}

type diffState interface {
	stateReader
	Balance(addr primitives.Address) u256.Int
	AddBalance(addr primitives.Address, amount u256.Int)
	SubBalance(addr primitives.Address, amount u256.Int)
	SetNonce(addr primitives.Address, nonce uint64)
	SetCode(addr primitives.Address, code []byte)
	SetStorage(addr primitives.Address, key primitives.Hash, value u256.Int)
	FinalizeTx()
}

func applyDiff(st diffState, d *prestateDiff) error {
	for _, addrHex := range slices.Sorted(maps.Keys(d.Pre)) {
		addr, err := parseAddr(addrHex)
		if err != nil {
			return err
		}
		post := d.Post[addrHex]
		for _, k := range slices.Sorted(maps.Keys(d.Pre[addrHex].Storage)) {
			if _, kept := post.Storage[k]; kept {
				continue
			}
			key, err := parseHash(k)
			if err != nil {
				return err
			}
			st.SetStorage(addr, key, u256.Int{})
		}
	}
	for _, addrHex := range slices.Sorted(maps.Keys(d.Post)) {
		addr, err := parseAddr(addrHex)
		if err != nil {
			return err
		}
		acct := d.Post[addrHex]
		if err := applyAccount(st, addr, &acct); err != nil {
			return err
		}
	}
	st.FinalizeTx()
	return nil
}

func applyAccount(st diffState, addr primitives.Address, acct *prestateAccount) error {
	if acct.Nonce != 0 {
		st.SetNonce(addr, acct.Nonce)
	}
	if acct.Code != "" {
		code, err := decodeHex(acct.Code)
		if err != nil {
			return err
		}
		st.SetCode(addr, code)
	}
	if acct.Balance != "" {
		b, err := parseHash(acct.Balance)
		if err != nil {
			return err
		}
		want := u256.FromBytes32(b)
		have := st.Balance(addr)
		if want.Lt(have) {
			st.SubBalance(addr, have.Sub(want))
		} else {
			st.AddBalance(addr, want.Sub(have))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(acct.Storage)) {
		key, err := parseHash(k)
		if err != nil {
			return err
		}
		v, err := parseHash(acct.Storage[k])
		if err != nil {
			return err
		}
		st.SetStorage(addr, key, u256.FromBytes32(v))
	}
	return nil
}
