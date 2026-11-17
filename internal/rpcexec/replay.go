package rpcexec

import (
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tracing"
)

func parityTraces(t *tracing.TxTrace, info tracing.TxInfo, loc *tracing.Locator) []rpc.ParityTrace {
	flats := tracing.FlatTraces(t.Recorder, info)
	out := make([]rpc.ParityTrace, len(flats))
	for i := range flats {
		f := &flats[i]
		out[i] = rpc.ParityTrace{From: f.From, To: f.To, ToKnown: f.ToKnown, Addr: f.Addr, Plain: tracing.FlatJSON(t.Recorder, f, nil)}
		if loc != nil {
			out[i].Localized = tracing.FlatJSON(t.Recorder, f, loc)
		}
	}
	return out
}

func replayOf(t *tracing.TxTrace, info tracing.TxInfo, loc *tracing.Locator, diff rpc.DiffMode) *rpc.TxReplay {
	r := &rpc.TxReplay{Traces: parityTraces(t, info, loc), Output: tracing.TxOutput(t.Recorder, info)}
	if diff == rpc.WithStateDiff {
		r.StateDiff = tracing.StateDiffJSON(t)
	}
	return r
}

func (x *Executor) ReplayBlock(number uint64, stateDiff rpc.DiffMode) ([]*rpc.TxReplay, error) {
	release := x.acquire()
	defer release()
	run, err := x.replayBlock(number, tracing.StepOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]*rpc.TxReplay, len(run.data.Block.Transactions))
	for u, i := range run.user {
		loc := &tracing.Locator{BlockHash: run.data.Hash, BlockNumber: number, TxHash: run.data.TxHash(i), TxIndex: i}
		out[i] = replayOf(run.trace(u), run.infos[i], loc, stateDiff)
	}
	return out, nil
}

func (x *Executor) TraceCalls(block uint64, reqs []*rpc.CallRequest, stateDiff rpc.DiffMode) ([]rpc.TxReplay, error) {
	release := x.acquire()
	defer release()
	e, err := x.envAt(block)
	if err != nil {
		return nil, err
	}
	bs, err := x.blockState(block)
	if err != nil {
		return nil, err
	}
	defer bs.Discard()
	e.st = bs
	out := make([]rpc.TxReplay, 0, len(reqs))
	used := uint64(0)
	for _, req := range reqs {
		gas := traceCallGasCap - min(used, traceCallGasCap)
		if req.Gas != nil {
			gas = min(*req.Gas, gas)
		}
		run, err := x.applyCall(&e, callSpec{req: req, gas: gas, keep: keepCallState})
		if err != nil {
			return nil, err
		}
		used += run.gasUsed
		info := run.info
		info.Output = run.output
		out = append(out, *replayOf(run.trace, info, nil, stateDiff))
	}
	return out, nil
}

func (x *Executor) CallMany(block uint64, bundles []rpc.CallBundle) ([][]rpc.CallResult, error) {
	release := x.acquire()
	defer release()
	e, err := x.envAt(block)
	if err != nil {
		return nil, err
	}
	bs, err := x.blockState(block)
	if err != nil {
		return nil, err
	}
	defer bs.Discard()
	e.st = bs
	out := make([][]rpc.CallResult, len(bundles))
	for i := range bundles {
		be := e
		if n := bundles[i].Number; n != nil {
			blk := *e.block
			blk.Number = *n
			be.block = &blk
		}
		for _, req := range bundles[i].Calls {
			gas := min(be.block.GasLimit, traceCallGasCap)
			if req.Gas != nil {
				gas = min(*req.Gas, gas)
			}
			run, err := x.applyCall(&be, callSpec{req: req, gas: gas, keep: keepCallState})
			if err != nil {
				return nil, err
			}
			out[i] = append(out[i], result(run.outcome()))
		}
	}
	return out, nil
}
