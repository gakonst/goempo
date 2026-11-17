package primitives_test

import (
	"encoding/json"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

func TestEncodeDecodeHex(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{name: "empty", in: []byte{}, want: "0x"},
		{name: "one", in: []byte{0x01}, want: "0x01"},
		{name: "mixed", in: []byte{0xde, 0xad, 0xbe, 0xef, 0x00}, want: "0xdeadbeef00"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := primitives.EncodeHex(test.in)
			if got != test.want {
				t.Fatalf("encode got %q want %q", got, test.want)
			}
			back, err := primitives.DecodeHex(got)
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != string(test.in) {
				t.Fatalf("decode got %x", back)
			}
		})
	}
}

func TestDecodeHexErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{name: "odd", in: "0xabc", want: primitives.ErrHexOddLength},
		{name: "digit", in: "0xzz", want: primitives.ErrHexDigit},
		{name: "low digit", in: "0x0g", want: primitives.ErrHexDigit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := primitives.DecodeHex(test.in)
			if err != test.want {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
	got, err := primitives.DecodeHex("ABcd")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0xab {
		t.Fatalf("got %x", got)
	}
}

func TestAddressHash(t *testing.T) {
	a, err := primitives.HexToAddress("0x20C0000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if a.Hex() != "0x20c0000000000000000000000000000000000000" {
		t.Fatalf("got %s", a)
	}
	if _, err := primitives.HexToAddress("0x20c0"); err != primitives.ErrHexLength {
		t.Fatalf("short address: %v", err)
	}
	b := primitives.BytesToAddress([]byte{1, 2})
	if b[18] != 1 {
		t.Fatalf("left pad: %x", b)
	}
	if b[19] != 2 {
		t.Fatalf("left pad: %x", b)
	}
	long := make([]byte, 25)
	long[5] = 9
	if primitives.BytesToAddress(long)[0] != 9 {
		t.Fatal("truncate keeps the right bytes")
	}
	h := primitives.BytesToHash([]byte{7})
	if h[31] != 7 {
		t.Fatal("hash pad")
	}
	if !(primitives.Hash{}).IsZero() {
		t.Fatal("zero hash")
	}
	if h.Compare(primitives.Hash{}) != 1 {
		t.Fatal("compare")
	}
	if primitives.Uint64ToHash(258) != primitives.BytesToHash([]byte{1, 2}) {
		t.Fatal("uint64 to hash")
	}
	h2, err := primitives.HexToHash(h.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h {
		t.Fatal("hash round trip")
	}
}

func TestJSONText(t *testing.T) {
	type doc struct {
		A primitives.Address
		H primitives.Hash
	}
	in := doc{A: primitives.BytesToAddress([]byte{0xaa}), H: primitives.BytesToHash([]byte{0xbb})}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out doc
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip %s", raw)
	}
	err = json.Unmarshal([]byte(`{"A":"0x12"}`), &out)
	if err == nil {
		t.Fatal("short address accepted")
	}
}

func TestBigEndian(t *testing.T) {
	cases := []struct {
		name string
		v    uint64
		want string
	}{
		{name: "zero", v: 0, want: "0x"},
		{name: "byte", v: 0x7f, want: "0x7f"},
		{name: "two", v: 0x0100, want: "0x0100"},
		{name: "max", v: ^uint64(0), want: "0xffffffffffffffff"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := primitives.AppendUint64Minimal(nil, test.v)
			if primitives.EncodeHex(got) != test.want {
				t.Fatalf("got %x", got)
			}
			back, err := primitives.Uint64FromBytes(got)
			if err != nil {
				t.Fatal(err)
			}
			assert.Equal(back, test.v, "round trip")
		})
	}
	if _, err := primitives.Uint64FromBytes(make([]byte, 9)); err != primitives.ErrUint64Overflow {
		t.Fatal("overflow")
	}
	if len(primitives.TrimLeftZeros([]byte{0, 0, 1, 0})) != 2 {
		t.Fatal("trim")
	}
	if len(primitives.TrimLeftZeros([]byte{0, 0})) != 0 {
		t.Fatal("trim all")
	}
}

func TestHexUint64(t *testing.T) {
	cases := []struct {
		name string
		v    uint64
		want string
	}{
		{name: "zero", v: 0, want: "0x0"},
		{name: "small", v: 0x1074, want: "0x1074"},
		{name: "max", v: ^uint64(0), want: "0xffffffffffffffff"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := primitives.EncodeHexUint64(test.v)
			if got != test.want {
				t.Fatalf("got %s", got)
			}
			back, err := primitives.DecodeHexUint64(got)
			if err != nil {
				t.Fatal(err)
			}
			assert.Equal(back, test.v, "round trip")
		})
	}
	if _, err := primitives.DecodeHexUint64("0x10000000000000000"); err != primitives.ErrHexLength {
		t.Fatal("too long")
	}
	if _, err := primitives.DecodeHexUint64("0x"); err != primitives.ErrHexLength {
		t.Fatal("empty")
	}
}
