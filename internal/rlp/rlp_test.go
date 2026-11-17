package rlp_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

type vector struct {
	In  json.RawMessage `json:"in"`
	Out string          `json:"out"`
}

func loadVectors(t *testing.T, path string) map[string]vector {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]vector{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func sortedNames(m map[string]vector) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type frame struct {
	value   any
	listEnd bool
}

func encodeJSON(t *testing.T, in json.RawMessage) []byte {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		t.Fatal(err)
	}
	enc := rlp.NewEncoder(0)
	stack := []frame{{value: root}}
	for steps := 0; steps < 100000; steps++ {
		if len(stack) == 0 {
			return enc.Encoded()
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.listEnd {
			enc.ListEnd()
			continue
		}
		switch v := top.value.(type) {
		case []any:
			enc.ListStart()
			stack = append(stack, frame{listEnd: true})
			for i := len(v) - 1; i >= 0; i-- {
				stack = append(stack, frame{value: v[i]})
			}
		case json.Number:
			n, ok := new(big.Int).SetString(v.String(), 10)
			if !ok {
				t.Fatalf("number %s", v)
			}
			enc.WriteUintBytes(n.Bytes())
		case string:
			if strings.HasPrefix(v, "#") {
				n, ok := new(big.Int).SetString(v[1:], 10)
				if !ok {
					t.Fatalf("bignum %s", v)
				}
				enc.WriteUintBytes(n.Bytes())
				continue
			}
			enc.WriteString(v)
		default:
			t.Fatalf("unexpected json value %T", v)
		}
	}
	t.Fatal("json too large")
	return nil
}

func TestEthereumRLPTests(t *testing.T) {
	vectors := loadVectors(t, "testdata/rlptest.json")
	if len(vectors) < 20 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, name := range sortedNames(vectors) {
		v := vectors[name]
		t.Run(name, func(t *testing.T) {
			want, err := primitives.DecodeHex(v.Out)
			if err != nil {
				t.Fatal(err)
			}
			got := encodeJSON(t, v.In)
			if !bytes.Equal(got, want) {
				t.Fatalf("encode got %x want %x", got, want)
			}
			if err := rlp.Validate(want); err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}

func TestEthereumInvalidRLPTests(t *testing.T) {
	vectors := loadVectors(t, "testdata/invalidRLPTest.json")
	if len(vectors) < 20 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, name := range sortedNames(vectors) {
		v := vectors[name]
		t.Run(name, func(t *testing.T) {
			in, err := primitives.DecodeHex(v.Out)
			if err != nil {
				t.Fatal(err)
			}
			if err := rlp.Validate(in); err == nil {
				t.Fatalf("accepted %x", in)
			}
		})
	}
}

func TestStreamErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{name: "single byte wrapped", in: "0x8105", want: rlp.ErrSingleByte},
		{name: "short string past end", in: "0x83aabb", want: rlp.ErrUnexpectedEOF},
		{name: "long size leading zero", in: "0xb90040", want: rlp.ErrNonCanonicalSize},
		{name: "long size too small", in: "0xb801ff", want: rlp.ErrNonCanonicalSize},
		{name: "list past end", in: "0xc501", want: rlp.ErrUnexpectedEOF},
		{name: "long list header past end", in: "0xf9", want: rlp.ErrUnexpectedEOF},
		{name: "empty input", in: "0x", want: rlp.ErrEmpty},
		{name: "trailing", in: "0x0102", want: rlp.ErrTrailingBytes},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			in, err := primitives.DecodeHex(test.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := rlp.Validate(in); got != test.want {
				t.Fatalf("validate got %v want %v", got, test.want)
			}
			_, got := rlp.DecodeBytes(in)
			if got != test.want {
				t.Fatalf("decode got %v want %v", got, test.want)
			}
		})
	}
}

func TestUint64(t *testing.T) {
	cases := []struct {
		name string
		v    uint64
		want string
	}{
		{name: "zero", v: 0, want: "0x80"},
		{name: "one", v: 1, want: "0x01"},
		{name: "127", v: 0x7f, want: "0x7f"},
		{name: "128", v: 0x80, want: "0x8180"},
		{name: "1024", v: 0x400, want: "0x820400"},
		{name: "max", v: ^uint64(0), want: "0x88ffffffffffffffff"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := rlp.EncodeUint64(test.v)
			if primitives.EncodeHex(got) != test.want {
				t.Fatalf("got %x", got)
			}
			if len(got) != rlp.Uint64Size(test.v) {
				t.Fatalf("size %d", rlp.Uint64Size(test.v))
			}
			back, err := rlp.DecodeUint64(got)
			if err != nil {
				t.Fatal(err)
			}
			if back != test.v {
				t.Fatalf("back %d", back)
			}
		})
	}
	bad := []struct {
		name string
		in   string
		want error
	}{
		{name: "leading zero", in: "0x820001", want: rlp.ErrLeadingZero},
		{name: "zero byte", in: "0x00", want: rlp.ErrLeadingZero},
		{name: "nine bytes", in: "0x89010000000000000000", want: rlp.ErrUintOverflow},
		{name: "list", in: "0xc0", want: rlp.ErrExpectedString},
	}
	for _, test := range bad {
		t.Run(test.name, func(t *testing.T) {
			in, err := primitives.DecodeHex(test.in)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rlp.DecodeUint64(in); err != test.want {
				t.Fatalf("got %v want %v", err, test.want)
			}
		})
	}
}

