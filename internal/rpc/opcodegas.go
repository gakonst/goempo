package rpc

import (
	"encoding/json"
)

var emptyOpcodeGas = json.RawMessage("[]")

func (s *Server) opcodeGas(b *BlockData) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, len(b.Block.Transactions))
	if !systemOnly(b) {
		if s.exec == nil {
			return nil, internalError("unimplemented")
		}
		traces, err := s.exec.TraceBlock(b.Number(), &TraceOptions{OpcodeGas: true})
		if err != nil {
			return nil, executionError(err)
		}
		copy(out, traces)
	}
	for i := range out {
		if out[i] == nil {
			out[i] = emptyOpcodeGas
		}
	}
	return out, nil
}

func writeOpcodeGasTx(w *writer, b *BlockData, i int, gas json.RawMessage) {
	w.objOpen()
	w.kHash("transactionHash", b.Block.Transactions[i].Hash())
	w.key("opcodeGas")
	w.raw(gas)
	w.objClose()
}

func traceTransactionOpcodeGas(s *Server, p *params) ([]byte, error) {
	t, err := txByHashArg(s, p)
	if err != nil {
		return nil, err
	}
	if t.b == nil {
		return resultNull, nil
	}
	gas, err := s.opcodeGas(t.b)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) { writeOpcodeGasTx(w, t.b, t.i, gas[t.i]) }), nil
}

func traceBlockOpcodeGas(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block_id")
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return nil, err
	}
	b := s.blockFor(ref)
	if b == nil {
		return nil, blockNotFound(ref.describe())
	}
	gas, err := s.opcodeGas(b)
	if err != nil {
		return nil, err
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.kHash("blockHash", b.Hash)
		w.key("blockNumber")
		w.integer(int64(b.Number()))
		w.key("transactions")
		w.arrOpen()
		for i := range gas {
			writeOpcodeGasTx(w, b, i, gas[i])
		}
		w.arrClose()
		w.objClose()
	}), nil
}
