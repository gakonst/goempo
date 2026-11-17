//go:build !amd64 || purego

package secp256k1

const useADX = false

func (a fe) mul(b fe) fe { return a.mulGeneric(b) }

func (a fe) sqr() fe { return a.sqrGeneric() }

func montMul(a, b scalar) scalar { return montMulGeneric(a, b) }

func (a fe) sqrN(n int) fe { return a.sqrNGeneric(n) }

func montSqrN(a scalar, n int) scalar { return montSqrNGeneric(a, n) }
