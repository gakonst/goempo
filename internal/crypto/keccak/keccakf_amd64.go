//go:build amd64 && !purego

package keccak

//go:noescape
func keccakF1600x8(a *[25][8]uint64)

type cpuidRegs struct {
	eax uint32
	ebx uint32
	ecx uint32
	edx uint32
}

func cpuid(leaf uint32) cpuidRegs

func xgetbv() uint32

var useAVX512 = detectAVX512()

func detectAVX512() bool {
	if cpuid(0).eax < 7 {
		return false
	}
	if cpuid(1).ecx&(1<<27) == 0 {
		return false
	}
	if xgetbv()&0xe6 != 0xe6 {
		return false
	}
	return cpuid(7).ebx&(1<<16) != 0
}
