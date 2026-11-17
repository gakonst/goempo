package u256

import "github.com/kapetan-io/tiger/assert"

const inverseSteps = 6 * Bits

func (x Int) ModInverse(m Int) (Int, bool) {
	if m[0]&1 == 0 {
		return Int{}, false
	}
	if m == One() {
		return Int{}, false
	}
	a := x.Mod(m)
	if a.IsZero() {
		return Int{}, false
	}
	u, v := a, m
	x1, x2 := One(), Int{}
	step := 0
	for ; step < inverseSteps; step++ {
		if u == One() {
			return x1, true
		}
		if v == One() {
			return x2, true
		}
		if u.IsZero() {
			return Int{}, false
		}
		if v.IsZero() {
			return Int{}, false
		}
		if u[0]&1 == 0 {
			u = u.Rsh(1)
			x1 = x1.halveMod(modulus(m))
			continue
		}
		if v[0]&1 == 0 {
			v = v.Rsh(1)
			x2 = x2.halveMod(modulus(m))
			continue
		}
		if u.Lt(v) {
			v = v.Sub(u)
			x2 = x2.subMod(x1, modulus(m))
			continue
		}
		u = u.Sub(v)
		x1 = x1.subMod(x2, modulus(m))
	}
	assert.Ok(step < inverseSteps, "u256: ModInverse did not converge")
	return Int{}, false
}

type modulus Int

func (x Int) halveMod(m modulus) Int {
	if x[0]&1 == 0 {
		return x.Rsh(1)
	}
	sum, carry := x.AddOverflow(Int(m))
	half := sum.Rsh(1)
	if carry {
		half[Limbs-1] |= 1 << (limbBits - 1)
	}
	return half
}

func (x Int) subMod(y Int, m modulus) Int {
	d, borrow := x.SubOverflow(y)
	if borrow {
		return d.Add(Int(m))
	}
	return d
}
