package rpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	uint64HexDigits = 16
	u256HexDigits   = 64
)

const _ = uint(u256HexDigits - uint64HexDigits)

type params struct {
	items  []json.RawMessage
	fields map[string]json.RawMessage
	raw    []byte
	pos    int
}

func newParams(raw []byte) (*params, error) {
	p := &params{raw: raw}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, nullID) {
		return p, nil
	}
	if trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &p.fields); err != nil {
			return nil, invalidParams("invalid type: map, expected a sequence")
		}
		return p, nil
	}
	if trimmed[0] != '[' {
		return nil, invalidParams("invalid type: " + unexpected(trimmed) + ", expected a sequence")
	}
	if err := json.Unmarshal(trimmed, &p.items); err != nil {
		return nil, invalidParams("invalid type, expected a sequence")
	}
	return p, nil
}

func (p *params) required(name string) (json.RawMessage, error) {
	if p.fields != nil {
		v, ok := p.fields[name]
		if !ok {
			return nil, invalidParams("missing field `" + name + "`" + column(bytes.TrimSpace(p.raw)))
		}
		return v, nil
	}
	if p.pos >= len(p.items) {
		return nil, invalidParams("No more params")
	}
	v := p.items[p.pos]
	p.pos++
	return v, nil
}

func (p *params) optional(name string) json.RawMessage {
	var v json.RawMessage
	if p.fields != nil {
		v = p.fields[name]
	} else if p.pos < len(p.items) {
		v = p.items[p.pos]
		p.pos++
	}
	if isNull(v) {
		return nil
	}
	return v
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, nullID)
}

func opensContainer(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	return raw[0] == '{' || raw[0] == '['
}

func cut0x(s string) (string, bool) {
	if body, ok := strings.CutPrefix(s, "0x"); ok {
		return body, true
	}
	return strings.CutPrefix(s, "0X")
}

func trim0x(s string) string {
	body, _ := cut0x(s)
	return body
}

func unexpected(raw []byte) string {
	if len(raw) == 0 {
		return "null"
	}
	switch raw[0] {
	case '{':
		return "map"
	case '[':
		return "sequence"
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return "string"
		}
		return "string " + strconv.Quote(s)
	case 't', 'f':
		return "boolean `" + string(raw) + "`"
	case 'n':
		return "null"
	}
	if bytes.ContainsAny(raw, ".eE") {
		return "floating point `" + string(raw) + "`"
	}
	return "integer `" + string(raw) + "`"
}

func column(raw []byte) string {
	return " at line 1 column " + strconv.Itoa(len(raw))
}

func typeError(raw []byte, expected string) *Error {
	raw = bytes.TrimSpace(raw)
	col := len(raw)
	if opensContainer(raw) {
		col = 1
	}
	return invalidParams("invalid type: " + unexpected(raw) + ", expected " + expected + " at line 1 column " + strconv.Itoa(col))
}

func stringParam(raw json.RawMessage, expected string) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", typeError(raw, expected)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", typeError(raw, expected)
	}
	return s, nil
}

func isHexDigit(c byte) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	case 'A' <= c && c <= 'F':
		return true
	}
	return false
}

func allHex(s string) bool {
	for i := range len(s) {
		if !isHexDigit(s[i]) {
			return false
		}
	}
	return true
}

func decodeHexString(raw json.RawMessage, s string) ([]byte, error) {
	body := trim0x(s)
	col := column(bytes.TrimSpace(raw))
	if len(body)%2 == 1 {
		return nil, invalidParams("odd number of digits" + col)
	}
	out, err := hex.DecodeString(body)
	if err == nil {
		return out, nil
	}
	for i := range len(body) {
		if !isHexDigit(body[i]) {
			return nil, invalidParams("invalid character " + strconv.QuoteRune(rune(body[i])) + " at position " + strconv.Itoa(i) + col)
		}
	}
	return nil, invalidParams("invalid hex string" + col)
}

func fixedParam(raw json.RawMessage, size int) ([]byte, error) {
	s, err := stringParam(raw, "a valid hex string")
	if err != nil {
		return nil, err
	}
	if len(trim0x(s)) != 2*size {
		return nil, invalidParams("invalid string length" + column(bytes.TrimSpace(raw)))
	}
	return decodeHexString(raw, s)
}

func addressParam(raw json.RawMessage) (primitives.Address, error) {
	var a primitives.Address
	b, err := fixedParam(raw, len(a))
	if err != nil {
		return a, err
	}
	copy(a[:], b)
	return a, nil
}

func hashParam(raw json.RawMessage) (primitives.Hash, error) {
	var h primitives.Hash
	b, err := fixedParam(raw, len(h))
	if err != nil {
		return h, err
	}
	copy(h[:], b)
	return h, nil
}

func bytesParam(raw json.RawMessage) ([]byte, error) {
	s, err := stringParam(raw, "a valid hex string")
	if err != nil {
		return nil, err
	}
	b, err := decodeHexString(raw, s)
	if err != nil {
		return nil, invalidParams("invalid value: string " + strconv.Quote(s) + ", expected a valid hex string" + column(bytes.TrimSpace(raw)))
	}
	return b, nil
}

func boolParam(raw json.RawMessage) (jbool, error) {
	raw = bytes.TrimSpace(raw)
	switch string(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, typeError(raw, "a boolean")
}

func parseHexUint64(s string) (uint64, error) {
	body, ok := cut0x(s)
	if !ok {
		return 0, invalidParams("hex string without 0x prefix")
	}
	if body == "" {
		return 0, invalidParams("cannot parse integer from empty string")
	}
	if !allHex(body) {
		return 0, invalidParams("invalid digit found in string")
	}
	v, err := strconv.ParseUint(body, 16, 64)
	if err != nil {
		return 0, invalidParams("number too large to fit in target type")
	}
	return v, nil
}

func startsWithDigit(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	return '0' <= raw[0] && raw[0] <= '9'
}

func quantityParam(raw json.RawMessage) (uint64, error) {
	raw = bytes.TrimSpace(raw)
	if startsWithDigit(raw) {
		v, err := strconv.ParseUint(string(raw), 10, 64)
		if err != nil {
			return 0, typeError(raw, "a hex string")
		}
		return v, nil
	}
	s, err := stringParam(raw, "a hex string")
	if err != nil {
		return 0, err
	}
	v, err := parseHexUint64(s)
	if err != nil {
		return 0, invalidParams(errDetail(err) + column(raw))
	}
	return v, nil
}

func quantity256Param(raw json.RawMessage) (u256.Int, error) {
	raw = bytes.TrimSpace(raw)
	if startsWithDigit(raw) {
		v, err := strconv.ParseUint(string(raw), 10, 64)
		if err != nil {
			return u256.Int{}, typeError(raw, "a hex string")
		}
		return u256.FromUint64(v), nil
	}
	s, err := stringParam(raw, "a hex string")
	if err != nil {
		return u256.Int{}, err
	}
	body, ok := cut0x(s)
	if !ok {
		return u256.Int{}, invalidParams("invalid hex string" + column(raw))
	}
	if len(body) > u256HexDigits {
		return u256.Int{}, invalidParams("number too large to fit in target type" + column(raw))
	}
	if len(body)%2 == 1 {
		body = "0" + body
	}
	b, err := hex.DecodeString(body)
	if err != nil {
		return u256.Int{}, invalidParams("invalid digit found in string" + column(raw))
	}
	return u256.FromBytes(b), nil
}
