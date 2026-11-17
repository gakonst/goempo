package rpc

import (
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const fillFeeHeadroom = 2

func ethFillTransaction(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("request")
	if err != nil {
		return nil, err
	}
	if err := structParam(raw, "struct TempoTransactionRequest"); err != nil {
		return nil, err
	}
	req, err := callParam(raw)
	if err != nil {
		return nil, err
	}
	if req.From == nil {
		return nil, newError(CodeInvalidParams, "unknown account")
	}
	if req.FeeToken != nil || req.NonceKey != nil || len(req.Calls) > 0 || req.GasPrice != nil {
		return nil, internalError("unimplemented")
	}
	head := s.chain.Head()
	tx := types.DynamicFeeTx{ChainID: s.cfg.ChainID, To: req.To, Value: req.Value, Data: req.Input, AccessList: req.AccessList}
	if req.Nonce != nil {
		tx.Nonce = *req.Nonce
	} else {
		if s.state == nil {
			return nil, internalError("unimplemented")
		}
		st, err := s.state.StateAt(head)
		if err != nil {
			return nil, internalError(err.Error())
		}
		tx.Nonce = st.Nonce(*req.From)
	}
	if req.Gas != nil {
		tx.Gas = *req.Gas
	} else {
		if s.exec == nil {
			return nil, internalError("unimplemented")
		}
		if tx.Gas, err = s.exec.EstimateGas(head, req, s.cfg.GasCap); err != nil {
			return nil, executionError(err)
		}
	}
	if req.MaxPriorityFeePerGas != nil {
		tx.MaxPriorityFeePerGas = *req.MaxPriorityFeePerGas
	}
	if req.MaxFeePerGas != nil {
		tx.MaxFeePerGas = *req.MaxFeePerGas
	} else {
		b := s.blockFor(blockRef{kind: tagNumber, number: head})
		if b == nil {
			return nil, blockNotFound("latest")
		}
		base := u256.FromUint64(b.Block.Header.Inner.BaseFeePerGas)
		tx.MaxFeePerGas = base.Mul(u256.FromUint64(fillFeeHeadroom)).Add(tx.MaxPriorityFeePerGas)
	}
	signed := types.Transaction{Data: &tx}
	return encode(func(w *writer) {
		w.objOpen()
		w.kData("raw", signed.Encode())
		w.key("tx")
		w.objOpen()
		writeDynamicFields(w, types.TxTypeDynamicFee, &tx, nil)
		w.kHash("hash", signed.Hash())
		w.objClose()
		w.objClose()
	}), nil
}
