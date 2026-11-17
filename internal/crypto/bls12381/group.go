package bls12381

import (
	"errors"
	"math/big"

	"github.com/kapetan-io/tiger/assert"
)

const (
	CompressedG2Length = 2 * fpByteLength
)

var (
	ErrIdentity      = errors.New("bls12381: point is the identity")
	ErrScalarRange   = errors.New("bls12381: scalar not below the group order")
	ErrScalarInverse = errors.New("bls12381: zero has no inverse")
)

type G1 struct{ p g1Jac }

type G2 struct{ p g2Jac }

type Scalar [ScalarLength]byte

var scalarOrder = new(big.Int).SetBytes([]byte{
	0x73, 0xed, 0xa7, 0x53, 0x29, 0x9d, 0x7d, 0x48, 0x33, 0x39, 0xd8, 0x08, 0x09, 0xa1, 0xd8, 0x05,
	0x53, 0xbd, 0xa4, 0x02, 0xff, 0xfe, 0x5b, 0xfe, 0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x01,
})

var g1GeneratorCompressed = [CompressedG1Length]byte{
	0x97, 0xf1, 0xd3, 0xa7, 0x31, 0x97, 0xd7, 0x94, 0x26, 0x95, 0x63, 0x8c, 0x4f, 0xa9, 0xac, 0x0f,
	0xc3, 0x68, 0x8c, 0x4f, 0x97, 0x74, 0xb9, 0x05, 0xa1, 0x4e, 0x3a, 0x3f, 0x17, 0x1b, 0xac, 0x58,
	0x6c, 0x55, 0xe8, 0x3f, 0xf9, 0x7a, 0x1a, 0xef, 0xfb, 0x3a, 0xf0, 0x0a, 0xdb, 0x22, 0xc6, 0xbb,
}

var g2GeneratorCompressed = [CompressedG2Length]byte{
	0x93, 0xe0, 0x2b, 0x60, 0x52, 0x71, 0x9f, 0x60, 0x7d, 0xac, 0xd3, 0xa0, 0x88, 0x27, 0x4f, 0x65,
	0x59, 0x6b, 0xd0, 0xd0, 0x99, 0x20, 0xb6, 0x1a, 0xb5, 0xda, 0x61, 0xbb, 0xdc, 0x7f, 0x50, 0x49,
	0x33, 0x4c, 0xf1, 0x12, 0x13, 0x94, 0x5d, 0x57, 0xe5, 0xac, 0x7d, 0x05, 0x5d, 0x04, 0x2b, 0x7e,
	0x02, 0x4a, 0xa2, 0xb2, 0xf0, 0x8f, 0x0a, 0x91, 0x26, 0x08, 0x05, 0x27, 0x2d, 0xc5, 0x10, 0x51,
	0xc6, 0xe4, 0x7a, 0xd4, 0xfa, 0x40, 0x3b, 0x02, 0xb4, 0x51, 0x0b, 0x64, 0x7a, 0xe3, 0xd1, 0x77,
	0x0b, 0xac, 0x03, 0x26, 0xa8, 0x05, 0xbb, 0xef, 0xd4, 0x80, 0x56, 0xc8, 0xc1, 0x21, 0xbd, 0xb8,
}

func G1Generator() G1 {
	p, err := DecodeG1(g1GeneratorCompressed[:])
	assert.Ok(err == nil, "bls12381: G1 generator decodes")
	return p
}

func G2Generator() G2 {
	p, err := DecodeG2(g2GeneratorCompressed[:])
	assert.Ok(err == nil, "bls12381: G2 generator decodes")
	return p
}

func (a fp2) lexicographicallyLargest() bool {
	if !a.c1.isZero() {
		return a.c1.lexicographicallyLargest()
	}
	return a.c0.lexicographicallyLargest()
}

func compressedFlags(b []byte) (bool, error) {
	if b[0]&flagCompressed == 0 {
		return false, ErrCompressedFlags
	}
	if b[0]&flagInfinity == 0 {
		return false, nil
	}
	if b[0] != flagCompressed|flagInfinity {
		return false, ErrCompressedFlags
	}
	for _, v := range b[1:] {
		if v != 0 {
			return false, ErrCompressedFlags
		}
	}
	return true, nil
}

func decodeCompressedFp(b []byte) (fp, bool) {
	var enc [EncodedFpLength]byte
	copy(enc[fpPadding:], b[:fpByteLength])
	enc[fpPadding] &= flagMask
	return decodeFp(enc[:])
}

func DecodeG1(b []byte) (G1, error) {
	if len(b) != CompressedG1Length {
		return G1{}, ErrInputLength
	}
	infinity, err := compressedFlags(b)
	if err != nil {
		return G1{}, err
	}
	if infinity {
		return G1{}, ErrIdentity
	}
	x, ok := decodeCompressedFp(b)
	if !ok {
		return G1{}, ErrInvalidField
	}
	y, ok := x.sqr().mul(x).add(curveB).sqrt()
	if !ok {
		return G1{}, ErrInvalidPoint
	}
	if y.lexicographicallyLargest() != (b[0]&flagSign != 0) {
		y = y.neg()
	}
	p := g1Affine{x: x, y: y}
	if !p.inSubgroup() {
		return G1{}, ErrNotInSubgroup
	}
	return G1{p: p.jac()}, nil
}

