//go:build !amd64 || purego

package keccak

import "github.com/kapetan-io/tiger/assert"

var useAVX512 = false

func keccakF1600x8(a *[25][8]uint64) {
	assert.Fail("keccak AVX-512 permutation on a build without it")
}
