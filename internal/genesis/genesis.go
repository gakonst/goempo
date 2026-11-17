package genesis

import (
	_ "embed"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"sort"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/trie"
)

//go:embed presto.json
var mainnetJSON []byte

const MainnetChainID = 4217

var MainnetStateRoot = primitives.Hash{
	0x83, 0x40, 0x89, 0x74, 0x32, 0x3f, 0x63, 0xab, 0x96, 0x9b, 0x23, 0xf0, 0xfe, 0x1d, 0xba, 0x30,
	0xd7, 0xee, 0x5d, 0xc5, 0xc5, 0x24, 0xa9, 0x75, 0xba, 0xe3, 0x81, 0x87, 0xea, 0xa2, 0xc7, 0xf6,
}

const accountsMax = 1 << 20

const slotsMax = 1 << 24

const _ = uint(slotsMax - accountsMax)

const _ = uint(math.MaxInt32 - slotsMax)

var (
	ErrQuantity     = errors.New("genesis: quantity is not 0x-prefixed hex of at most 32 bytes")
	ErrTooMany      = errors.New("genesis: alloc holds more accounts or slots than the limit")
	ErrMissingChain = errors.New("genesis: config has no chainId")
)

type Word [32]byte

type Slot struct {
	Key   primitives.Hash
	Value Word
}

type Account struct {
	Address primitives.Address
	Nonce   uint64
	Balance Word
	Code    []byte
	Storage []Slot
}

type Config struct {
	ChainID   uint64
	ForkTimes map[string]uint64
	Raw       json.RawMessage
}

type Genesis struct {
	Config     Config
	Nonce      uint64
	Timestamp  uint64
	ExtraData  []byte
	GasLimit   uint64
	Difficulty Word
	MixHash    primitives.Hash
	Coinbase   primitives.Address
	BaseFee    uint64
	Alloc      []Account
}

