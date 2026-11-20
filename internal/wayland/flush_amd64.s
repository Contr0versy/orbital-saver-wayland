#include "textflag.h"

// func cpuid(leaf, sub uint32) (a, b, c, d uint32)
TEXT ·cpuid(SB), NOSPLIT, $0-24
	MOVL leaf+0(FP), AX
	MOVL sub+4(FP), CX
	CPUID
	MOVL AX, a+8(FP)
	MOVL BX, b+12(FP)
	MOVL CX, c+16(FP)
	MOVL DX, d+20(FP)
	RET

// func flushOpt(p unsafe.Pointer, n uintptr)
TEXT ·flushOpt(SB), NOSPLIT, $0-16
	MOVQ p+0(FP), AX
	MOVQ n+8(FP), CX
	ADDQ AX, CX
	ANDQ $-64, AX

loop:
	CMPQ AX, CX
	JAE  done
	CLFLUSHOPT (AX)
	ADDQ $64, AX
	JMP  loop

done:
	SFENCE
	RET

// func flushLegacy(p unsafe.Pointer, n uintptr)
TEXT ·flushLegacy(SB), NOSPLIT, $0-16
	MOVQ p+0(FP), AX
	MOVQ n+8(FP), CX
	ADDQ AX, CX
	ANDQ $-64, AX

loop:
	CMPQ AX, CX
	JAE  done
	CLFLUSH (AX)
	ADDQ $64, AX
	JMP  loop

done:
	MFENCE
	RET
