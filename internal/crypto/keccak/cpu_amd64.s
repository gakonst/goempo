//go:build amd64 && !purego

#include "textflag.h"

// func cpuid(leaf uint32) cpuidRegs
TEXT ·cpuid(SB), NOSPLIT, $0-24
	MOVL leaf+0(FP), AX
	MOVL $0, CX
	CPUID
	MOVL AX, ret_eax+8(FP)
	MOVL BX, ret_ebx+12(FP)
	MOVL CX, ret_ecx+16(FP)
	MOVL DX, ret_edx+20(FP)
	RET

// func xgetbv() uint32
TEXT ·xgetbv(SB), NOSPLIT, $0-4
	MOVL $0, CX
	XGETBV
	MOVL AX, ret+0(FP)
	RET
