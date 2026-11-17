package bls12381

import (
	"math/bits"

	"github.com/kapetan-io/tiger/assert"
)

const (
	scalarBits    = ScalarLength * 8
	windowBitsMax = 12
	windowBitsMin = 1
)

const _ = uint(scalarBits - windowBitsMax)

const _ = uint(windowBitsMax - windowBitsMin)

func windowBits(n int) int {
	return max(windowBitsMin, min(bits.Len(uint(n))-1, windowBitsMax))
}

type scalarWindow struct {
	start int
	width int
}

func windowValue(scalar []byte, w scalarWindow) int {
	v := 0
	for i := w.width - 1; i >= 0; i-- {
		bit := w.start + i
		if bit >= scalarBits {
			continue
		}
		b := scalar[ScalarLength-1-bit/8]
		v = v<<1 | int((b>>uint(bit%8))&1)
	}
	return v
}

func msmG1(points []g1Jac, scalars [][]byte) g1Jac {
	return msmG1Bits(points, scalars, scalarBits)
}

func msmG1Bits(points []g1Jac, scalars [][]byte, width int) g1Jac {
	assert.Ok(width > 0, "bls12381: msm width is positive")
	assert.Ok(width <= scalarBits, "bls12381: msm width within scalar bits")
	n := min(len(points), len(scalars))
	c := windowBits(n)
	buckets := make([]g1Jac, (1<<c)-1)
	windows := (width + c - 1) / c
	result := g1Jac{}
	for w := windows - 1; w >= 0; w-- {
		for d := 0; d < c; d++ {
			result = result.double()
		}
		clear(buckets)
		for i := 0; i < n; i++ {
			idx := windowValue(scalars[i], scalarWindow{start: w * c, width: c})
			if idx > 0 {
				buckets[idx-1] = buckets[idx-1].add(points[i])
			}
		}
		running := g1Jac{}
		sum := g1Jac{}
		for b := len(buckets) - 1; b >= 0; b-- {
			running = running.add(buckets[b])
			sum = sum.add(running)
		}
		result = result.add(sum)
	}
	return result
}

func msmG2(points []g2Jac, scalars [][]byte) g2Jac {
	n := min(len(points), len(scalars))
	c := windowBits(n)
	buckets := make([]g2Jac, (1<<c)-1)
	windows := (scalarBits + c - 1) / c
	result := g2Jac{}
	for w := windows - 1; w >= 0; w-- {
		for d := 0; d < c; d++ {
			result = result.double()
		}
		clear(buckets)
		for i := 0; i < n; i++ {
			idx := windowValue(scalars[i], scalarWindow{start: w * c, width: c})
			if idx > 0 {
				buckets[idx-1] = buckets[idx-1].add(points[i])
			}
		}
		running := g2Jac{}
		sum := g2Jac{}
		for b := len(buckets) - 1; b >= 0; b-- {
			running = running.add(buckets[b])
			sum = sum.add(running)
		}
		result = result.add(sum)
	}
	return result
}
