package rpcexec

import (
	"encoding/json"
	"errors"

	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/executor"
)

const codeValueTransfer = -32003

func (x *Executor) TraceCallMany(block uint64, bundles []rpc.CallBundle, opts *rpc.TraceOptions) ([][]json.RawMessage, error) {
	cfg, err := parseOptions(opts)
	if errors.Is(err, ErrTracer) {
		return nil, &rpc.Error{Code: rpc.CodeInternal, Message: "JS Tracer is not enabled"}
	}
	if err != nil {
		return nil, err
	}
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
	out := make([][]json.RawMessage, len(bundles))
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
			run, err := x.applyCall(&be, callSpec{req: req, gas: gas, steps: cfg.steps, keep: keepCallState})
			if errors.Is(err, executor.ErrValue) {
				return nil, &rpc.Error{Code: codeValueTransfer, Message: "value transfer not allowed"}
			}
			if err != nil {
				return nil, err
			}
			info := run.info
			info.Output = run.output
			out[i] = append(out[i], render(cfg, run.trace, info))
		}
	}
	return out, nil
}
