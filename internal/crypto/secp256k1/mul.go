package secp256k1

import "github.com/gakonst/goempo/internal/u256"

const (
	windowBits     = 4
	windowSize     = 1 << windowBits
	windowCount    = 256 / windowBits
	windowMask     = windowSize - 1
	nibblesPerLimb = 64 / windowBits
)

const _ = uint(windowCount*windowBits - 256)

const _ = uint(nibblesPerLimb*windowBits - 64)

type affineTable [windowSize]affine

type jacobianTable [windowSize]jacobian

var generatorTable = buildGeneratorTable()

func buildGeneratorTable() affineTable {
	var t affineTable
	t[0] = affine{infinity: true}
	acc := jacobian{}
	for i := 1; i < windowSize; i++ {
		acc = acc.addAffine(generator)
		t[i] = acc.toAffine()
	}
	return t
}

func buildTable(p jacobian) jacobianTable {
	var t jacobianTable
	acc := jacobian{}
	for i := 1; i < windowSize; i++ {
		acc = acc.add(p)
		t[i] = acc
	}
	return t
}

func nibble(k u256.Int, window int) int {
	limb := window / nibblesPerLimb
	shift := uint(window%nibblesPerLimb) * windowBits
	return int((k[limb] >> shift) & windowMask)
}

type pointScalar struct {
	point  jacobian
	scalar u256.Int
}

func shamir(gScalar u256.Int, other pointScalar) jacobian {
	table := buildTable(other.point)
	acc := jacobian{}
	for w := windowCount - 1; w >= 0; w-- {
		for d := 0; d < windowBits; d++ {
			acc = acc.double()
		}
		acc = acc.addAffine(generatorTable[nibble(gScalar, w)])
		acc = acc.add(table[nibble(other.scalar, w)])
	}
	return acc
}

func baseMul(k u256.Int) jacobian {
	acc := jacobian{}
	for w := windowCount - 1; w >= 0; w-- {
		for d := 0; d < windowBits; d++ {
			acc = acc.double()
		}
		acc = acc.addAffine(generatorTable[nibble(k, w)])
	}
	return acc
}
