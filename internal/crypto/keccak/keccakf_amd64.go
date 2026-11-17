//go:build amd64 && !purego

package keccak

//go:noescape
func keccakF1600x8(a *[25][8]uint64)

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)

func xgetbv() (eax, edx uint32)

var useAVX512 = detectAVX512()

// detectAVX512 reports AVX-512F with the OS saving opmask and ZMM state.
func detectAVX512() bool {
	maxLeaf, _, _, _ := cpuid(0, 0)
	if maxLeaf < 7 {
		return false
	}
	_, _, ecx1, _ := cpuid(1, 0)
	if ecx1&(1<<27) == 0 {
		return false
	}
	xcr0, _ := xgetbv()
	if xcr0&0xe6 != 0xe6 {
		return false
	}
	_, ebx7, _, _ := cpuid(7, 0)
	return ebx7&(1<<16) != 0
}
