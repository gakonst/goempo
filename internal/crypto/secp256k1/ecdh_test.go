package secp256k1_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSharedSecretMatchesIndependentVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/ecdh.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 6 {
		t.Fatalf("%d vectors", len(lines))
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("bad line %q", line)
		}
		var key secp256k1.PrivateKey
		var pub secp256k1.PublicKey
		k := unhex(t, f[0])
		p := unhex(t, f[1])
		want := unhex(t, f[2])
		copy(key[:], k)
		copy(pub[:], p)
		got, err := secp256k1.SharedSecret(key, pub)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got[:], want) {
			t.Fatalf("shared %x want %x", got, want)
		}
	}
}

func TestSharedSecretIsSymmetric(t *testing.T) {
	a := secp256k1.PrivateKey{31: 7}
	b := secp256k1.PrivateKey{0: 0x11, 31: 0x42}
	pa, err := a.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	pb, err := b.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	ab, err := secp256k1.SharedSecret(a, pb)
	if err != nil {
		t.Fatal(err)
	}
	ba, err := secp256k1.SharedSecret(b, pa)
	if err != nil {
		t.Fatal(err)
	}
	if ab != ba {
		t.Fatalf("%x != %x", ab, ba)
	}
}

func TestSharedSecretRejectsBadInputs(t *testing.T) {
	good := secp256k1.PrivateKey{31: 1}
	pub, err := good.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secp256k1.SharedSecret(secp256k1.PrivateKey{}, pub); !errors.Is(err, secp256k1.ErrInvalidPrivateKey) {
		t.Fatalf("zero key: %v", err)
	}
	bad := pub
	bad[63] ^= 1
	if _, err := secp256k1.SharedSecret(good, bad); !errors.Is(err, secp256k1.ErrPublicKeyNotOnCurve) {
		t.Fatalf("off-curve key: %v", err)
	}
}

func TestCompressedRoundTrip(t *testing.T) {
	for i := byte(1); i < 20; i++ {
		key := secp256k1.PrivateKey{0: i, 31: i * 3}
		pub, err := key.PublicKey()
		if err != nil {
			t.Fatal(err)
		}
		c := pub.Compressed()
		back, err := secp256k1.PublicKeyFromCompressed(c[:])
		if err != nil {
			t.Fatal(err)
		}
		if back != pub {
			t.Fatalf("round trip %x", c)
		}
	}
	if _, err := secp256k1.PublicKeyFromCompressed([]byte{4, 1}); !errors.Is(err, secp256k1.ErrPublicKeyNotOnCurve) {
		t.Fatalf("bad prefix: %v", err)
	}
}