func DecodeG2(b []byte) (G2, error) {
	if len(b) != CompressedG2Length {
		return G2{}, ErrInputLength
	}
	infinity, err := compressedFlags(b)
	if err != nil {
		return G2{}, err
	}
	if infinity {
		return G2{}, ErrIdentity
	}
	c1, ok1 := decodeCompressedFp(b[:fpByteLength])
	c0, ok0 := decodeCompressedFp(b[fpByteLength:])
	if !ok0 || !ok1 || b[fpByteLength]&^flagMask != 0 {
		return G2{}, ErrInvalidField
	}
	x := fp2{c0: c0, c1: c1}
	y, ok := x.sqr().mul(x).add(twistB).sqrt()
	if !ok {
		return G2{}, ErrInvalidPoint
	}
	if y.lexicographicallyLargest() != (b[0]&flagSign != 0) {
		y = y.neg()
	}
	p := g2Affine{x: x, y: y}
	if !p.inSubgroup() {
		return G2{}, ErrNotInSubgroup
	}
	return G2{p: p.jac()}, nil
}

func (p G1) Bytes() [CompressedG1Length]byte {
	var out [CompressedG1Length]byte
	a := p.p.affine()
	if a.infinity {
		out[0] = flagCompressed | flagInfinity
		return out
	}
	var enc [EncodedFpLength]byte
	a.x.putBytes(enc[:])
	copy(out[:], enc[fpPadding:])
	out[0] |= flagCompressed
	if a.y.lexicographicallyLargest() {
		out[0] |= flagSign
	}
	return out
}

func (p G2) Bytes() [CompressedG2Length]byte {
	var out [CompressedG2Length]byte
	a := p.p.affine()
	if a.infinity {
		out[0] = flagCompressed | flagInfinity
		return out
	}
	var enc [EncodedFpLength]byte
	a.x.c1.putBytes(enc[:])
	copy(out[:fpByteLength], enc[fpPadding:])
	a.x.c0.putBytes(enc[:])
	copy(out[fpByteLength:], enc[fpPadding:])
	out[0] |= flagCompressed
	if a.y.lexicographicallyLargest() {
		out[0] |= flagSign
	}
	return out
}

func (p G1) Add(q G1) G1 { return G1{p: p.p.add(q.p)} }

func (p G2) Add(q G2) G2 { return G2{p: p.p.add(q.p)} }

func (p G1) Neg() G1 { return G1{p: p.p.neg()} }

func (p G2) Neg() G2 { return G2{p: p.p.neg()} }

func (p G1) Mul(k Scalar) G1 { return G1{p: p.p.mulBytes(k[:])} }

func MultiExpG1(points []G1, scalars []Scalar, width int) G1 {
	assert.Ok(len(points) == len(scalars), "bls12381: one scalar per point")
	jac := make([]g1Jac, len(points))
	raw := make([][]byte, len(points))
	for i := range points {
		jac[i] = points[i].p
		raw[i] = scalars[i][:]
	}
	return G1{p: msmG1Bits(jac, raw, width)}
}

func (p G2) Mul(k Scalar) G2 { return G2{p: p.p.mulBytes(k[:])} }

func (p G1) Equal(q G1) bool { return p.p.equal(q.p) }

func (p G2) Equal(q G2) bool { return p.p.equal(q.p) }

func (p G1) IsIdentity() bool { return p.p.isInfinity() }

func (p G2) IsIdentity() bool { return p.p.isInfinity() }

type PairingTerm struct {
	A G1
	B G2
}

func PairingIsOne(terms []PairingTerm) bool {
	pairs := make([]pair, 0, len(terms))
	for _, t := range terms {
		a := t.A.p.affine()
		b := t.B.p.affine()
		if a.infinity || b.infinity {
			continue
		}
		pairs = append(pairs, pair{p: a, q: b})
	}
	if len(pairs) == 0 {
		return true
	}
	return pairingProduct(pairs).isOne()
}

func (k Scalar) big() *big.Int { return new(big.Int).SetBytes(k[:]) }

func scalarOf(v *big.Int) Scalar {
	var out Scalar
	new(big.Int).Mod(v, scalarOrder).FillBytes(out[:])
	return out
}

func ScalarFromBytes(b []byte) (Scalar, error) {
	if len(b) != ScalarLength {
		return Scalar{}, ErrInputLength
	}
	var out Scalar
	copy(out[:], b)
	if out.big().Cmp(scalarOrder) >= 0 {
		return Scalar{}, ErrScalarRange
	}
	return out, nil
}

func ScalarFromUint64(v uint64) Scalar { return scalarOf(new(big.Int).SetUint64(v)) }

func ScalarFromWide(b []byte) Scalar { return scalarOf(new(big.Int).SetBytes(b)) }

func (k Scalar) Add(o Scalar) Scalar { return scalarOf(new(big.Int).Add(k.big(), o.big())) }

func (k Scalar) Sub(o Scalar) Scalar { return scalarOf(new(big.Int).Sub(k.big(), o.big())) }

func (k Scalar) Mul(o Scalar) Scalar { return scalarOf(new(big.Int).Mul(k.big(), o.big())) }

func (k Scalar) IsZero() bool { return k == Scalar{} }

func (k Scalar) Inverse() (Scalar, error) {
	if k.IsZero() {
		return Scalar{}, ErrScalarInverse
	}
	return scalarOf(new(big.Int).ModInverse(k.big(), scalarOrder)), nil
}