type fileAccount struct {
	Nonce   string            `json:"nonce"`
	Balance string            `json:"balance"`
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

type file struct {
	Config        json.RawMessage        `json:"config"`
	Nonce         string                 `json:"nonce"`
	Timestamp     string                 `json:"timestamp"`
	ExtraData     string                 `json:"extraData"`
	GasLimit      string                 `json:"gasLimit"`
	Difficulty    string                 `json:"difficulty"`
	MixHash       primitives.Hash        `json:"mixHash"`
	Coinbase      primitives.Address     `json:"coinbase"`
	BaseFeePerGas string                 `json:"baseFeePerGas"`
	Alloc         map[string]fileAccount `json:"alloc"`
}

func Mainnet() (*Genesis, error) {
	return Parse(mainnetJSON)
}

func MainnetJSON() []byte {
	return mainnetJSON
}

func Parse(raw []byte) (*Genesis, error) {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	g := &Genesis{MixHash: f.MixHash, Coinbase: f.Coinbase}
	config, err := parseConfig(f.Config)
	if err != nil {
		return nil, err
	}
	g.Config = config
	header := []struct {
		in  string
		out *uint64
	}{
		{in: f.Nonce, out: &g.Nonce},
		{in: f.Timestamp, out: &g.Timestamp},
		{in: f.GasLimit, out: &g.GasLimit},
		{in: f.BaseFeePerGas, out: &g.BaseFee},
	}
	for _, field := range header {
		if field.in == "" {
			continue
		}
		v, err := primitives.DecodeHexUint64(field.in)
		if err != nil {
			return nil, err
		}
		*field.out = v
	}
	if g.Difficulty, err = ParseWord(f.Difficulty); err != nil {
		return nil, err
	}
	if g.ExtraData, err = primitives.DecodeHex(f.ExtraData); err != nil {
		return nil, err
	}
	if len(f.Alloc) > accountsMax {
		return nil, ErrTooMany
	}
	g.Alloc, err = parseAlloc(f.Alloc)
	if err != nil {
		return nil, err
	}
	return g, nil
}

func parseConfig(raw json.RawMessage) (Config, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Config{}, err
	}
	c := Config{ForkTimes: map[string]uint64{}, Raw: raw}
	chain, ok := fields["chainId"]
	if !ok {
		return Config{}, ErrMissingChain
	}
	if err := json.Unmarshal(chain, &c.ChainID); err != nil {
		return Config{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		value := fields[name]
		if len(name) < len("Time") {
			continue
		}
		if name[len(name)-len("Time"):] != "Time" {
			continue
		}
		var at uint64
		if err := json.Unmarshal(value, &at); err != nil {
			return Config{}, err
		}
		c.ForkTimes[name[:len(name)-len("Time")]] = at
	}
	return c, nil
}

func parseAlloc(in map[string]fileAccount) ([]Account, error) {
	out := make([]Account, 0, len(in))
	slots := 0
	for _, key := range slices.Sorted(maps.Keys(in)) {
		fa := in[key]
		addr, err := primitives.HexToAddress(key)
		if err != nil {
			return nil, err
		}
		acct := Account{Address: addr}
		if fa.Nonce != "" {
			if acct.Nonce, err = primitives.DecodeHexUint64(fa.Nonce); err != nil {
				return nil, err
			}
		}
		if acct.Balance, err = ParseWord(fa.Balance); err != nil {
			return nil, err
		}
		if acct.Code, err = primitives.DecodeHex(fa.Code); err != nil {
			return nil, err
		}
		slots += len(fa.Storage)
		if slots > slotsMax {
			return nil, ErrTooMany
		}
		for _, slotKey := range slices.Sorted(maps.Keys(fa.Storage)) {
			slotValue := fa.Storage[slotKey]
			k, err := ParseWord(slotKey)
			if err != nil {
				return nil, err
			}
			v, err := ParseWord(slotValue)
			if err != nil {
				return nil, err
			}
			acct.Storage = append(acct.Storage, Slot{Key: primitives.Hash(k), Value: v})
		}
		sort.Slice(acct.Storage, func(i, j int) bool {
			return acct.Storage[i].Key.Compare(acct.Storage[j].Key) < 0
		})
		out = append(out, acct)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Address.Compare(out[j].Address) < 0
	})
	return out, nil
}

func ParseWord(s string) (Word, error) {
	var w Word
	if s == "" {
		return w, nil
	}
	digits := primitives.TrimHexPrefix(s)
	if len(digits) == len(s) {
		return w, ErrQuantity
	}
	if len(digits) > 2*len(w) {
		return w, ErrQuantity
	}
	padded := make([]byte, 2*len(w))
	for i := range padded {
		padded[i] = '0'
	}
	copy(padded[len(padded)-len(digits):], digits)
	if err := primitives.DecodeHexFixed(w[:], string(padded)); err != nil {
		return w, err
	}
	return w, nil
}

func (a *Account) StorageRoot() primitives.Hash {
	t := trie.NewSecure()
	enc := rlp.NewEncoder(33)
	for _, slot := range a.Storage {
		if slot.Value == (Word{}) {
			continue
		}
		enc.Reset()
		enc.WriteUintBytes(slot.Value[:])
		t.Update(slot.Key[:], enc.Encoded())
	}
	return t.Hash()
}

func (a *Account) EncodeRLP(storageRoot primitives.Hash) []byte {
	codeHash := keccak.Sum256(a.Code)
	enc := rlp.NewEncoder(110)
	enc.ListStart()
	enc.WriteUint64(a.Nonce)
	enc.WriteUintBytes(a.Balance[:])
	enc.WriteBytes(storageRoot[:])
	enc.WriteBytes(codeHash[:])
	enc.ListEnd()
	return enc.Encoded()
}

func (g *Genesis) StateRoot() primitives.Hash {
	t := trie.NewSecure()
	for i := range g.Alloc {
		acct := &g.Alloc[i]
		t.Update(acct.Address[:], acct.EncodeRLP(acct.StorageRoot()))
	}
	return t.Hash()
}
