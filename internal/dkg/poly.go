package dkg

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/kapetan-io/tiger/assert"
)

var ErrDuplicatePoint = errors.New("dkg: duplicate evaluation point")

type Poly []bls12381.Scalar

func evaluationPoint(index int) bls12381.Scalar {
	assert.Ok(index >= 0, "dkg: index is non-negative")
	return bls12381.ScalarFromUint64(uint64(index) + 1)
}

func (p Poly) Eval(index int) bls12381.Scalar {
	x := evaluationPoint(index)
	acc := p[len(p)-1]
	for i := range len(p) - 1 {
		acc = acc.Mul(x).Add(p[len(p)-2-i])
	}
	return acc
}

func (p Poly) Commit() Commitment {
	out := make(Commitment, len(p))
	g := bls12381.G2Generator()
	for i, k := range p {
		out[i] = g.Mul(k)
	}
	return out
}

func (c Commitment) Eval(index int) bls12381.G2 {
	x := evaluationPoint(index)
	acc := c[len(c)-1]
	for i := range len(c) - 1 {
		acc = acc.Mul(x).Add(c[len(c)-2-i])
	}
	return acc
}

func (c Commitment) Equal(o Commitment) bool {
	if len(c) != len(o) {
		return false
	}
	for i := range c {
		if !c[i].Equal(o[i]) {
			return false
		}
	}
	return true
}

func LagrangeAtZero(indices []int) ([]bls12381.Scalar, error) {
	assert.Ok(len(indices) <= ParticipantsMax, "dkg: lagrange points within bound")
	out := make([]bls12381.Scalar, len(indices))
	for k, ik := range indices {
		num := bls12381.ScalarFromUint64(1)
		den := bls12381.ScalarFromUint64(1)
		xk := evaluationPoint(ik)
		for j, ij := range indices {
			if j == k {
				continue
			}
			xj := evaluationPoint(ij)
			num = num.Mul(xj)
			den = den.Mul(xj.Sub(xk))
		}
		inv, err := den.Inverse()
		if err != nil {
			return nil, ErrDuplicatePoint
		}
		out[k] = num.Mul(inv)
	}
	return out, nil
}

type Partial struct {
	Index     int
	Signature bls12381.G1
}

func RecoverG1(partials []Partial) (bls12381.G1, error) {
	indices := make([]int, len(partials))
	for i, p := range partials {
		indices[i] = p.Index
	}
	weights, err := LagrangeAtZero(indices)
	if err != nil {
		return bls12381.G1{}, err
	}
	var acc bls12381.G1
	for i, p := range partials {
		term := p.Signature.Mul(weights[i])
		if i == 0 {
			acc = term
			continue
		}
		acc = acc.Add(term)
	}
	return acc, nil
}
