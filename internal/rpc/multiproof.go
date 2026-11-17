package rpc

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
)

const (
	multiProofPair   = 2
	columnMarker     = " at line 1 column "
	hashKeyExpecting = "32 bytes, represented as a hex string of length 64, an array of u8, or raw bytes"
)

type span struct {
	start int
	end   int
}

type jsonElem struct {
	off int
	raw json.RawMessage
}

func arrayElems(raw []byte) ([]jsonElem, bool) {
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil, false
	}
	var out []jsonElem
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 1; i < len(raw)-1; i++ {
		c := raw[i]
		switch {
		case inStr && esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case inStr:
			inStr = c != '"'
		case c == '"':
			inStr = true
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, trimElem(raw, span{start: start, end: i}))
			start = -1
			continue
		}
		if start < 0 && c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, trimElem(raw, span{start: start, end: len(raw) - 1}))
	}
	return out, true
}

func trimElem(raw []byte, sp span) jsonElem {
	if sp.start < 0 {
		return jsonElem{off: sp.end}
	}
	return jsonElem{off: sp.start, raw: bytes.TrimRight(raw[sp.start:sp.end], " \t\r\n")}
}

func shiftColumn(err error, off int) error {
	e, ok := err.(*Error)
	if !ok || off == 0 {
		return err
	}
	var data string
	if json.Unmarshal(e.Data, &data) != nil {
		return err
	}
	i := strings.LastIndex(data, columnMarker)
	if i < 0 {
		return err
	}
	n, convErr := strconv.Atoi(data[i+len(columnMarker):])
	if convErr != nil {
		return err
	}
	return invalidParams(data[:i] + columnMarker + strconv.Itoa(n+off))
}

func hashKeyParam(raw json.RawMessage) (primitives.Hash, error) {
	var h primitives.Hash
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return h, typeError(raw, hashKeyExpecting)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return h, typeError(raw, hashKeyExpecting)
	}
	if len(trim0x(s))%2 == 1 {
		return h, invalidParams("odd number of digits" + column(raw))
	}
	return hashParam(raw)
}

type multiProofTarget struct {
	addr primitives.Address
	keys []storageKeyArg
}

func multiProofTargets(raw json.RawMessage) ([]multiProofTarget, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		return nil, invalidParams("invalid type: map, expected a sequence at line 1 column 0")
	}
	elems, ok := arrayElems(raw)
	if !ok {
		return nil, typeError(raw, "a sequence")
	}
	out := make([]multiProofTarget, 0, len(elems))
	for _, e := range elems {
		t, err := multiProofPairParam(e.raw)
		if err != nil {
			return nil, shiftColumn(err, e.off)
		}
		out = append(out, t)
	}
	return out, nil
}

func multiProofPairParam(raw json.RawMessage) (multiProofTarget, error) {
	var t multiProofTarget
	parts, ok := arrayElems(raw)
	if !ok {
		return t, typeError(raw, "a tuple of size 2")
	}
	if len(parts) < multiProofPair {
		return t, invalidParams("invalid length " + strconv.Itoa(len(parts)) + ", expected a tuple of size 2" + column(raw))
	}
	if len(parts) > multiProofPair {
		return t, invalidParams("trailing characters at line 1 column " + strconv.Itoa(parts[multiProofPair].off+1))
	}
	addr, err := addressParam(parts[0].raw)
	if err != nil {
		return t, shiftColumn(err, parts[0].off)
	}
	t.addr = addr
	keys, ok := arrayElems(parts[1].raw)
	if !ok {
		return t, shiftColumn(typeError(parts[1].raw, "a sequence"), parts[1].off)
	}
	for _, k := range keys {
		h, err := hashKeyParam(k.raw)
		if err != nil {
			return t, shiftColumn(err, parts[1].off+k.off)
		}
		t.keys = append(t.keys, storageKeyArg{hash: h, text: h.Hex()})
	}
	return t, nil
}

func ethGetMultiProof(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("targets")
	if err != nil {
		return nil, err
	}
	targets, err := multiProofTargets(raw)
	if err != nil {
		return nil, err
	}
	at, err := s.proofWindow(p, "block_number")
	if err != nil {
		return nil, err
	}
	proofs := make([]AccountProof, len(targets))
	for i, t := range targets {
		proofs[i], err = s.accountProof(at, t.addr, t.keys)
		if err != nil {
			return nil, err
		}
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for i := range proofs {
			writeAccountProof(w, targets[i].addr, &proofs[i])
		}
		w.arrClose()
	}), nil
}
