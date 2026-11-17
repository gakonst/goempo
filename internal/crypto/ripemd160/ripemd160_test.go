package ripemd160_test

import (
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/ripemd160"
)

func TestKnownDigests(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "0000000000000000000000009c1185a5c5e9fc54612808977ee8f548b2258d31"},
		{name: "abc", in: "abc", want: "0000000000000000000000008eb208f7e05d987a9b044a8e98c6b087f15a0bfc"},
	}
	for _, c := range cases {
		got := ripemd160.Sum([]byte(c.in))
		if hex.EncodeToString(got[:]) != c.want {
			t.Fatalf("ripemd160(%q) = %x", c.in, got)
		}
	}
	if ripemd160.Gas(0) != 600 || ripemd160.Gas(1) != 720 || ripemd160.Gas(33) != 840 {
		t.Fatal("gas")
	}
}
