package bls12381

import (
	"crypto/sha256"
	"math"
	"math/big"

	"github.com/kapetan-io/tiger/assert"
)

type DST []byte

const (
	xmdBlockBytes  = sha256.BlockSize
	xmdHashBytes   = sha256.Size
	xmdBlocksMax   = 255
	xmdOutputMax   = xmdBlocksMax * xmdHashBytes
	dstMax         = 255
	hashFieldBytes = 64
	hashFieldCount = 2
	fp2Degree      = 2
)

const _ = uint(xmdOutputMax - hashFieldCount*fp2Degree*hashFieldBytes)

const _ = uint(xmdOutputMax - dstMax)

const _ = uint(math.MaxUint8 - xmdBlocksMax)

var fpModulusBig = new(big.Int).SetBytes([]byte{
	0x1a, 0x01, 0x11, 0xea, 0x39, 0x7f, 0xe6, 0x9a, 0x4b, 0x1b, 0xa7, 0xb6, 0x43, 0x4b, 0xac, 0xd7,
	0x64, 0x77, 0x4b, 0x84, 0xf3, 0x85, 0x12, 0xbf, 0x67, 0x30, 0xd2, 0xa0, 0xf6, 0xb0, 0xf6, 0x24,
	0x1e, 0xab, 0xff, 0xfe, 0xb1, 0x53, 0xff, 0xff, 0xb9, 0xfe, 0xff, 0xff, 0xff, 0xff, 0xaa, 0xab,
})

func ExpandMessageXMD(dst DST, msg []byte, n int) []byte {
	assert.Ok(len(dst) <= dstMax, "bls12381: domain separation tag longer than 255 bytes")
	assert.Ok(n > 0, "bls12381: expand_message_xmd needs a positive length")
	assert.Ok(n <= xmdOutputMax, "bls12381: expand_message_xmd length too large")
	ell := (n + xmdHashBytes - 1) / xmdHashBytes
	assert.Ok(ell <= xmdBlocksMax, "bls12381: expand_message_xmd block count within 255")
	dstPrime := make([]byte, 0, len(dst)+1)
	dstPrime = append(dstPrime, dst...)
	dstPrime = append(dstPrime, byte(len(dst)))
	first := make([]byte, xmdBlockBytes, xmdBlockBytes+len(msg)+3+len(dstPrime))
	first = append(first, msg...)
	first = append(first, byte(n>>8), byte(n), 0)
	first = append(first, dstPrime...)
	b0 := sha256.Sum256(first)
	block := make([]byte, xmdHashBytes+1+len(dstPrime))
	copy(block, b0[:])
	block[xmdHashBytes] = 1
	copy(block[xmdHashBytes+1:], dstPrime)
	bi := sha256.Sum256(block)
	out := make([]byte, 0, ell*xmdHashBytes)
	out = append(out, bi[:]...)
	for i := 2; i <= ell; i++ {
		for j := 0; j < xmdHashBytes; j++ {
			block[j] = b0[j] ^ bi[j]
		}
		block[xmdHashBytes] = byte(i)
		bi = sha256.Sum256(block)
		out = append(out, bi[:]...)
	}
	return out[:n]
}

func fpFromWide(b []byte) fp {
	assert.Ok(len(b) == hashFieldBytes, "bls12381: hash_to_field element must be 64 bytes")
	v := new(big.Int).SetBytes(b)
	v.Mod(v, fpModulusBig)
	var enc [EncodedFpLength]byte
	v.FillBytes(enc[fpPadding:])
	out, ok := decodeFp(enc[:])
	assert.Ok(ok, "bls12381: reduced field element is canonical")
	return out
}

func hashToFieldFp(dst DST, msg []byte) [hashFieldCount]fp {
	uniform := ExpandMessageXMD(dst, msg, hashFieldCount*hashFieldBytes)
	var out [hashFieldCount]fp
	for i := range out {
		out[i] = fpFromWide(uniform[i*hashFieldBytes : (i+1)*hashFieldBytes])
	}
	return out
}

func hashToFieldFp2(dst DST, msg []byte) [hashFieldCount]fp2 {
	uniform := ExpandMessageXMD(dst, msg, hashFieldCount*fp2Degree*hashFieldBytes)
	var out [hashFieldCount]fp2
	for i := range out {
		off := i * fp2Degree * hashFieldBytes
		out[i] = fp2{
			c0: fpFromWide(uniform[off : off+hashFieldBytes]),
			c1: fpFromWide(uniform[off+hashFieldBytes : off+2*hashFieldBytes]),
		}
	}
	return out
}

func HashToG1(dst DST, msg []byte) G1 {
	u := hashToFieldFp(dst, msg)
	q0 := iso11(sswuG1(u[0])).jac()
	q1 := iso11(sswuG1(u[1])).jac()
	return G1{p: q0.add(q1).mulUint64(g1Cofactor)}
}

func HashToG2(dst DST, msg []byte) G2 {
	u := hashToFieldFp2(dst, msg)
	q0 := iso3(sswuG2(u[0])).jac()
	q1 := iso3(sswuG2(u[1])).jac()
	return G2{p: q0.add(q1).mulBytes(g2CofactorBytes[:])}
}
