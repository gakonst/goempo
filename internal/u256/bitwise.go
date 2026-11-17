package u256

func (x Int) And(y Int) Int { return Int{x[0] & y[0], x[1] & y[1], x[2] & y[2], x[3] & y[3]} }

func (x Int) Or(y Int) Int { return Int{x[0] | y[0], x[1] | y[1], x[2] | y[2], x[3] | y[3]} }

func (x Int) Xor(y Int) Int { return Int{x[0] ^ y[0], x[1] ^ y[1], x[2] ^ y[2], x[3] ^ y[3]} }

func (x Int) Not() Int { return Int{^x[0], ^x[1], ^x[2], ^x[3]} }

func (x Int) Byte(index Int) Int {
	if !index.IsUint64() {
		return Int{}
	}
	i := index[0]
	if i >= Bytes {
		return Int{}
	}
	fromLow := Bytes - 1 - i
	limb := x[fromLow/limbBytes]
	return FromUint64((limb >> (8 * (fromLow % limbBytes))) & 0xff)
}

func (x Int) SignExtend(size Int) Int {
	if !size.IsUint64() {
		return x
	}
	if size[0] >= Bytes-1 {
		return x
	}
	bit := uint(size[0]*8 + 7)
	mask := One().Lsh(bit + 1).Sub(One())
	if (x[bit/limbBits]>>(bit%limbBits))&1 == 1 {
		return x.Or(mask.Not())
	}
	return x.And(mask)
}

func shiftAmount(shift Int) (uint, bool) {
	if !shift.IsUint64() {
		return 0, false
	}
	if shift[0] >= Bits {
		return 0, false
	}
	return uint(shift[0]), true
}

func (x Int) Shl(shift Int) Int {
	n, ok := shiftAmount(shift)
	if !ok {
		return Int{}
	}
	return x.Lsh(n)
}

func (x Int) Shr(shift Int) Int {
	n, ok := shiftAmount(shift)
	if !ok {
		return Int{}
	}
	return x.Rsh(n)
}

func (x Int) Sar(shift Int) Int {
	n, ok := shiftAmount(shift)
	if !ok {
		if x.IsNegative() {
			return Max()
		}
		return Int{}
	}
	if !x.IsNegative() {
		return x.Rsh(n)
	}
	return x.Not().Rsh(n).Not()
}

func (x Int) Lsh(n uint) Int {
	if n >= Bits {
		return Int{}
	}
	limbs := int(n / limbBits)
	s := n % limbBits
	var z Int
	for i := 0; i < Limbs; i++ {
		src := i - limbs
		if src < 0 {
			continue
		}
		z[i] = x[src] << s
		if s != 0 {
			if src > 0 {
				z[i] |= x[src-1] >> (limbBits - s)
			}
		}
	}
	return z
}

func (x Int) Rsh(n uint) Int {
	if n >= Bits {
		return Int{}
	}
	limbs := int(n / limbBits)
	s := n % limbBits
	var z Int
	for i := 0; i < Limbs; i++ {
		src := i + limbs
		if src >= Limbs {
			continue
		}
		z[i] = x[src] >> s
		if s != 0 {
			if src+1 < Limbs {
				z[i] |= x[src+1] << (limbBits - s)
			}
		}
	}
	return z
}
