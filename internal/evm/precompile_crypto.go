package evm

import (
	"crypto/sha256"
	"math/big"

	"github.com/gakonst/goempo/internal/crypto/blake2f"
	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/crypto/bn254"
	"github.com/gakonst/goempo/internal/crypto/kzg"
	"github.com/gakonst/goempo/internal/crypto/p256"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"golang.org/x/crypto/ripemd160"
)

type ComputeFunc func(input []byte) ([]byte, bool)

type Priced struct {
	Gas   func(input []byte) uint64
	Valid func(input []byte) bool
	Run   ComputeFunc
}

func NewPriced(p Priced) Precompile {
	return pricedPrecompile{p: p}
}

func (p Priced) execute(call *PrecompileCall) PrecompileResult {
	if p.Valid != nil && !p.Valid(call.Input) {
		return PrecompileResult{GasUsed: call.Gas, Status: StatusPrecompileFailure}
	}
	need := p.Gas(call.Input)
	if need > call.Gas {
		return PrecompileResult{GasUsed: call.Gas, Status: StatusOutOfGas}
	}
	if p.Run == nil {
		return PrecompileResult{GasUsed: call.Gas, Status: StatusPrecompileFailure}
	}
	out, ok := p.Run(call.Input)
	if !ok {
		return PrecompileResult{GasUsed: call.Gas, Status: StatusPrecompileFailure}
	}
	return PrecompileResult{Output: out, GasUsed: need, Status: StatusReturn}
}

type pricedPrecompile struct {
	p Priced
}

func (w pricedPrecompile) Run(call *PrecompileCall) PrecompileResult {
	return w.p.execute(call)
}

func constGas(g uint64) func([]byte) uint64 {
	return func(_ []byte) uint64 { return g }
}

func linearGas(c linearCost) func([]byte) uint64 {
	return c.of
}

func exactLength(n int) func([]byte) bool {
	return func(input []byte) bool { return len(input) == n }
}

func multipleOf(n int) func([]byte) bool {
	return func(input []byte) bool { return len(input) > 0 && len(input)%n == 0 }
}

func addCryptoPrecompiles(s *PrecompileSet, fork Fork) {
	s.Set(PrecompileAddress(0x01), NewPriced(Priced{Gas: constGas(3000), Run: ecrecover}))
	s.Set(PrecompileAddress(0x02), NewPriced(Priced{Gas: linearGas(linearCost{base: 60, word: 12}), Run: sha256Sum}))
	s.Set(PrecompileAddress(0x03), NewPriced(Priced{Gas: linearGas(linearCost{base: 600, word: 120}), Run: ripemdSum}))
	s.Set(PrecompileAddress(0x04), NewPriced(Priced{Gas: linearGas(linearCost{base: 15, word: 3}), Run: identity}))
	modexp := modexpPricing{osaka: fork >= Osaka, eip198: fork < Berlin}
	s.Set(PrecompileAddress(0x05), NewPriced(Priced{Gas: modexp.gas, Valid: modexp.valid, Run: modexpBig}))
	s.Set(PrecompileAddress(0x06), NewPriced(Priced{Gas: constGas(bn254.AddGas), Run: bnAdd}))
	s.Set(PrecompileAddress(0x07), NewPriced(Priced{Gas: constGas(bn254.MulGas), Run: bnMul}))
	s.Set(PrecompileAddress(0x08), NewPriced(Priced{Gas: bnPairingGas, Valid: bnPairingValid, Run: bnPairing}))
	s.Set(PrecompileAddress(0x09), NewPriced(Priced{Gas: blake2fGas, Valid: blake2fValid, Run: blake2fCompress}))
	if fork >= Cancun {
		s.Set(PrecompileAddress(0x0a), NewPriced(Priced{Gas: constGas(pointEvalGas), Valid: exactLength(pointEvalInput), Run: pointEvaluation}))
	}
	if fork >= Prague {
		addBLS(s)
	}
	if fork >= Osaka {
		s.Set(PrecompileAddress(0x100), NewPriced(Priced{Gas: constGas(p256.PrecompileGas), Run: p256Verify}))
	}
}

func addBLS(s *PrecompileSet) {
	g1 := msmPricing{pairSize: blsG1MulPair, baseGas: 12000, discounts: &blsG1Discounts}
	g2 := msmPricing{pairSize: blsG2MulPair, baseGas: 22500, discounts: &blsG2Discounts}
	s.Set(PrecompileAddress(0x0b), NewPriced(Priced{Gas: constGas(375), Valid: exactLength(blsG1AddInput), Run: blsG1Add}))
	s.Set(PrecompileAddress(0x0c), NewPriced(Priced{Gas: g1.gas, Valid: multipleOf(blsG1MulPair), Run: blsG1MSM}))
	s.Set(PrecompileAddress(0x0d), NewPriced(Priced{Gas: constGas(600), Valid: exactLength(blsG2AddInput), Run: blsG2Add}))
	s.Set(PrecompileAddress(0x0e), NewPriced(Priced{Gas: g2.gas, Valid: multipleOf(blsG2MulPair), Run: blsG2MSM}))
	s.Set(PrecompileAddress(0x0f), NewPriced(Priced{Gas: blsPairingGas, Valid: multipleOf(blsPairingPair), Run: blsPairing}))
	s.Set(PrecompileAddress(0x10), NewPriced(Priced{Gas: constGas(5500), Valid: exactLength(blsMapFpInput), Run: blsMapG1}))
	s.Set(PrecompileAddress(0x11), NewPriced(Priced{Gas: constGas(23800), Valid: exactLength(blsMapFp2Input), Run: blsMapG2}))
}

