//go:build amd64 && !purego

package secp256k1

//go:noescape
func feMulADX(z, a, b *fe)

//go:noescape
func scalarMontMulADX(z, a, b *scalar)

//go:noescape
func feSqrNADX(z *fe, n int)

//go:noescape
func scalarMontSqrNADX(z *scalar, n int)

func (a fe) sqrN(n int) fe {
	if !useADX || n < 1 {
		return a.sqrNGeneric(n)
	}
	feSqrNADX(&a, n)
	return a
}

func montSqrN(a scalar, n int) scalar {
	if !useADX || n < 1 {
		return montSqrNGeneric(a, n)
	}
	scalarMontSqrNADX(&a, n)
	return a
}

func montMul(a, b scalar) scalar {
	if !useADX {
		return montMulGeneric(a, b)
	}
	var z scalar
	scalarMontMulADX(&z, &a, &b)
	return z
}

type cpuidRegs struct {
	eax uint32
	ebx uint32
	ecx uint32
	edx uint32
}

func cpuid(leaf uint32) cpuidRegs

const (
	cpuidBMI2 = 1 << 8
	cpuidADX  = 1 << 19
)

var useADX = detectADX()

func detectADX() bool {
	if cpuid(0).eax < 7 {
		return false
	}
	ebx := cpuid(7).ebx
	return ebx&cpuidBMI2 != 0 && ebx&cpuidADX != 0
}

func (a fe) mul(b fe) fe {
	if !useADX {
		return a.mulGeneric(b)
	}
	var z fe
	feMulADX(&z, &a, &b)
	return z
}

func (a fe) sqr() fe {
	if !useADX {
		return a.sqrGeneric()
	}
	var z fe
	feMulADX(&z, &a, &a)
	return z
}
