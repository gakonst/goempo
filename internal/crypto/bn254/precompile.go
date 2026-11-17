package bn254

import "errors"

const (
	AddInputLength = 4 * ElementLength
	MulInputLength = 3 * ElementLength
	PairLength     = 6 * ElementLength
	OutputLength   = 2 * ElementLength
	AddGas         = 150
	MulGas         = 6000
	PairingBaseGas = 45000
	PairingPerPair = 34000
	g1Length       = 2 * ElementLength
)

var (
	ErrInvalidPoint  = errors.New("bn254: invalid point")
	ErrPairingLength = errors.New("bn254: pairing input is not a multiple of 192 bytes")
)

func PairingGas(inputLength int) uint64 {
	return PairingBaseGas + PairingPerPair*uint64(inputLength/PairLength)
}

func padded(input []byte, n int) []byte {
	out := make([]byte, n)
	copy(out, input)
	return out
}

func Add(input []byte) ([OutputLength]byte, error) {
	in := padded(input, AddInputLength)
	var out [OutputLength]byte
	a, ok := decodeG1(in[:g1Length])
	if !ok {
		return out, ErrInvalidPoint
	}
	b, ok := decodeG1(in[g1Length:])
	if !ok {
		return out, ErrInvalidPoint
	}
	a.jac().add(b.jac()).affine().encode(out[:])
	return out, nil
}

func ScalarMul(input []byte) ([OutputLength]byte, error) {
	in := padded(input, MulInputLength)
	var out [OutputLength]byte
	p, ok := decodeG1(in[:g1Length])
	if !ok {
		return out, ErrInvalidPoint
	}
	var k [ElementLength]byte
	copy(k[:], in[g1Length:])
	p.jac().mulScalar(&k).affine().encode(out[:])
	return out, nil
}

func PairingCheck(input []byte) (bool, error) {
	if len(input)%PairLength != 0 {
		return false, ErrPairingLength
	}
	n := len(input) / PairLength
	pairs := make([]pair, 0, n)
	for i := 0; i < n; i++ {
		chunk := input[i*PairLength : (i+1)*PairLength]
		p, ok := decodeG1(chunk[:g1Length])
		if !ok {
			return false, ErrInvalidPoint
		}
		q, ok := decodeG2(chunk[g1Length:])
		if !ok {
			return false, ErrInvalidPoint
		}
		pairs = append(pairs, pair{p: p, q: q})
	}
	return pairingProduct(pairs).isOne(), nil
}
