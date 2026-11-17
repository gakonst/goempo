package rpc

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/gakonst/goempo/internal/primitives"
)

func u64Param(raw json.RawMessage) (uint64, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
		return 0, typeError(raw, "u64")
	}
	v, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, typeError(raw, "u64")
	}
	return v, nil
}

func debugSeedHash(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("number")
	if err != nil {
		return nil, err
	}
	if _, err := u64Param(raw); err != nil {
		return nil, err
	}
	return hashResult(primitives.Hash{}), nil
}

func debugTraceBadBlock(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("block_hash")
	if err != nil {
		return nil, err
	}
	if _, err := hashParam(raw); err != nil {
		return nil, err
	}
	if _, err := traceOptions(p); err != nil {
		return nil, err
	}
	return nil, internalError("bad block not found in cache")
}

func rpcModules(_ *Server, _ *params) ([]byte, error) {
	return encode(func(w *writer) {
		w.objOpen()
		for _, m := range []string{"net", "rpc", "debug", "operator", "trace", "eth", "web3"} {
			w.kStr(field(m), "1.0")
		}
		w.objClose()
	}), nil
}

func debugGetRawTransactions(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block_id")
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return nil, err
	}
	b := s.blockFor(ref)
	return encode(func(w *writer) {
		w.arrOpen()
		if b != nil {
			for i := range b.Block.Transactions {
				w.data(b.Block.Transactions[i].Encode())
			}
		}
		w.arrClose()
	}), nil
}

type CodeLookup struct {
	Code  []byte
	Found bool
}

type CodeSource interface {
	CodeByHash(hash primitives.Hash) (CodeLookup, error)
}

func debugCodeByHash(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("hash")
	if err != nil {
		return nil, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(p.optional("block_id"))
	if err != nil {
		return nil, err
	}
	if _, err := s.stateBlock(ref); err != nil {
		return nil, err
	}
	src, ok := s.state.(CodeSource)
	if !ok {
		return resultNull, nil
	}
	code, err := src.CodeByHash(h)
	if err != nil {
		return nil, internalError(err.Error())
	}
	if !code.Found {
		return resultNull, nil
	}
	return dataResult(code.Code), nil
}
