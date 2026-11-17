package statetest

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

type Fixture struct {
	Env         envJSON                `json:"env"`
	Pre         map[string]accountJSON `json:"pre"`
	Transaction txJSON                 `json:"transaction"`
	Post        map[string][]postJSON  `json:"post"`
	Config      configJSON             `json:"config"`
}

type configJSON struct {
	ChainID      string                    `json:"chainid"`
	BlobSchedule map[string]blobParamsJSON `json:"blobSchedule"`
}

type blobParamsJSON struct {
	Target                string `json:"target"`
	Max                   string `json:"max"`
	BaseFeeUpdateFraction string `json:"baseFeeUpdateFraction"`
}

type envJSON struct {
	Coinbase      string `json:"currentCoinbase"`
	GasLimit      string `json:"currentGasLimit"`
	Number        string `json:"currentNumber"`
	Timestamp     string `json:"currentTimestamp"`
	Random        string `json:"currentRandom"`
	Difficulty    string `json:"currentDifficulty"`
	BaseFee       string `json:"currentBaseFee"`
	ExcessBlobGas string `json:"currentExcessBlobGas"`
}

type accountJSON struct {
	Balance string            `json:"balance"`
	Nonce   string            `json:"nonce"`
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

type accessTupleJSON struct {
	Address     string   `json:"address"`
	StorageKeys []string `json:"storageKeys"`
}

type authJSON struct {
	ChainID string `json:"chainId"`
	Address string `json:"address"`
	Nonce   string `json:"nonce"`
	V       string `json:"v"`
	R       string `json:"r"`
	S       string `json:"s"`
	Signer  string `json:"signer"`
	YParity string `json:"yParity"`
}

type txJSON struct {
	Nonce                string              `json:"nonce"`
	GasPrice             string              `json:"gasPrice"`
	MaxPriorityFeePerGas string              `json:"maxPriorityFeePerGas"`
	MaxFeePerGas         string              `json:"maxFeePerGas"`
	GasLimit             []string            `json:"gasLimit"`
	To                   string              `json:"to"`
	Value                []string            `json:"value"`
	Data                 []string            `json:"data"`
	AccessLists          [][]accessTupleJSON `json:"accessLists"`
	AuthorizationList    []authJSON          `json:"authorizationList"`
	Sender               string              `json:"sender"`
	BlobVersionedHashes  []string            `json:"blobVersionedHashes"`
	MaxFeePerBlobGas     string              `json:"maxFeePerBlobGas"`
}

type indexesJSON struct {
	Data  int `json:"data"`
	Gas   int `json:"gas"`
	Value int `json:"value"`
}

type postJSON struct {
	Hash            string                 `json:"hash"`
	Logs            string                 `json:"logs"`
	Indexes         indexesJSON            `json:"indexes"`
	State           map[string]accountJSON `json:"state"`
	ExpectException string                 `json:"expectException"`
}

var errBadHex = errors.New("statetest: bad hex value")

func LoadFile(path string) (map[string]Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]Fixture
	err = json.Unmarshal(raw, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parseInt(s string) (u256.Int, error) {
	t := strings.TrimPrefix(s, "0x")
	t = strings.TrimLeft(t, "0")
	if t == "" {
		return u256.Zero(), nil
	}
	return u256.FromHex(t)
}

func parseUint64(s string) (uint64, error) {
	v, err := parseInt(s)
	if err != nil {
		return 0, err
	}
	if !v.IsUint64() {
		return 0, errBadHex
	}
	return v.Uint64(), nil
}

func parseBytes(s string) ([]byte, error) {
	t := strings.TrimPrefix(s, "0x")
	if t == "" {
		return nil, nil
	}
	return primitives.DecodeHex(t)
}

func parseAddress(s string) (primitives.Address, error) {
	b, err := parseBytes(s)
	if err != nil {
		return primitives.Address{}, err
	}
	if len(b) != primitives.AddressLength {
		return primitives.Address{}, errBadHex
	}
	return primitives.Address(b), nil
}

func parseHash(s string) (primitives.Hash, error) {
	v, err := parseInt(s)
	if err != nil {
		return primitives.Hash{}, err
	}
	return primitives.Hash(v.Bytes32()), nil
}

func parseAccount(a accountJSON) (memstate.Account, error) {
	var out memstate.Account
	var err error
	out.Balance, err = parseInt(a.Balance)
	if err != nil {
		return out, err
	}
	out.Nonce, err = parseUint64(a.Nonce)
	if err != nil {
		return out, err
	}
	out.Code, err = parseBytes(a.Code)
	if err != nil {
		return out, err
	}
	out.Storage = make(map[primitives.Hash]u256.Int, len(a.Storage))
	for _, k := range slices.Sorted(maps.Keys(a.Storage)) {
		key, err := parseHash(k)
		if err != nil {
			return out, err
		}
		value, err := parseInt(a.Storage[k])
		if err != nil {
			return out, err
		}
		out.Storage[key] = value
	}
	return out, nil
}

func buildState(pre map[string]accountJSON) (*memstate.State, error) {
	st := memstate.New()
	for _, k := range slices.Sorted(maps.Keys(pre)) {
		addr, err := parseAddress(k)
		if err != nil {
			return nil, err
		}
		acc, err := parseAccount(pre[k])
		if err != nil {
			return nil, err
		}
		st.Put(addr, acc)
	}
	return st, nil
}

func buildBlock(env envJSON, fork evm.Fork) (*evm.BlockContext, error) {
	var b evm.BlockContext
	var err error
	b.Coinbase, err = parseAddress(env.Coinbase)
	if err != nil {
		return nil, err
	}
	b.GasLimit, err = parseUint64(env.GasLimit)
	if err != nil {
		return nil, err
	}
	b.Number, err = parseUint64(env.Number)
	if err != nil {
		return nil, err
	}
	b.Time, err = parseUint64(env.Timestamp)
	if err != nil {
		return nil, err
	}
	b.BaseFee, err = parseInt(env.BaseFee)
	if err != nil {
		return nil, err
	}
	randomness := env.Random
	if fork < evm.Paris {
		randomness = env.Difficulty
	}
	b.PrevRandao, err = parseHash(randomness)
	if err != nil {
		return nil, err
	}
	b.GetHash = blockHashOf
	return &b, nil
}

func blockHashOf(number uint64) primitives.Hash {
	return keccak.Sum256([]byte(strconv.FormatUint(number, 10)))
}
