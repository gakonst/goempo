package modexp

import (
	"errors"
	"math/big"
	"math/bits"
)

type Rules uint8

const (
	Prague Rules = iota
	Osaka
)

const (
	headerLength     = 96
	wordLength       = 32
	OsakaLengthMax   = 1024
	OsakaGasMin      = 500
	PragueGasMin     = 200
	OperandLengthMax = 75888
	pragueDivisor    = 3
	pragueMultiplier = 8
	osakaMultiplier  = 16
	osakaSmallCost   = 16
	limbBytes        = 8
)

const _ = uint(OperandLengthMax - OsakaLengthMax)

const _ = uint(OsakaGasMin - PragueGasMin)

var (
	ErrLengthTooLarge  = errors.New("modexp: declared length too large")
	ErrOperandTooLarge = errors.New("modexp: operand longer than any gas pays for")
)

type lengths struct {
	base     uint64
	exponent uint64
	modulus  uint64
}

func readWord(input []byte, offset uint64) [wordLength]byte {
	var w [wordLength]byte
	if offset >= uint64(len(input)) {
		return w
	}
	copy(w[:], input[offset:])
	return w
}

func wordFits(w [wordLength]byte, limit uint64) (uint64, bool) {
	for i := 0; i < wordLength-limbBytes; i++ {
		if w[i] != 0 {
			return 0, false
		}
	}
	var v uint64
	for i := wordLength - limbBytes; i < wordLength; i++ {
		v = v<<8 | uint64(w[i])
	}
	return v, v <= limit
}

func readLengths(input []byte, rules Rules) (lengths, error) {
	limit := uint64(OsakaLengthMax)
	if rules == Prague {
		limit = ^uint64(0)
	}
	var l lengths
	var ok bool
	l.base, ok = wordFits(readWord(input, 0), limit)
	if !ok {
		return lengths{}, ErrLengthTooLarge
	}
	l.exponent, ok = wordFits(readWord(input, wordLength), limit)
	if !ok {
		if rules != Prague {
			return lengths{}, ErrLengthTooLarge
		}
		l.exponent = ^uint64(0)
	}
	l.modulus, ok = wordFits(readWord(input, 2*wordLength), limit)
	if !ok {
		return lengths{}, ErrLengthTooLarge
	}
	return l, nil
}

type span struct {
	offset uint64
	length uint64
}

func readPadded(input []byte, s span) []byte {
	out := make([]byte, s.length)
	if s.offset >= uint64(len(input)) {
		return out
	}
	copy(out, input[s.offset:])
	return out
}

type saturating uint64

func (a saturating) add(b uint64) uint64 {
	s, c := bits.Add64(uint64(a), b, 0)
	if c != 0 {
		return ^uint64(0)
	}
	return s
}

func (a saturating) mul(b uint64) uint64 {
	hi, lo := bits.Mul64(uint64(a), b)
	if hi != 0 {
		return ^uint64(0)
	}
	return lo
}

func exponentHeadBits(input []byte, l lengths) uint64 {
	n := min(l.exponent, wordLength)
	head := readPadded(input, span{offset: saturating(headerLength).add(l.base), length: n})
	for i, b := range head {
		if b != 0 {
			return uint64(len(head)-i-1)*8 + uint64(bits.Len8(b))
		}
	}
	return 0
}

func iterations(input []byte, l lengths, multiplier uint64) uint64 {
	headBits := exponentHeadBits(input, l)
	var count uint64
	if headBits > 0 {
		count = headBits - 1
	}
	if l.exponent > wordLength {
		count = saturating(saturating(multiplier).mul(l.exponent - wordLength)).add(count)
	}
	return max(count, 1)
}

func Gas(input []byte, rules Rules) (uint64, error) {
	l, err := readLengths(input, rules)
	if err != nil {
		return 0, err
	}
	longest := max(l.base, l.modulus)
	words := longest / limbBytes
	if longest%limbBytes != 0 {
		words++
	}
	if rules == Prague {
		count := iterations(input, l, pragueMultiplier)
		hi, lo := bits.Mul64(words, words)
		cost := mulDiv3(wide{hi: hi, lo: lo}, count)
		return max(cost, PragueGasMin), nil
	}
	complexity := uint64(osakaSmallCost)
	if longest > wordLength {
		complexity = 2 * words * words
	}
	cost := saturating(complexity).mul(iterations(input, l, osakaMultiplier))
	return max(cost, OsakaGasMin), nil
}

type wide struct {
	hi uint64
	lo uint64
}

func mulDiv3(w wide, count uint64) uint64 {
	h1, l0 := bits.Mul64(w.lo, count)
	h2, l1 := bits.Mul64(w.hi, count)
	mid, c := bits.Add64(l1, h1, 0)
	top := h2 + c
	if top >= pragueDivisor {
		return ^uint64(0)
	}
	q1, r1 := bits.Div64(top, mid, pragueDivisor)
	if q1 != 0 {
		return ^uint64(0)
	}
	q0, _ := bits.Div64(r1, l0, pragueDivisor)
	return q0
}

func Run(input []byte, rules Rules) ([]byte, error) {
	l, err := readLengths(input, rules)
	if err != nil {
		return nil, err
	}
	if l.base == 0 {
		if l.modulus == 0 {
			return []byte{}, nil
		}
	}
	if l.modulus > OperandLengthMax {
		return nil, ErrOperandTooLarge
	}
	expStart := saturating(headerLength).add(l.base)
	modStart := saturating(expStart).add(l.exponent)
	modBytes := readPadded(input, span{offset: modStart, length: l.modulus})
	modulus := new(big.Int).SetBytes(modBytes)
	out := make([]byte, l.modulus)
	if modulus.Sign() == 0 {
		return out, nil
	}
	if l.base > OperandLengthMax {
		return nil, ErrOperandTooLarge
	}
	if l.exponent > uint64(len(input)) {
		return nil, ErrOperandTooLarge
	}
	base := new(big.Int).SetBytes(readPadded(input, span{offset: headerLength, length: l.base}))
	exponent := new(big.Int).SetBytes(readPadded(input, span{offset: expStart, length: l.exponent}))
	result := new(big.Int)
	if modulus.Cmp(big.NewInt(1)) != 0 {
		result.Exp(base, exponent, modulus)
	}
	result.FillBytes(out)
	return out, nil
}
