package rpcexec

import (
	"errors"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tracing"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

var errNoPipeline = errors.New("rpcexec: no transaction pipeline for this state")

type callRun struct {
	trace   *tracing.TxTrace
	info    tracing.TxInfo
	output  []byte
	status  evm.Status
	gasUsed uint64
	tx      types.Transaction
}

func (c *callRun) outcome() outcome {
	return outcome{res: evm.Result{Status: c.status, Output: c.output}, gasUsed: c.gasUsed}
}

func isTempoRequest(req *rpc.CallRequest) bool {
	return req.FeeToken != nil || req.NonceKey != nil || len(req.Calls) > 0
}

func requestCalls(req *rpc.CallRequest) []types.Call {
	if len(req.Calls) > 0 {
		return req.Calls
	}
	return []types.Call{{To: req.To, Value: req.Value, Input: req.Input}}
}

type pricing struct {
	maxFee u256.Int
	tip    u256.Int
}

func priceOf(req *rpc.CallRequest) pricing {
	if req.GasPrice != nil {
		return pricing{maxFee: *req.GasPrice, tip: *req.GasPrice}
	}
	var p pricing
	if req.MaxFeePerGas != nil {
		p.maxFee = *req.MaxFeePerGas
	}
	if req.MaxPriorityFeePerGas != nil {
		p.tip = *req.MaxPriorityFeePerGas
	}
	return p
}

type txShape struct {
	chainID uint64
	gas     uint64
	nonce   uint64
}

func buildTx(req *rpc.CallRequest, shape txShape) types.Transaction {
	chainID, gas, nonce := shape.chainID, shape.gas, shape.nonce
	p := priceOf(req)
	maxFee, tip := p.maxFee, p.tip
	if isTempoRequest(req) {
		t := &types.TempoTx{
			ChainID:              chainID,
			MaxPriorityFeePerGas: tip,
			MaxFeePerGas:         maxFee,
			GasLimit:             gas,
			Calls:                requestCalls(req),
			AccessList:           req.AccessList,
			Nonce:                nonce,
			FeeToken:             req.FeeToken,
		}
		if req.NonceKey != nil {
			t.NonceKey = *req.NonceKey
		}
		return types.Transaction{Data: t}
	}
	if req.GasPrice != nil && len(req.AccessList) == 0 {
		return types.Transaction{Data: &types.LegacyTx{Nonce: nonce, GasPrice: maxFee, Gas: gas, To: req.To, Value: req.Value, Data: req.Input}}
	}
	return types.Transaction{Data: &types.DynamicFeeTx{
		ChainID:              chainID,
		Nonce:                nonce,
		MaxPriorityFeePerGas: tip,
		MaxFeePerGas:         maxFee,
		Gas:                  gas,
		To:                   req.To,
		Value:                req.Value,
		Data:                 req.Input,
		AccessList:           req.AccessList,
	}}
}

type callSpec struct {
	req   *rpc.CallRequest
	gas   uint64
	steps tracing.StepOptions
}

func (x *Executor) applyCall(e *env, spec callSpec) (callRun, error) {
	st, ok := e.st.(executor.State)
	if !ok || x.Blocks == nil || e.header == nil {
		return callRun{}, errNoPipeline
	}
	defer st.RevertToSnapshot(st.Snapshot())
	req := spec.req
	from := sender(req)
	nonce := st.Nonce(from)
	if req.Nonce != nil {
		nonce = *req.Nonce
	}
	maxFee := priceOf(req).maxFee
	tx := buildTx(req, txShape{chainID: uint64(x.ChainID), gas: spec.gas, nonce: nonce})
	session := tracing.NewSession(st, []evm.Address{from}, spec.steps)
	sim := executor.Simulation{Sender: from, SkipNonceCheck: req.Nonce == nil, SkipBalanceCheck: maxFee.IsZero()}
	out, err := x.Blocks.ApplyCall(session, e.header, &tx, sim)
	if err != nil {
		return callRun{}, err
	}
	trace := session.Close()
	run := callRun{
		trace:   trace,
		gasUsed: out.GasUsed,
		tx:      tx,
		info:    tracing.TxInfo{AA: isTempoRequest(req), Sender: from, GasLimit: spec.gas, GasUsed: out.GasUsed, Success: out.Success},
		status:  evm.StatusStop,
	}
	rec := trace.Recorder
	if len(rec.Roots) > 0 {
		last := &rec.Nodes[rec.Roots[len(rec.Roots)-1]]
		run.output = last.Output
		run.status = last.Status
		for _, r := range rec.Roots {
			if n := &rec.Nodes[r]; !n.Success() {
				run.output, run.status = n.Output, n.Status
				break
			}
		}
	}
	if !out.Success && run.status.Success() {
		run.status = evm.StatusOutOfGas
	}
	return run, nil
}

func (x *Executor) execute(e *env, req *rpc.CallRequest, gas uint64) (outcome, error) {
	run, err := x.applyCall(e, callSpec{req: req, gas: gas})
	if errors.Is(err, errNoPipeline) {
		return x.run(e, req, gas)
	}
	if err != nil {
		return outcome{}, err
	}
	return run.outcome(), nil
}
