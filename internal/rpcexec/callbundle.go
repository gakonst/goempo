package rpcexec

import (
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tracing"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const bundleBlockSpacing = 12

func (x *Executor) bundleHeader(parent *types.Block, req *rpc.BundleRequest) *types.Block {
	blk := *parent
	h := &blk.Header.Inner
	gasUsed := h.GasUsed
	parentFee := h.BaseFeePerGas
	h.Timestamp += bundleBlockSpacing
	if req.Timestamp != nil {
		h.Timestamp = *req.Timestamp
	}
	if req.Coinbase != nil {
		h.Beneficiary = *req.Coinbase
	}
	h.GasLimit = req.GasLimit
	h.BaseFeePerGas = chain.RulesAt(x.ChainID, h.Timestamp).ExpectedBaseFee(chain.ParentGas{BaseFee: parentFee, GasUsed: gasUsed})
	if req.BaseFee != nil {
		h.BaseFeePerGas = *req.BaseFee
	}
	h.Number = req.BlockNumber
	blk.Transactions = nil
	return &blk
}

func (x *Executor) CallBundle(req *rpc.BundleRequest) (rpc.BundleResult, error) {
	release := x.acquire()
	defer release()
	if x.Blocks == nil {
		return rpc.BundleResult{}, ErrNoBlockExecutor
	}
	parent, ok := x.Chain.Block(req.StateBlock)
	if !ok {
		return rpc.BundleResult{}, ErrNoBlock
	}
	bs, err := x.blockState(req.StateBlock)
	if err != nil {
		return rpc.BundleResult{}, err
	}
	defer bs.Discard()
	blk := x.bundleHeader(parent.Block, req)
	baseFee := blk.Header.Inner.BaseFeePerGas
	coinbase := blk.Header.Inner.Beneficiary
	initial := bs.Balance(coinbase)
	before := initial
	var out rpc.BundleResult
	for i := range req.Txs {
		tx := &req.Txs[i]
		if out.GasUsed+gasLimit(*tx) > req.GasLimit {
			return rpc.BundleResult{}, &rpc.Error{Code: rpc.CodeInvalidParams, Message: "bundle gas limit exceeded"}
		}
		if rpc.EffectiveGasPrice(*tx, baseFee).Lt(u256.FromUint64(baseFee)) {
			return rpc.BundleResult{}, &rpc.Error{Code: rpc.CodeServer, Message: "max fee per gas less than block base fee"}
		}
		from := req.Senders[i]
		session := tracing.NewSession(bs, []evm.Address{from}, tracing.StepOptions{})
		res, err := x.Blocks.ApplyCall(session, blk, tx, executor.Simulation{Sender: from})
		if err != nil {
			return rpc.BundleResult{}, err
		}
		trace := session.Close()
		bs.FinalizeTx()
		end := lastFrame(trace.Recorder)
		after := bs.Balance(coinbase)
		out.GasUsed += res.GasUsed
		out.Txs = append(out.Txs, rpc.BundleTxResult{
			GasUsed:      res.GasUsed,
			GasPrice:     rpc.EffectiveTip(*tx, baseFee),
			Success:      res.Success,
			Output:       end.output,
			CoinbaseDiff: rpc.Balances{Before: before, After: after}.Gain(),
		})
		before = after
	}
	out.CoinbaseDiff = rpc.Balances{Before: initial, After: before}.Gain()
	return out, nil
}
