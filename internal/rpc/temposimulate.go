package rpc

import (
	"bytes"
	"encoding/binary"
	"slices"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
)

const (
	tokenMetadataGas = 1_000_000
)

var (
	tip20Prefix   = []byte{0x20, 0xc0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	transferTopic = keccak.Sum256([]byte("Transfer(address,address,uint256)"))
)

type tokenMetadata struct {
	name     string
	symbol   string
	currency string
}

func isTIP20(a *primitives.Address) bool {
	return a != nil && bytes.HasPrefix(a[:], tip20Prefix)
}

func simulateTokens(req *SimulateRequest, blocks []SimulatedBlock) []primitives.Address {
	set := map[primitives.Address]struct{}{}
	add := func(a *primitives.Address) {
		if isTIP20(a) {
			set[*a] = struct{}{}
		}
	}
	for _, b := range req.Blocks {
		for _, c := range b.Calls {
			add(c.To)
			for i := range c.Calls {
				add(c.Calls[i].To)
			}
			add(c.FeeToken)
		}
	}
	for _, b := range blocks {
		for _, c := range b.Calls {
			for i := range c.Logs {
				l := &c.Logs[i]
				if len(l.Topics) > 0 && l.Topics[0] == primitives.Hash(transferTopic) {
					add(&l.Address)
				}
			}
		}
	}
	out := make([]primitives.Address, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y primitives.Address) int { return bytes.Compare(x[:], y[:]) })
	return out
}

func abiStringResult(ret []byte) (string, bool) {
	if len(ret) < 2*abiWord {
		return "", false
	}
	off := binary.BigEndian.Uint64(ret[abiWord-8 : abiWord])
	if off > uint64(len(ret)-abiWord) {
		return "", false
	}
	n := binary.BigEndian.Uint64(ret[off+abiWord-8 : off+abiWord])
	start := off + abiWord
	if n > uint64(len(ret))-start {
		return "", false
	}
	return string(ret[start : start+n]), true
}

func (s *Server) tokenString(block uint64, token primitives.Address, signature string) (string, bool) {
	sel := keccak.Sum256([]byte(signature))
	res, err := s.exec.Call(block, &CallRequest{To: &token, Input: sel[:4]}, tokenMetadataGas)
	if err != nil || res.Reverted || res.Halt != "" {
		return "", false
	}
	return abiStringResult(res.Return)
}

func (s *Server) tokenMetadata(block uint64, token primitives.Address) (tokenMetadata, bool) {
	st, err := s.state.StateAt(block)
	if err != nil || len(st.Code(token)) == 0 {
		return tokenMetadata{}, false
	}
	var m tokenMetadata
	var ok bool
	if m.name, ok = s.tokenString(block, token, "name()"); !ok {
		return m, false
	}
	if m.symbol, ok = s.tokenString(block, token, "symbol()"); !ok {
		return m, false
	}
	m.currency, ok = s.tokenString(block, token, "currency()")
	return m, ok
}

func tempoSimulateV1(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("payload")
	if err != nil {
		return nil, err
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '{' {
		return nil, typeError(t, "struct SimulatePayload")
	}
	req, err := simulateParam(raw)
	if err != nil {
		return nil, err
	}
	if len(req.Blocks) == 0 {
		return nil, newError(CodeInvalidParams, "calls are empty.")
	}
	ref, err := blockIDParam(p.optional("block"))
	if err != nil {
		return nil, err
	}
	n, err := s.stateBlock(ref)
	if err != nil {
		return nil, err
	}
	if s.exec == nil || s.state == nil {
		return nil, internalError("unimplemented")
	}
	blocks, err := s.exec.SimulateV1(n, req)
	if err != nil {
		return nil, executionError(err)
	}
	tokens := simulateTokens(req, blocks)
	meta := make([]tokenMetadata, len(tokens))
	known := make([]bool, len(tokens))
	for i, t := range tokens {
		meta[i], known[i] = s.tokenMetadata(n, t)
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.key("blocks")
		w.arrOpen()
		for i := range blocks {
			writeSimulatedBlock(w, &blocks[i], txDetail(req.FullTx))
		}
		w.arrClose()
		w.key("tokenMetadata")
		w.objOpen()
		for i, t := range tokens {
			if !known[i] {
				continue
			}
			w.key(field(t.Hex()))
			w.objOpen()
			w.kStr("name", meta[i].name)
			w.kStr("symbol", meta[i].symbol)
			w.kStr("currency", meta[i].currency)
			w.objClose()
		}
		w.objClose()
		w.objClose()
	}), nil
}
