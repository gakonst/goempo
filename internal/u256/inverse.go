package u256

import "math/big"

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
	ab := a.Bytes32()
	mb := m.Bytes32()
	inv := new(big.Int).ModInverse(new(big.Int).SetBytes(ab[:]), new(big.Int).SetBytes(mb[:]))
	if inv == nil {
		return Int{}, false
	}
	return FromBytes(inv.Bytes()), true
}
