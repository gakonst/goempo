package rpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/types"
)

const (
	dbKeyLength    = 33
	dbCodePrefix   = 0x63
	bytesExpecting = "a variable number of bytes represented as a hex string, an array of u8, or raw bytes"
)

func consensusSubscribe(_ *Server, _ *params) ([]byte, error) {
	return nil, newError(CodeInternal, "Internal error")
}

func operatorPeers(_ *Server, _ *params) ([]byte, error) {
	return resultEmpty, nil
}

func debugSetGCPercent(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("percent")
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if !startsWithDigit(raw) && !bytes.HasPrefix(raw, []byte("-")) {
		return nil, typeError(raw, "i32")
	}
	if _, convErr := strconv.ParseInt(string(raw), 10, 64); convErr != nil {
		return nil, typeError(raw, "i32")
	}
	if _, convErr := strconv.ParseInt(string(raw), 10, 32); convErr != nil {
		return nil, invalidParams("invalid value: integer `" + string(raw) + "`, expected i32" + column(raw))
	}
	return resultNull, nil
}

func debugStandardTraceToFile(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("block")
	if err != nil {
		return nil, err
	}
	s, err := stringParam(raw, "a string")
	if err != nil {
		return nil, err
	}
	if _, err := parseHexUint64(s); err != nil {
		return nil, err
	}
	return resultNull, nil
}

func debugDBGet(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("key")
	if err != nil {
		return nil, err
	}
	str, err := stringParam(raw, "a string")
	if err != nil {
		return nil, err
	}
	key := []byte(str)
	if body, ok := strings.CutPrefix(str, "0x"); ok {
		key, err = hex.DecodeString(body)
		if err != nil {
			return nil, newError(CodeInvalidParams, "Invalid hex key")
		}
	}
	if len(key) != dbKeyLength {
		return nil, newError(CodeInvalidParams, "Key must be 33 bytes, got "+strconv.Itoa(len(key)))
	}
	if key[0] != dbCodePrefix {
		return nil, newError(CodeInvalidParams, "Key prefix must be 0x63")
	}
	src, ok := s.state.(CodeSource)
	if !ok {
		return resultNull, nil
	}
	code, err := src.CodeByHash(primitives.Hash(key[1:]))
	if err != nil {
		return nil, internalError(err.Error())
	}
	if !code.Found {
		return resultNull, nil
	}
	return dataResult(code.Code), nil
}

func structParam(raw json.RawMessage, expected string, fields ...string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return typeError(raw, expected)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return invalidParams(err.Error())
	}
	for _, f := range fields {
		if _, ok := m[f]; !ok {
			return invalidParams("missing field `" + f + "`" + column(raw))
		}
	}
	return nil
}

func objectThenUnimplemented(expected string, fields ...string) handler {
	return func(_ *Server, p *params) ([]byte, error) {
		raw, err := p.required("params")
		if err != nil {
			return nil, err
		}
		if err := structParam(raw, expected, fields...); err != nil {
			return nil, err
		}
		return nil, internalError("unimplemented")
	}
}

func tempoFundAddress(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("address")
	if err != nil {
		return nil, err
	}
	if _, err := addressParam(raw); err != nil {
		return nil, err
	}
	return nil, internalError("faucet is not configured on this node")
}

func debugStateRootWithUpdates(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("hashed_state")
	if err != nil {
		return nil, err
	}
	if err := structParam(raw, "struct HashedPostState", "accounts", "storages"); err != nil {
		return nil, err
	}
	if _, err := blockIDParam(p.optional("block_id")); err != nil {
		return nil, err
	}
	return nil, internalError("unimplemented")
}

func debugExecutionWitness(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block")
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(raw)
	if err != nil {
		return nil, err
	}
	return s.executionWitness(ref)
}

func debugExecutionWitnessByBlockHash(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("hash")
	if err != nil {
		return nil, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return nil, err
	}
	return s.executionWitness(blockRef{kind: tagHash, hash: h})
}

func (s *Server) executionWitness(ref blockRef) ([]byte, error) {
	if s.blockFor(ref) == nil {
		return nil, blockNotFound(ref.describe())
	}
	return nil, internalError("unimplemented")
}

func debugIntermediateRoots(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block_hash")
	if err != nil {
		return nil, err
	}
	h, err := hashParam(raw)
	if err != nil {
		return nil, err
	}
	if opt := p.optional("opts"); present(opt) {
		if err := structParam(opt, "struct GethDebugTracingCallOptions"); err != nil {
			return nil, err
		}
	}
	ref := blockRef{kind: tagHash, hash: h}
	if s.blockFor(ref) == nil {
		return nil, blockNotFound(ref.describe())
	}
	return nil, internalError("unimplemented")
}

func rawBytesParam(raw json.RawMessage) ([]byte, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '"' {
		return nil, typeError(t, bytesExpecting)
	}
	return bytesParam(raw)
}

func debugTraceBlock(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("rlp_block")
	if err != nil {
		return nil, err
	}
	enc, err := rawBytesParam(raw)
	if err != nil {
		return nil, err
	}
	blk, err := types.DecodeBlock(enc)
	if err != nil {
		reason := "input too short"
		if errors.Is(err, rlp.ErrExpectedList) {
			reason = "unexpected string"
		}
		return nil, internalError("failed to decode raw block " + reason)
	}
	ref := blockRef{kind: tagHash, hash: blk.Header.Hash()}
	b := s.blockFor(ref)
	if b == nil {
		return nil, internalError("parent block not found")
	}
	return s.traceBlock(b, ref, p)
}

func traceRawTransaction(_ *Server, p *params) ([]byte, error) {
	raw, err := p.required("data")
	if err != nil {
		return nil, err
	}
	enc, err := bytesParam(raw)
	if err != nil {
		return nil, err
	}
	kinds, err := p.required("trace_types")
	if err != nil {
		return nil, err
	}
	if _, err := traceTypesParam(kinds); err != nil {
		return nil, err
	}
	if _, err := types.DecodeTransaction(enc); err != nil {
		return nil, newError(CodeInvalidParams, "failed to decode signed transaction")
	}
	return nil, internalError("unimplemented")
}