func bnPairingGas(input []byte) uint64 {
	return bn254.PairingGas(len(input))
}

func bnAdd(input []byte) ([]byte, bool) {
	out, err := bn254.Add(input)
	if err != nil {
		return nil, false
	}
	return out[:], true
}

func bnMul(input []byte) ([]byte, bool) {
	out, err := bn254.ScalarMul(input)
	if err != nil {
		return nil, false
	}
	return out[:], true
}

func bnPairing(input []byte) ([]byte, bool) {
	ok, err := bn254.PairingCheck(input)
	if err != nil {
		return nil, false
	}
	out := make([]byte, 32)
	if ok {
		out[31] = 1
	}
	return out, true
}

func bnPairingValid(input []byte) bool {
	return len(input)%bnPairSize == 0
}

func blsPairingGas(input []byte) uint64 {
	return 37700 + uint64(len(input)/blsPairingPair)*32600
}

func blake2fGas(input []byte) uint64 {
	if len(input) < 4 {
		return 0
	}
	return uint64(input[0])<<24 | uint64(input[1])<<16 | uint64(input[2])<<8 | uint64(input[3])
}

func blake2fValid(input []byte) bool {
	return len(input) == blake2fInputSize && input[blake2fInputSize-1] <= 1
}

func blake2fCompress(input []byte) ([]byte, bool) {
	out, err := blake2f.Compress(input)
	if err != nil {
		return nil, false
	}
	return out[:], true
}

func blsG1Add(input []byte) ([]byte, bool) {
	out, err := bls12381.G1Add(input)
	return out[:], err == nil
}

func blsG1MSM(input []byte) ([]byte, bool) {
	out, err := bls12381.G1MSM(input)
	return out[:], err == nil
}

func blsG2Add(input []byte) ([]byte, bool) {
	out, err := bls12381.G2Add(input)
	return out[:], err == nil
}

func blsG2MSM(input []byte) ([]byte, bool) {
	out, err := bls12381.G2MSM(input)
	return out[:], err == nil
}

func blsPairing(input []byte) ([]byte, bool) {
	out, err := bls12381.PairingCheck(input)
	return out[:], err == nil
}

func blsMapG1(input []byte) ([]byte, bool) {
	out, err := bls12381.MapFpToG1(input)
	return out[:], err == nil
}

func blsMapG2(input []byte) ([]byte, bool) {
	out, err := bls12381.MapFp2ToG2(input)
	return out[:], err == nil
}

func pointEvaluation(input []byte) ([]byte, bool) {
	out, err := kzg.PointEvaluation(input)
	return out[:], err == nil
}

func ecrecover(input []byte) ([]byte, bool) {
	out, ok := secp256k1.Ecrecover(input)
	if !ok {
		return nil, true
	}
	return out[:], true
}

func sha256Sum(input []byte) ([]byte, bool) {
	sum := sha256.Sum256(input)
	return sum[:], true
}

func ripemdSum(input []byte) ([]byte, bool) {
	h := ripemd160.New()
	h.Write(input)
	out := make([]byte, 32)
	copy(out[12:], h.Sum(nil))
	return out, true
}

func identity(input []byte) ([]byte, bool) {
	out := make([]byte, len(input))
	copy(out, input)
	return out, true
}

func p256Verify(input []byte) ([]byte, bool) {
	if !p256.VerifyPrecompile(input) {
		return nil, true
	}
	var out [32]byte
	out[31] = 1
	return out[:], true
}

func modexpBig(input []byte) ([]byte, bool) {
	if readModexpHeader(input).modLen == 0 {
		return []byte{}, true
	}
	ops, ok := modexpOperands(input)
	if !ok {
		return nil, false
	}
	out := make([]byte, len(ops.mod))
	if len(ops.mod) == 0 {
		return out, true
	}
	mod := new(big.Int).SetBytes(ops.mod)
	if mod.Sign() == 0 {
		return out, true
	}
	base := new(big.Int).SetBytes(ops.base)
	exp := new(big.Int).SetBytes(ops.exp)
	result := new(big.Int).Exp(base, exp, mod)
	result.FillBytes(out)
	return out, true
}