func TestStreamWalk(t *testing.T) {
	enc := rlp.NewEncoder(64)
	enc.ListStart()
	enc.WriteUint64(7)
	addr := primitives.BytesToAddress([]byte{0xaa, 0xbb})
	enc.WriteBytes(addr[:])
	enc.ListStart()
	enc.WriteString("dog")
	enc.ListEnd()
	long := bytes.Repeat([]byte{0x11}, 70)
	enc.WriteBytes(long)
	enc.ListEnd()
	out := enc.Encoded()
	if out[0] != 0xf8 {
		t.Fatalf("long list header %x", out[:2])
	}
	if len(out) != rlp.ListSize(1+rlp.BytesSize(addr[:])+5+rlp.BytesSize(long)) {
		t.Fatal("list size")
	}

	s := rlp.NewStream(out)
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(); err != nil {
		t.Fatal(err)
	}
	if list.Depth() != 1 {
		t.Fatal("depth")
	}
	n, err := list.Count()
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("count %d", n)
	}
	v, err := list.Uint64()
	if err != nil {
		t.Fatal(err)
	}
	if v != 7 {
		t.Fatal("uint")
	}
	gotAddr, err := list.Address()
	if err != nil {
		t.Fatal(err)
	}
	if gotAddr != addr {
		t.Fatal("address")
	}
	kind, err := list.Kind()
	if err != nil {
		t.Fatal(err)
	}
	if kind != rlp.KindList {
		t.Fatal("kind")
	}
	raw, err := list.Raw()
	if err != nil {
		t.Fatal(err)
	}
	if primitives.EncodeHex(raw) != "0xc483646f67" {
		t.Fatalf("raw %x", raw)
	}
	if _, err := list.Hash(); err != rlp.ErrLength {
		t.Fatalf("hash of 70 bytes: %v", err)
	}
	got, err := list.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, long) {
		t.Fatal("long")
	}
	if !list.Empty() {
		t.Fatal("not empty")
	}
	if _, err := list.Bytes(); err != rlp.ErrEmpty {
		t.Fatal("past end")
	}
	if len(list.Rest()) != 0 {
		t.Fatal("rest")
	}
}

func TestDepth(t *testing.T) {
	nested := bytes.Repeat([]byte{0xc1}, rlp.DepthMax)
	nested = append(nested, 0xc0)
	nested[len(nested)-2] = 0xc1
	enc := rlp.NewEncoder(0)
	for i := 0; i < rlp.DepthMax; i++ {
		enc.ListStart()
	}
	for i := 0; i < rlp.DepthMax; i++ {
		enc.ListEnd()
	}
	ok := enc.Encoded()
	if err := rlp.Validate(ok); err != nil {
		t.Fatalf("depth max: %v", err)
	}
	if err := rlp.Validate(rlp.EncodeList(ok)); err != rlp.ErrTooDeep {
		t.Fatalf("too deep: %v", err)
	}
}

func TestSplitAndHelpers(t *testing.T) {
	list := rlp.EncodeList(rlp.EncodeBytes([]byte("cat")), rlp.EncodeUint64(1024))
	it, err := rlp.Split(list)
	if err != nil {
		t.Fatal(err)
	}
	if it.Kind != rlp.KindList {
		t.Fatal("kind")
	}
	if len(it.Rest) != 0 {
		t.Fatal("rest")
	}
	first, err := rlp.Split(it.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Content) != "cat" {
		t.Fatal("content")
	}
	top := new(big.Int).Lsh(big.NewInt(1), 255)
	padded := make([]byte, 40)
	top.FillBytes(padded)
	enc := rlp.NewEncoder(0)
	enc.WriteUintBytes(padded)
	s := rlp.NewStream(enc.Encoded())
	got, err := s.UintBytes(32)
	if err != nil {
		t.Fatal(err)
	}
	if new(big.Int).SetBytes(got).Cmp(top) != 0 {
		t.Fatal("uint bytes")
	}
	s = rlp.NewStream(enc.Encoded())
	if _, err := s.UintBytes(31); err != rlp.ErrUintOverflow {
		t.Fatal("uint bytes max")
	}
}

func FuzzValidateAgreesWithStream(f *testing.F) {
	f.Add([]byte{0xc4, 0x83, 'd', 'o', 'g'})
	f.Add([]byte{0xb8, 0x38})
	f.Fuzz(func(t *testing.T, in []byte) {
		if rlp.Validate(in) != nil {
			return
		}
		s := rlp.NewStream(in)
		raw, err := s.Raw()
		if err != nil {
			t.Fatal(err)
		}
		if s.Finish() != nil {
			t.Fatal("validate accepted trailing bytes")
		}
		if !bytes.Equal(raw, in) {
			t.Fatal("raw")
		}
	})
}
