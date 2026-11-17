package bls12381

import "errors"

const (
	CompressedG1Length = fpByteLength
	flagCompressed     = 0x80
	flagInfinity       = 0x40
	flagSign           = 0x20
	flagMask           = 0x1f
)

var (
	ErrCompressedFlags = errors.New("bls12381: compressed point has invalid flags")
)

func (a fp) lexicographicallyLargest() bool {
	l := a.canonical().limbs()
	h := expPMinus1Over2.limbs()
	for i := fpLimbs - 1; i >= 0; i-- {
		if l[i] != h[i] {
			return l[i] > h[i]
		}
	}
	return false
}

func DecompressG1(b [CompressedG1Length]byte) ([G1Length]byte, error) {
	var out [G1Length]byte
	if b[0]&flagCompressed == 0 {
		return out, ErrCompressedFlags
	}
	if b[0]&flagInfinity != 0 {
		if b[0] != flagCompressed|flagInfinity {
			return out, ErrCompressedFlags
		}
		for _, v := range b[1:] {
			if v != 0 {
				return out, ErrCompressedFlags
			}
		}
		return out, nil
	}
	var enc [EncodedFpLength]byte
	copy(enc[fpPadding:], b[:])
	enc[fpPadding] &= flagMask
	x, ok := decodeFp(enc[:])
	if !ok {
		return out, ErrInvalidField
	}
	y, ok := x.sqr().mul(x).add(curveB).sqrt()
	if !ok {
		return out, ErrInvalidPoint
	}
	if y.lexicographicallyLargest() != (b[0]&flagSign != 0) {
		y = y.neg()
	}
	p := g1Affine{x: x, y: y}
	if !p.inSubgroup() {
		return out, ErrNotInSubgroup
	}
	p.encode(out[:])
	return out, nil
}
