package precompiles_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type verifierFrame struct {
	Source    string `json:"source"`
	ChainID   uint64 `json:"chainId"`
	Timestamp uint64 `json:"timestamp"`
	From      string `json:"from"`
	Input     string `json:"input"`
	Gas       uint64 `json:"gas"`
	GasUsed   uint64 `json:"gasUsed"`
	Output    string `json:"output"`
	Reverted  bool   `json:"reverted"`
}

func loadVerifierFrames(t *testing.T) []verifierFrame {
	raw, err := os.ReadFile("testdata/zone_verifier/frames.json")
	assert.NoError(err, "read")
	var frames []verifierFrame
	assert.NoError(json.Unmarshal(raw, &frames), "parse")
	assert.Ok(len(frames) > 0, "fixtures present")
	return frames
}

type verifierCall struct {
	frame  verifierFrame
	input  []byte
	caller string
	fork   hardfork.Fork
	time   uint64
}

type verifierResult struct {
	gasUsed uint64
	out     []byte
}

func runVerifier(v verifierCall) (verifierResult, error) {
	c := precompiles.NewContext(memstate.New(), v.fork, v.frame.Gas)
	c.Block.Time = v.time
	c.ChainID = u256.FromUint64(v.frame.ChainID)
	c.Caller = mustAddr(v.caller)
	out, err := precompiles.Call(c, precompiles.ZoneVerifierAddress, v.input)
	return verifierResult{gasUsed: c.GasUsed(), out: out}, err
}

func frameCall(f verifierFrame) verifierCall {
	return verifierCall{
		frame: f, input: mustBytes(f.Input), caller: f.From,
		fork: hardfork.At(hardfork.ChainID(f.ChainID), f.Timestamp), time: f.Timestamp,
	}
}

func TestZoneVerifierRealFrames(t *testing.T) {
	for _, f := range loadVerifierFrames(t) {
		t.Run(f.Source[:18], func(t *testing.T) {
			res, err := runVerifier(frameCall(f))
			assert.NoError(err, "verify call")
			assert.Equal(res.gasUsed, f.GasUsed, "gas used")
			assert.Equal("0x"+hex.EncodeToString(res.out), f.Output, "output")
		})
	}
}

const verifierHeadWords = 15

func inputWordCost(n int) uint64 { return uint64((n+31)/32) * 30 }

func verifierProofStart(input []byte) int {
	off := 4 + 32*16
	at := 4 + int(new(big.Int).SetBytes(input[off:off+32]).Int64())
	return at + 32
}

type verifierInput struct {
	head   []byte
	config []byte
	proof  []byte
}

func verifierEncode(in verifierInput) []byte {
	head, config, proof := in.head, in.config, in.proof
	out := append([]byte{}, head[:4+32*verifierHeadWords]...)
	pad := func(b []byte) []byte { return append(append([]byte{}, b...), make([]byte, (32-len(b)%32)%32)...) }
	cfgAt := uint64(17 * 32)
	proofAt := cfgAt + 32 + uint64(len(pad(config)))
	out = append(out, amount(cfgAt)...)
	out = append(out, amount(proofAt)...)
	out = append(out, amount(uint64(len(config)))...)
	out = append(out, pad(config)...)
	out = append(out, amount(uint64(len(proof)))...)
	return append(out, pad(proof)...)
}

type verifierCase struct {
	name    string
	mutate  func(*verifierCall)
	gas     func(f verifierFrame, v verifierCall) uint64
	output  byte
	reverts bool
}

func inputOnly(f verifierFrame, v verifierCall) uint64 { return inputWordCost(len(v.input)) }

func fullGas(f verifierFrame, v verifierCall) uint64 { return f.GasUsed }

func baseOnly(f verifierFrame, v verifierCall) uint64 { return inputWordCost(len(v.input)) + 40_000 }

func TestZoneVerifierMutations(t *testing.T) {
	f := loadVerifierFrames(t)[0]
	cases := []verifierCase{
		{"real frame", func(v *verifierCall) {}, fullGas, 1, false},
		{"wrong caller", func(v *verifierCall) { v.caller = "0x5ad0000000000000000000000000000000000009" }, inputOnly, 0, false},
		{"altered tempo block number", func(v *verifierCall) { v.input[4+32+31] ^= 1 }, fullGas, 0, false},
		{"altered withdrawal queue hash", func(v *verifierCall) { v.input[4+32*14+31] ^= 1 }, fullGas, 0, false},
		{"other chain id", func(v *verifierCall) { v.frame.ChainID++ }, fullGas, 0, false},
		{"corrupt document signature", func(v *verifierCall) {
			p := verifierProofStart(v.input)
			n := int(new(big.Int).SetBytes(v.input[p-32 : p]).Int64())
			v.input[p+n-1] ^= 1
		}, fullGas, 0, false},
		{"malformed cbor", func(v *verifierCall) { v.input[verifierProofStart(v.input)] = 0x00 }, baseOnly, 0, false},
		{"no proof mode", func(v *verifierCall) { v.input = verifierEncode(verifierInput{head: v.input, config: []byte{0x02}}) }, inputOnly, 1, false},
		{"no proof mode with proof", func(v *verifierCall) {
			v.input = verifierEncode(verifierInput{head: v.input, config: []byte{0x02}, proof: []byte{1}})
		}, inputOnly, 0, false},
		{"nitro mode without proof", func(v *verifierCall) { v.input = verifierEncode(verifierInput{head: v.input, config: []byte{0x01}}) }, inputOnly, 0, false},
		{"oversized calldata", func(v *verifierCall) {
			p := verifierProofStart(v.input)
			v.input = verifierEncode(verifierInput{head: v.input, config: []byte{0x01}, proof: make([]byte, 25_220-p+1)})
		}, inputOnly, 0, false},
		{"oversized document", func(v *verifierCall) {
			v.input = verifierEncode(verifierInput{head: v.input, config: []byte{0x01}, proof: make([]byte, 24_577)})
		}, inputOnly, 0, false},
		{"certificates expired", func(v *verifierCall) { v.time += 30 * 86_400 }, fullGas, 0, false},
		{"attestation from the future", func(v *verifierCall) { v.time -= 3 * 3_600 }, fullGas, 0, false},
		{"before T13", func(v *verifierCall) { v.fork = hardfork.T12 }, inputOnly, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := frameCall(f)
			v.input = append([]byte{}, v.input...)
			tc.mutate(&v)
			res, err := runVerifier(v)
			if tc.reverts {
				assert.Ok(err != nil, "reverts")
				return
			}
			assert.NoError(err, "verify call")
			assert.Equal(res.gasUsed, tc.gas(f, v), "gas used")
			assert.Equal(len(res.out), 32, "bool output")
			assert.Equal(res.out[31], tc.output, "result")
		})
	}
}
