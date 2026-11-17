package snappy_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/p2p/snappy"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	if s == "-" {
		return []byte{}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeReferenceVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 5 {
		t.Fatalf("only %d vectors", len(lines))
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("bad line %q", line)
		}
		plain := unhex(t, f[1])
		got, err := snappy.Decode(unhex(t, f[2]))
		if err != nil {
			t.Fatalf("%s: %v", f[0], err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("%s: decoded %x want %x", f[0], got, plain)
		}
		back, err := snappy.Decode(snappy.Encode(plain))
		if err != nil || !bytes.Equal(back, plain) {
			t.Fatalf("%s: round trip %v", f[0], err)
		}
	}
}

func TestEncodeRoundTripsLargeInputs(t *testing.T) {
	for _, size := range []int{0, 1, 59, 60, 61, 255, 256, 257, 65535, 65536, 65537, 200000} {
		plain := make([]byte, size)
		for i := range plain {
			plain[i] = byte(i * 7)
		}
		got, err := snappy.Decode(snappy.Encode(plain))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("size %d: mismatch", size)
		}
	}
}

func TestDecodeRejectsCorruptInput(t *testing.T) {
	cases := [][]byte{
		{},
		{0x05, 0x10, 'a'},
		{0x04, 0x0c, 'a', 'b', 'c', 'd', 0x01},
		{0x08, 0x00, 'a', 0x0d, 0x05},
		{0x02, 0x04, 'a', 'b', 'c'},
	}
	for i, c := range cases {
		if _, err := snappy.Decode(c); !errors.Is(err, snappy.ErrCorrupt) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	huge := []byte{0x80, 0x80, 0x80, 0x10}
	if _, err := snappy.Decode(huge); !errors.Is(err, snappy.ErrTooLarge) {
		t.Fatalf("huge: %v", err)
	}
	if _, err := snappy.DecodedLen(huge); !errors.Is(err, snappy.ErrTooLarge) {
		t.Fatalf("huge len: %v", err)
	}
}

func TestDecodeOverlappingCopy(t *testing.T) {
	got, err := snappy.Decode([]byte{0x0a, 0x04, 'a', 'b', 0x11, 0x02})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abababab"[:2]+"abababab" {
		t.Fatalf("got %q", got)
	}
}
