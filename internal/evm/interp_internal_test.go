package evm

import (
	"testing"

	"github.com/gakonst/goempo/internal/u256"
)

// pushImmediate matches reading the immediate into a zeroed 32-byte buffer,
// for every width, every start (the short-prefix path below 8 bytes in) and
// every cut by the code's end.
func TestPushImmediate(t *testing.T) {
	code := make([]byte, 80)
	for i := range code {
		code[i] = byte(i*37 + 11)
	}
	for width := 1; width <= u256.Bytes; width++ {
		for start := 0; start <= len(code); start++ {
			for _, end := range []int{len(code), min(start+width, len(code)), max(start, len(code)-3)} {
				c := code[:end]
				var buf [u256.Bytes]byte
				if start < len(c) {
					copy(buf[u256.Bytes-width:], c[start:min(start+width, len(c))])
				}
				want := u256.FromBytes32(buf)
				if got := pushImmediate(c, start, width); got != want {
					t.Fatalf("width %d start %d len %d: %x want %x", width, start, len(c), got, want)
				}
			}
		}
	}
}

func TestMarkInlineOnlyStock(t *testing.T) {
	table := NewJumpTable(Osaka, OsakaGas())
	if !table[ADD].inline || !table[PUSH1].inline || table[SLOAD].inline {
		t.Fatal("stock inline marks")
	}
	table.Set(ADD, table[ADD])
	if table[ADD].inline {
		t.Fatal("Set clears the inline mark")
	}
	cancun := NewJumpTable(Cancun, OsakaGas())
	if cancun[CLZ].Defined || cancun[CLZ].inline {
		t.Fatal("undefined opcodes stay undefined")
	}
}
