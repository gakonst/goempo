package bls12381

func (p G1) Uncompressed() [G1Length]byte {
	var out [G1Length]byte
	p.p.affine().encode(out[:])
	return out
}

func (p G2) Uncompressed() [G2Length]byte {
	var out [G2Length]byte
	p.p.affine().encode(out[:])
	return out
}

func SignMinSig(sk Scalar, dst DST, msg []byte) G1 {
	return HashToG1(dst, msg).Mul(sk)
}

func VerifyMinSig(pk G2, dst DST, msg []byte, sig G1) bool {
	if pk.IsIdentity() || sig.IsIdentity() {
		return false
	}
	return PairingIsOne([]PairingTerm{
		{A: sig, B: G2Generator().Neg()},
		{A: HashToG1(dst, msg), B: pk},
	})
}
