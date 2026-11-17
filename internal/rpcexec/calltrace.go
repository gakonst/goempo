package rpcexec

import (
	"encoding/json"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/tracing"
	"github.com/gakonst/goempo/internal/types"
)

const traceCallGasCap = 50_000_000

func (x *Executor) TraceCall(block uint64, req *rpc.CallRequest, opts *rpc.TraceOptions) (json.RawMessage, error) {
	cfg, err := parseOptions(opts)
	if err != nil {
		return nil, err
	}
	release := x.acquire()
	defer release()
	e, err := x.envAt(block)
	if err != nil {
		return nil, err
	}
	gas := min(e.block.GasLimit, traceCallGasCap)
	if req.Gas != nil {
		gas = min(*req.Gas, gas)
	}
	run, err := x.applyCall(&e, callSpec{req: req, gas: gas, steps: cfg.steps})
	if err != nil {
		return nil, err
	}
	info := run.info
	info.Output = run.output
	return render(cfg, run.trace, info), nil
}

func (x *Executor) CreateAccessList(block uint64, req *rpc.CallRequest, gasCap uint64) (rpc.AccessListResult, error) {
	release := x.acquire()
	defer release()
	e, err := x.envAt(block)
	if err != nil {
		return rpc.AccessListResult{}, err
	}
	gas := x.gasLimit(&e, req, gasCap)
	steps := tracing.StepOptions{Enabled: true, Stack: true}
	run, err := x.applyCall(&e, callSpec{req: req, gas: gas, steps: steps})
	if err != nil {
		return rpc.AccessListResult{}, err
	}
	fork := e.rules.Fork
	eth := e.rules.EthereumPrecompiles()
	spec := tracing.AccessSpec{
		Initial: toTuples(req.AccessList),
		Sender:  sender(req),
		Excluded: func(a evm.Address) bool {
			if _, ok := eth.Lookup(a); ok {
				return true
			}
			return precompiles.IsPrecompile(a, fork)
		},
	}
	list := tracing.AccessListOf(run.trace.Recorder, spec)
	out := rpc.AccessListResult{AccessList: fromTuples(list), GasUsed: run.gasUsed}
	if !run.status.Success() {
		out.Failure = result(run.outcome())
		return out, nil
	}
	again := *req
	again.AccessList = out.AccessList
	second, err := x.applyCall(&e, callSpec{req: &again, gas: gas})
	if err != nil {
		return rpc.AccessListResult{}, err
	}
	out.GasUsed = second.gasUsed
	if !second.status.Success() {
		out.Failure = result(second.outcome())
	}
	return out, nil
}

func toTuples(al types.AccessList) []tracing.AccessTuple {
	out := make([]tracing.AccessTuple, len(al))
	for i, t := range al {
		out[i] = tracing.AccessTuple{Address: t.Address, Keys: t.StorageKeys}
	}
	return out
}

func fromTuples(list []tracing.AccessTuple) types.AccessList {
	out := make(types.AccessList, len(list))
	for i, t := range list {
		out[i] = types.AccessTuple{Address: t.Address, StorageKeys: t.Keys}
	}
	return out
}
