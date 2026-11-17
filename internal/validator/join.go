package validator

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/primitives"
)

const (
	quantityBase = 16
	quantityBits = 64
)

var (
	ErrJoinChain      = errors.New("validator: rpc chain id differs from the registration's")
	ErrJoinNotInit    = errors.New("validator: ValidatorConfigV2 is not initialized on this chain")
	ErrJoinRegistered = errors.New("validator: public key is already registered")
	ErrJoinWord       = errors.New("validator: eth_call result is not one ABI word")
)

var (
	selectorOwner       = selector("owner()")
	selectorInitialized = selector("isInitialized()")
	selectorCount       = selector("validatorCount()")
	selectorByKey       = selector("validatorByPublicKey(bytes32)")
)

type Caller interface {
	Call(method string, params ...any) (json.RawMessage, error)
}

type JoinCheck struct {
	ChainID        uint64
	Owner          primitives.Address
	Count          uint64
	SimulatedIndex uint64
	Gas            uint64
	Calldata       []byte
}

type callArgs struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	Data string `json:"data"`
}

func hexBytes(b []byte) string { return "0x" + hex.EncodeToString(b) }

func viewCall(c Caller, from string, data []byte) ([]byte, error) {
	raw, err := c.Call("eth_call", callArgs{From: from, To: hexBytes(ValidatorConfigV2[:]), Data: hexBytes(data)}, "latest")
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

func quantity(raw json.RawMessage) (uint64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), quantityBase, quantityBits)
}

func word(b []byte) ([wordBytes]byte, error) {
	var w [wordBytes]byte
	if len(b) != wordBytes {
		return w, ErrJoinWord
	}
	copy(w[:], b)
	return w, nil
}

func wordU64(b []byte) (uint64, error) {
	w, err := word(b)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(w[wordBytes-u64Bytes:]), nil
}

func CheckJoin(c Caller, r Registration, feeRecipient primitives.Address, sig []byte) (JoinCheck, error) {
	var out JoinCheck
	raw, err := c.Call("eth_chainId")
	if err != nil {
		return out, err
	}
	if out.ChainID, err = quantity(raw); err != nil {
		return out, err
	}
	if out.ChainID != r.ChainID {
		return out, fmt.Errorf("%w: rpc %d, registration %d", ErrJoinChain, out.ChainID, r.ChainID)
	}
	if err := r.CheckAdd(feeRecipient, sig); err != nil {
		return out, err
	}
	b, err := viewCall(c, "", selectorInitialized[:])
	if err != nil {
		return out, err
	}
	if init, err := wordU64(b); err != nil || init != 1 {
		return out, errors.Join(ErrJoinNotInit, err)
	}
	if b, err = viewCall(c, "", selectorOwner[:]); err != nil {
		return out, err
	}
	w, err := word(b)
	if err != nil {
		return out, err
	}
	copy(out.Owner[:], w[wordBytes-addressBytes:])
	if b, err = viewCall(c, "", selectorCount[:]); err != nil {
		return out, err
	}
	if out.Count, err = wordU64(b); err != nil {
		return out, err
	}
	byKey := append(selectorByKey[:], r.PublicKey[:]...)
	if _, err := viewCall(c, "", byKey); err == nil {
		return out, ErrJoinRegistered
	}
	out.Calldata = r.AddCalldata(feeRecipient, sig)
	owner := hexBytes(out.Owner[:])
	if b, err = viewCall(c, owner, out.Calldata); err != nil {
		return out, fmt.Errorf("addValidator simulated from the owner: %w", err)
	}
	if out.SimulatedIndex, err = wordU64(b); err != nil {
		return out, err
	}
	raw, err = c.Call("eth_estimateGas", callArgs{From: owner, To: hexBytes(ValidatorConfigV2[:]), Data: hexBytes(out.Calldata)})
	if err != nil {
		return out, err
	}
	out.Gas, err = quantity(raw)
	return out, err
}
