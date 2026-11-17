package bls12381_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/kapetan-io/tiger/assert"
)

type h2cVector struct {
	Msg string
	P   struct {
		X string
		Y string
	}
}

type h2cSuite struct {
	Dst     string
	Vectors []h2cVector
}

func loadH2C(name string) h2cSuite {
	raw, err := os.ReadFile("testdata/" + name)
	assert.NoError(err, "read vectors")
	var suite h2cSuite
	assert.NoError(json.Unmarshal(raw, &suite), "parse vectors")
	assert.Equal(len(suite.Vectors), 5, "RFC 9380 lists five vectors")
	return suite
}

func padFp(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	assert.NoError(err, "hex field element")
	out := make([]byte, bls12381.EncodedFpLength)
	copy(out[len(out)-len(b):], b)
	return out
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "hex")
	return b
}

func TestHashToG1RFC9380(t *testing.T) {
	suite := loadH2C("bls12_381_h2c_g1.json")
	for _, v := range suite.Vectors {
		got := bls12381.HashToG1(bls12381.DST(suite.Dst), []byte(v.Msg)).Uncompressed()
		want := append(padFp(v.P.X), padFp(v.P.Y)...)
		assert.Equal(hex.EncodeToString(got[:]), hex.EncodeToString(want), "hash_to_curve G1 "+v.Msg)
	}
}

func TestHashToG2RFC9380(t *testing.T) {
	suite := loadH2C("bls12_381_h2c_g2.json")
	for _, v := range suite.Vectors {
		got := bls12381.HashToG2(bls12381.DST(suite.Dst), []byte(v.Msg)).Uncompressed()
		x := strings.Split(v.P.X, ",")
		y := strings.Split(v.P.Y, ",")
		want := append(append(append(padFp(x[0]), padFp(x[1])...), padFp(y[0])...), padFp(y[1])...)
		assert.Equal(hex.EncodeToString(got[:]), hex.EncodeToString(want), "hash_to_curve G2 "+v.Msg)
	}
}

func TestMinSigVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/min_sig.txt")
	assert.NoError(err, "read min_sig vectors")
	dst := bls12381.DST("BLS_SIG_BLS12381G1_XMD:SHA-256_SSWU_RO_NUL_")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Ok(len(lines) > 100, "vector count")
	for i, line := range lines {
		parts := strings.Split(line, ":")
		assert.Equal(len(parts), 3, "vector fields")
		sk, err := bls12381.ScalarFromBytes(mustHex(parts[0]))
		assert.NoError(err, "private key")
		msg := mustHex(parts[1])
		got := bls12381.SignMinSig(sk, dst, msg).Bytes()
		assert.Equal(hex.EncodeToString(got[:]), parts[2], "signature")
		if i%16 != 0 {
			continue
		}
		decoded, err := bls12381.DecodeG1(mustHex(parts[2]))
		assert.NoError(err, "decode signature")
		pk := bls12381.G2Generator().Mul(sk)
		assert.Ok(bls12381.VerifyMinSig(pk, dst, msg, decoded), "verifies")
		assert.Ok(!bls12381.VerifyMinSig(pk, dst, msg, decoded.Add(bls12381.G1Generator())), "tampered fails")
		enc := pk.Bytes()
		round, err := bls12381.DecodeG2(enc[:])
		assert.NoError(err, "decode public key")
		assert.Ok(round.Equal(pk), "G2 compression round trips")
	}
}

func TestDecodeRejectsIdentityAndBadFlags(t *testing.T) {
	inf := make([]byte, bls12381.CompressedG1Length)
	inf[0] = 0xc0
	_, err := bls12381.DecodeG1(inf)
	assert.Equal(err, bls12381.ErrIdentity, "identity")
	g := bls12381.G1Generator().Bytes()
	g[0] &^= 0x80
	_, err = bls12381.DecodeG1(g[:])
	assert.Equal(err, bls12381.ErrCompressedFlags, "uncompressed flag")
	_, err = bls12381.DecodeG2(make([]byte, 10))
	assert.Equal(err, bls12381.ErrInputLength, "length")
}

func TestScalarField(t *testing.T) {
	a := bls12381.ScalarFromUint64(7)
	inv, err := a.Inverse()
	assert.NoError(err, "inverse")
	assert.Equal(a.Mul(inv), bls12381.ScalarFromUint64(1), "a * a^-1")
	assert.Ok(a.Sub(a).IsZero(), "a - a")
	_, err = bls12381.Scalar{}.Inverse()
	assert.Equal(err, bls12381.ErrScalarInverse, "zero inverse")
	full := make([]byte, bls12381.ScalarLength)
	for i := range full {
		full[i] = 0xff
	}
	_, err = bls12381.ScalarFromBytes(full)
	assert.Equal(err, bls12381.ErrScalarRange, "range")
}
