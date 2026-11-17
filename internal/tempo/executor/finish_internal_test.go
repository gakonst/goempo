package executor

import (
	"testing"

	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/trie"
	"github.com/gakonst/goempo/internal/types"
)

// receiptsRoot's one-receipt shortcut and finish's pooled encodings give
// the same root as trie.OrderedRoot over separately encoded receipts.
func TestReceiptsRootMatchesOrderedRoot(t *testing.T) {
	for n := 0; n < 300; n += 1 + n/2 {
		receipts := make([]types.Receipt, n)
		encoded := make([][]byte, n)
		for i := range receipts {
			receipts[i] = types.Receipt{
				Type:              types.TxType(i % 3),
				Success:           i%2 == 0,
				CumulativeGasUsed: uint64(21_000 * (i + 1)),
				Logs:              []types.Log{{Address: Address{byte(i)}, Data: make([]byte, i%70)}},
			}
			receipts[i].Bloom = types.LogsBloom(receipts[i].Logs)
			encoded[i] = receipts[i].Encode()
		}
		want := trie.OrderedRoot(encoded)
		if got := finish(receipts, 0).ReceiptsRoot; got != want {
			t.Fatalf("%d receipts: finish root %s, want %s", n, got, want)
		}
		if got := receiptsRoot(rlp.NewEncoder(0), encoded); got != want {
			t.Fatalf("%d receipts: receiptsRoot %s, want %s", n, got, want)
		}
	}
}
