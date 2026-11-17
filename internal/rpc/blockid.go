package rpc

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

type tagKind int

const (
	tagNumber tagKind = iota
	tagLatest
	tagEarliest
	tagPending
	tagSafe
	tagFinalized
	tagHash
)

type blockRef struct {
	kind   tagKind
	number uint64
	hash   primitives.Hash
}

func tagOf(s string) (tagKind, bool) {
	switch s {
	case "latest":
		return tagLatest, true
	case "earliest":
		return tagEarliest, true
	case "pending":
		return tagPending, true
	case "safe":
		return tagSafe, true
	case "finalized":
		return tagFinalized, true
	}
	return tagNumber, false
}

func blockNumberParam(raw json.RawMessage) (blockRef, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		col := len(raw)
		if opensContainer(raw) {
			col = 0
		}
		return blockRef{}, invalidParams("invalid type: " + unexpected(raw) + ", expected a string at line 1 column " + strconv.Itoa(col))
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return blockRef{}, invalidParams("invalid hex string")
	}
	if k, ok := tagOf(s); ok {
		return blockRef{kind: k}, nil
	}
	n, err := parseHexUint64(s)
	if err != nil {
		return blockRef{}, err
	}
	return blockRef{kind: tagNumber, number: n}, nil
}

type blockIDObject struct {
	BlockHash        *string `json:"blockHash"`
	BlockNumber      *string `json:"blockNumber"`
	RequireCanonical *bool   `json:"requireCanonical"`
}

func blockIDParam(raw json.RawMessage) (blockRef, error) {
	if raw == nil {
		return blockRef{kind: tagLatest}, nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		return blockIDObjectParam(raw)
	}
	if len(raw) == 0 || raw[0] != '"' {
		return blockRef{}, typeError(raw, "a string")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && len(s) == 2+2*len(primitives.Hash{}) {
		h, err := hashParam(raw)
		if err != nil {
			return blockRef{}, err
		}
		return blockRef{kind: tagHash, hash: h}, nil
	}
	ref, err := blockNumberParam(raw)
	if err != nil {
		return blockRef{}, invalidParams(errDetail(err) + column(raw))
	}
	return ref, nil
}

func blockIDObjectParam(raw json.RawMessage) (blockRef, error) {
	var obj blockIDObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return blockRef{}, invalidParams("invalid block id")
	}
	if obj.BlockHash != nil {
		h, err := hashParam(json.RawMessage(strconv.Quote(*obj.BlockHash)))
		if err != nil {
			return blockRef{}, err
		}
		return blockRef{kind: tagHash, hash: h}, nil
	}
	if obj.BlockNumber != nil {
		return blockNumberParam(json.RawMessage(strconv.Quote(*obj.BlockNumber)))
	}
	return blockRef{}, invalidParams("expected `blockNumber` or `blockHash`" + column(raw))
}

func (r blockRef) describe() string {
	if r.kind == tagHash {
		return "hash " + r.hash.Hex()
	}
	return "0x" + strconv.FormatUint(r.number, 16)
}

func (s *Server) resolve(r blockRef) (uint64, bool) {
	switch r.kind {
	case tagNumber:
		return r.number, true
	case tagEarliest:
		return 0, true
	case tagPending:
		return 0, false
	case tagHash:
		return s.chain.BlockNumber(r.hash)
	case tagLatest, tagSafe, tagFinalized:
		return s.chain.Head(), true
	default:
		assert.Unreachable("tagKind: unhandled value")
	}
	return 0, false
}

func (s *Server) blockFor(r blockRef) *BlockData {
	n, ok := s.resolve(r)
	if !ok {
		return nil
	}
	if r.kind != tagHash && n > s.chain.Head() {
		return nil
	}
	b, ok := s.chain.Block(n)
	if !ok {
		return nil
	}
	return b
}

func (s *Server) stateBlock(r blockRef) (uint64, error) {
	if r.kind == tagPending {
		r = blockRef{kind: tagLatest}
	}
	n, ok := s.resolve(r)
	if !ok {
		return 0, blockNotFound(r.describe())
	}
	if n > s.chain.Head() {
		return 0, blockNotFound(r.describe())
	}
	return n, nil
}

func (s *Server) stateAt(r blockRef) (StateReader, error) {
	n, err := s.stateBlock(r)
	if err != nil {
		return nil, err
	}
	if s.state == nil {
		return nil, newError(CodeServer, "state is not available")
	}
	st, err := s.state.StateAt(n)
	if err != nil {
		return nil, newError(CodeServer, err.Error())
	}
	return st, nil
}

func indexParam(raw json.RawMessage) (uint64, error) {
	raw = bytes.TrimSpace(raw)
	s, err := stringParam(raw, "a string")
	if err != nil {
		return 0, err
	}
	body := trim0x(s)
	v, perr := strconv.ParseUint(body, 16, 64)
	if perr == nil {
		return v, nil
	}
	detail := "invalid digit found in string"
	if body == "" {
		detail = "cannot parse integer from empty string"
	} else if allHex(body) {
		detail = "number too large to fit in target type"
	}
	return 0, invalidParams("Failed to parse hex encoded index value: " + detail + column(raw))
}

func storageKeyParam(raw json.RawMessage) (primitives.Hash, error) {
	var h primitives.Hash
	bad := invalidParams("data did not match any variant of untagged enum JsonStorageKey")
	raw = bytes.TrimSpace(raw)
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return h, bad
	}
	body := trim0x(s)
	if len(body) > 2*len(h) || !allHex(body) {
		return h, bad
	}
	if len(body)%2 == 1 {
		body = "0" + body
	}
	b, err := primitives.DecodeHex(body)
	if err != nil {
		return h, bad
	}
	copy(h[len(h)-len(b):], b)
	return h, nil
}
