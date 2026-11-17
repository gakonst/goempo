package bn254

import "github.com/kapetan-io/tiger/assert"

const (
	ElementLength = 32
	limbBits      = 64
)

func fpFromLimbs(l [fpLimbs]uint64) fp { return fp{l0: l[0], l1: l[1], l2: l[2], l3: l[3]} }

func (a fp) limbs() [fpLimbs]uint64 { return [fpLimbs]uint64{a.l0, a.l1, a.l2, a.l3} }

func fpFromUint64(v uint64) fp { return fp{l0: v}.mul(fpR2) }

func fpFromBytes(b []byte) (fp, bool) {
	assert.Ok(len(b) == ElementLength, "bn254: field element must be 32 bytes")
	var l [fpLimbs]uint64
	for i := 0; i < fpLimbs; i++ {
		off := ElementLength - (i+1)*8
		l[i] = beUint64(b[off : off+8])
	}
	raw := fpFromLimbs(l)
	_, borrow := raw.rawSub(fpModulus)
	if !borrow {
		return fp{}, false
	}
	return raw.mul(fpR2), true
}

func (a fp) putBytes(dst []byte) {
	assert.Ok(len(dst) >= ElementLength, "bn254: short output")
	l := a.mul(fp{l0: 1}).limbs()
	for i := 0; i < fpLimbs; i++ {
		off := ElementLength - (i+1)*8
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

func beUint64(b []byte) uint64 {
	assert.Ok(len(b) >= 8, "bn254: short limb")
	return uint64(b[7]) | uint64(b[6])<<8 | uint64(b[5])<<16 | uint64(b[4])<<24 |
		uint64(b[3])<<32 | uint64(b[2])<<40 | uint64(b[1])<<48 | uint64(b[0])<<56
}

func putBeUint64(b []byte, v uint64) {
	assert.Ok(len(b) >= 8, "bn254: short limb")
	for i := 0; i < 8; i++ {
		b[7-i] = byte(v >> (8 * i))
	}
}
