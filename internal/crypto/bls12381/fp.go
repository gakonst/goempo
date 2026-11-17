package bls12381

import "github.com/kapetan-io/tiger/assert"

const (
	fpByteLength    = 48
	EncodedFpLength = 64
	fpPadding       = EncodedFpLength - fpByteLength
	limbBits        = 64
)

const _ = uint(fpByteLength - fpLimbs*8)

func fpFromUint64(v uint64) fp { return fp{l0: v}.mul(fpR2) }

func (a fp) limbs() [fpLimbs]uint64 { return [fpLimbs]uint64{a.l0, a.l1, a.l2, a.l3, a.l4, a.l5} }

func fpFromLimbs(l [fpLimbs]uint64) fp {
	return fp{l0: l[0], l1: l[1], l2: l[2], l3: l[3], l4: l[4], l5: l[5]}
}

func decodeFp(b []byte) (fp, bool) {
	assert.Ok(len(b) == EncodedFpLength, "bls12381: field element must be 64 bytes")
	for i := 0; i < fpPadding; i++ {
		if b[i] != 0 {
			return fp{}, false
		}
	}
	var l [fpLimbs]uint64
	for i := 0; i < fpLimbs; i++ {
		off := EncodedFpLength - (i+1)*8
		l[i] = beUint64(b[off : off+8])
	}
	raw := fpFromLimbs(l)
	_, borrow := raw.rawSub(fpModulus)
	if !borrow {
		return fp{}, false
	}
	return raw.mul(fpR2), true
}

func (a fp) canonical() fp { return a.mul(fp{l0: 1}) }

func (a fp) putBytes(dst []byte) {
	assert.Ok(len(dst) >= EncodedFpLength, "bls12381: short output")
	clear(dst[:fpPadding])
	l := a.canonical().limbs()
	for i := 0; i < fpLimbs; i++ {
		off := EncodedFpLength - (i+1)*8
		putBeUint64(dst[off:off+8], l[i])
	}
}

func (a fp) exp(e fp) fp {
	l := e.limbs()
	z := fpOne
	for i := fpLimbs - 1; i >= 0; i-- {
		for bit := limbBits - 1; bit >= 0; bit-- {
			z = z.sqr()
			if (l[i]>>uint(bit))&1 == 1 {
				z = z.mul(a)
			}
		}
	}
	return z
}

func (a fp) inv() fp { return a.exp(fpPMinus2) }

func (a fp) sqrt() (fp, bool) {
	y := a.exp(expPPlus1Over4)
	return y, y.sqr() == a
}

func (a fp) sgn0() bool { return a.canonical().l0&1 == 1 }

func beUint64(b []byte) uint64 {
	assert.Ok(len(b) >= 8, "bls12381: short limb")
	return uint64(b[7]) | uint64(b[6])<<8 | uint64(b[5])<<16 | uint64(b[4])<<24 |
		uint64(b[3])<<32 | uint64(b[2])<<40 | uint64(b[1])<<48 | uint64(b[0])<<56
}

func putBeUint64(b []byte, v uint64) {
	assert.Ok(len(b) >= 8, "bls12381: short limb")
	for i := 0; i < 8; i++ {
		b[7-i] = byte(v >> (8 * i))
	}
}
