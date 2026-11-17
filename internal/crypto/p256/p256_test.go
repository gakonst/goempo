package p256_test

import (
	"bufio"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/p256"
	"github.com/gakonst/goempo/internal/primitives"
)

type vector struct {
	input []byte
	valid bool
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	f, err := os.Open("testdata/eest_p256verify.txt.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var out []vector
	for lines := 0; lines < 10000; lines++ {
		if !sc.Scan() {
			return finishVectors(t, sc, out)
		}
		fields := strings.Fields(sc.Text())
		if len(fields) != 6 {
			continue
		}
		in, err := primitives.DecodeHex(fields[5])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, vector{input: in, valid: fields[3] == "32"})
	}
	t.Fatal("fixture too long")
	return nil
}

func finishVectors(t *testing.T, sc *bufio.Scanner, out []vector) []vector {
	t.Helper()
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEESTVectors(t *testing.T) {
	vectors := loadVectors(t)
	valid := 0
	for i, v := range vectors {
		if got := p256.VerifyPrecompile(v.input); got != v.valid {
			t.Fatalf("vector %d (%x): got %v, want %v", i, v.input, got, v.valid)
		}
		if v.valid {
			valid++
		}
	}
	if len(vectors) < 370 || valid < 200 {
		t.Fatalf("only %d vectors, %d valid", len(vectors), valid)
	}
}

type signed struct {
	hash [32]byte
	sig  p256.Signature
	key  p256.PublicKey
}

func signWith(t *testing.T, priv *ecdsa.PrivateKey, hash [32]byte) signed {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	var out signed
	out.hash = hash
	r.FillBytes(out.sig.R[:])
	s.FillBytes(out.sig.S[:])
	pub, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	copy(out.key.X[:], pub[1:33])
	copy(out.key.Y[:], pub[33:65])
	return out
}

func (s signed) input() []byte {
	in := make([]byte, 0, p256.PrecompileInput)
	in = append(in, s.hash[:]...)
	in = append(in, s.sig.R[:]...)
	in = append(in, s.sig.S[:]...)
	in = append(in, s.key.X[:]...)
	return append(in, s.key.Y[:]...)
}

func TestSignVerifyAndEdges(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	n := elliptic.P256().Params().N
	p := elliptic.P256().Params().P
	for i := 0; i < 20; i++ {
		var hash [32]byte
		hash[0] = byte(i)
		hash[31] = byte(i * 7)
		sg := signWith(t, priv, hash)
		if !p256.VerifyPrecompile(sg.input()) {
			t.Fatal("valid signature rejected")
		}
		twin := sg
		s := new(big.Int).SetBytes(sg.sig.S[:])
		new(big.Int).Sub(n, s).FillBytes(twin.sig.S[:])
		if !p256.Verify(twin.hash, twin.sig, twin.key) {
			t.Fatal("high/low s twin rejected: EIP-7951 has no low-s rule")
		}
		if p256.IsLowS(sg.sig.S) == p256.IsLowS(twin.sig.S) {
			t.Fatal("IsLowS does not separate the twins")
		}
		bad := sg.input()
		bad[0] ^= 1
		if p256.VerifyPrecompile(bad) {
			t.Fatal("wrong hash accepted")
		}
	}
	sg := signWith(t, priv, [32]byte{})
	if !p256.Verify(sg.hash, sg.sig, sg.key) {
		t.Fatal("zero hash rejected; EIP-7951 accepts z = 0")
	}
	in := sg.input()
	if p256.VerifyPrecompile(in[:159]) || p256.VerifyPrecompile(append(in, 0)) {
		t.Fatal("wrong length accepted")
	}
	inf := sg
	inf.key = p256.PublicKey{}
	if p256.Verify(inf.hash, inf.sig, inf.key) || inf.key.Valid() {
		t.Fatal("infinity key accepted")
	}
	wrapped := sg
	x := new(big.Int).SetBytes(sg.key.X[:])
	x.Add(x, p)
	if x.BitLen() <= 256 {
		x.FillBytes(wrapped.key.X[:])
		if p256.Verify(wrapped.hash, wrapped.sig, wrapped.key) {
			t.Fatal("x + p accepted")
		}
	}
	zero := sg
	zero.sig.R = [32]byte{}
	if p256.Verify(zero.hash, zero.sig, zero.key) {
		t.Fatal("r = 0 accepted")
	}
	big := sg
	n.FillBytes(big.sig.S[:])
	if p256.Verify(big.hash, big.sig, big.key) {
		t.Fatal("s = n accepted")
	}
}
