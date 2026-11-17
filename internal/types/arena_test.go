package types_test

import (
	"reflect"
	"testing"

	"github.com/gakonst/goempo/internal/types"
)

// BlockDecoder decodes every fixture block to the same value DecodeBlock
// does, before and after a Reset, and DecodeBlock never aliases its input.
func TestBlockDecoderMatchesDecodeBlock(t *testing.T) {
	blocks := readLines[blockFixture](t, "testdata/blocks.jsonl")
	if len(blocks) == 0 {
		t.Fatal("no block fixtures")
	}
	var d types.BlockDecoder
	for round := 0; round < 2; round++ {
		d.Reset()
		for i := range blocks {
			raw := mustHex(t, blocks[i].Raw)
			want, err := types.DecodeBlock(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.DecodeBlock(mustHex(t, blocks[i].Raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("block %d: BlockDecoder differs from DecodeBlock", blocks[i].Number)
			}
			hashes := make([]string, len(want.Transactions))
			for j := range want.Transactions {
				hashes[j] = want.Transactions[j].Hash().Hex()
			}
			clear(raw)
			for j := range want.Transactions {
				if want.Transactions[j].Hash().Hex() != hashes[j] {
					t.Fatalf("block %d tx %d: DecodeBlock aliases its input", blocks[i].Number, j)
				}
			}
		}
	}
}
