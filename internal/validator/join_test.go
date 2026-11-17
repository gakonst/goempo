package validator_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/validator"
	"github.com/kapetan-io/tiger/assert"
)

const (
	moderatoChain = 42431
	joinOwner     = "0x00000000000000000000000000000000000000aa"
	joinCount     = 45
	joinGas       = 2338306
)

var (
	sigOwner       = sel("owner()")
	sigInitialized = sel("isInitialized()")
	sigCount       = sel("validatorCount()")
	sigByKey       = sel("validatorByPublicKey(bytes32)")
)

func sel(sig string) string {
	h := keccak.Sum256([]byte(sig))
	return hex.EncodeToString(h[:4])
}

var errReverted = errors.New("execution reverted: ValidatorNotFound()")

type fakeRPC struct {
	chain      uint64
	registered bool
	added      []string
	calls      int
}

type callArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
	Data string `json:"data"`
}

func wordHex(v uint64) string {
	b := make([]byte, word)
	for i := range 8 {
		b[word-1-i] = byte(v >> (8 * i))
	}
	return hex.EncodeToString(b)
}

func result(s string) (json.RawMessage, error) { return json.Marshal("0x" + s) }

func (f *fakeRPC) Call(method string, params ...any) (json.RawMessage, error) {
	f.calls++
	switch method {
	case "eth_chainId":
		return json.Marshal("0xa5bf")
	case "eth_estimateGas":
		return json.Marshal("0x23ae02")
	case "eth_call":
	default:
		return nil, errors.New("unexpected " + method)
	}
	raw, err := json.Marshal(params[0])
	if err != nil {
		return nil, err
	}
	var a callArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	data := strings.TrimPrefix(a.Data, "0x")
	switch data[:8] {
	case sigInitialized:
		return result(wordHex(1))
	case sigOwner:
		return result(strings.Repeat("0", 24) + strings.TrimPrefix(joinOwner, "0x"))
	case sigCount:
		return result(wordHex(joinCount))
	case sigByKey:
		if f.registered {
			return result(wordHex(0))
		}
		return nil, errReverted
	case selectorAdd:
		f.added = append(f.added, a.From)
		return result(wordHex(joinCount))
	default:
		return nil, errors.New("unexpected selector " + data[:8])
	}
}

func TestCheckJoinSimulatesFromOwner(_ *testing.T) {
	k, err := validator.ParseSigningKey(rustSigningKey)
	assert.NoError(err, "key")
	fee := primitives.Address{9}
	r, err := validator.Registration{ChainID: moderatoChain, Validator: primitives.Address{1}, PublicKey: k.Public(), Ingress: "203.0.113.7:8000", Egress: "203.0.113.7"}.Canonical()
	assert.NoError(err, "registration")
	sig := r.SignAdd(k, fee)
	f := &fakeRPC{}
	j, err := validator.CheckJoin(f, r, fee, sig[:])
	assert.NoError(err, "check join")
	assert.Equal(j.ChainID, uint64(moderatoChain), "chain")
	assert.Equal(j.Owner.Hex(), joinOwner, "owner read from the config")
	assert.Equal(j.Count, uint64(joinCount), "validator count")
	assert.Equal(j.SimulatedIndex, uint64(joinCount), "next index")
	assert.Equal(j.Gas, uint64(joinGas), "gas")
	assert.Equal(len(f.added), 1, "addValidator simulated once")
	assert.Equal(f.added[0], joinOwner, "simulated from the owner")
	assert.Equal(hex.EncodeToString(j.Calldata), hex.EncodeToString(r.AddCalldata(fee, sig[:])), "calldata")
	_, err = validator.CheckJoin(&fakeRPC{registered: true}, r, fee, sig[:])
	assert.Ok(errors.Is(err, validator.ErrJoinRegistered), "a registered key is refused")
	_, err = validator.CheckJoin(&fakeRPC{}, r, primitives.Address{8}, sig[:])
	assert.Ok(errors.Is(err, validator.ErrSignature), "a bad signature is refused before any simulation")
	r.ChainID = 4217
	_, err = validator.CheckJoin(&fakeRPC{}, r, fee, sig[:])
	assert.Ok(errors.Is(err, validator.ErrJoinChain), "a signature for another chain is refused")
}
