package trie_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/trie"
)

type blockLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type blockReceipt struct {
	Type              string     `json:"type"`
	Status            string     `json:"status"`
	CumulativeGasUsed string     `json:"cumulativeGasUsed"`
	Logs              []blockLog `json:"logs"`
}

type blockFixture struct {
	RawBlock         string         `json:"rawBlock"`
	TransactionsRoot string         `json:"transactionsRoot"`
	ReceiptsRoot     string         `json:"receiptsRoot"`
	Receipts         []blockReceipt `json:"receipts"`
}

func blockTransactions(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	s := rlp.NewStream(raw)
	block, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if err := block.Skip(); err != nil {
		t.Fatal(err)
	}
	txs, err := block.List()
	if err != nil {
		t.Fatal(err)
	}
	count, err := txs.Count()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for range count {
		kind, err := txs.Kind()
		if err != nil {
			t.Fatal(err)
		}
		var tx []byte
		if kind == rlp.KindList {
			tx, err = txs.Raw()
		} else {
			tx, err = txs.Bytes()
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tx)
	}
	return out
}

func hexQuantity(t *testing.T, s string) uint64 {
	t.Helper()
	v, err := primitives.DecodeHexUint64(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func addToBloom(bloom *[256]byte, item []byte) {
	h := keccak.Sum256(item)
	for i := 0; i < 6; i += 2 {
		bit := (uint(h[i])<<8 | uint(h[i+1])) & 2047
		bloom[255-bit/8] |= 1 << (bit % 8)
	}
}

func encodeReceipt(t *testing.T, r blockReceipt) []byte {
	t.Helper()
	var bloom [256]byte
	enc := rlp.NewEncoder(512)
	enc.ListStart()
	enc.WriteUint64(hexQuantity(t, r.Status))
	enc.WriteUint64(hexQuantity(t, r.CumulativeGasUsed))
	logs := rlp.NewEncoder(512)
	logs.ListStart()
	for _, l := range r.Logs {
		addr := decodeField(t, l.Address)
		addToBloom(&bloom, addr)
		logs.ListStart()
		logs.WriteBytes(addr)
		logs.ListStart()
		for _, topic := range l.Topics {
			b := decodeField(t, topic)
			addToBloom(&bloom, b)
			logs.WriteBytes(b)
		}
		logs.ListEnd()
		logs.WriteBytes(decodeField(t, l.Data))
		logs.ListEnd()
	}
	logs.ListEnd()
	enc.WriteBytes(bloom[:])
	enc.WriteRaw(logs.Encoded())
	enc.ListEnd()
	typ := hexQuantity(t, r.Type)
	if typ == 0 {
		return enc.Encoded()
	}
	return append([]byte{byte(typ)}, enc.Encoded()...)
}

func TestRealBlockRoots(t *testing.T) {
	names := []string{"mainnet_41788340", "moderato_41543965"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var fx blockFixture
			readJSON(t, "testdata/blocks/"+name+".json", &fx)
			txs := blockTransactions(t, decodeField(t, fx.RawBlock))
			if len(txs) != len(fx.Receipts) {
				t.Fatalf("%d txs, %d receipts", len(txs), len(fx.Receipts))
			}
			if got := trie.OrderedRoot(txs).Hex(); got != fx.TransactionsRoot {
				t.Fatalf("transactions root %s want %s", got, fx.TransactionsRoot)
			}
			receipts := make([][]byte, len(fx.Receipts))
			for i, r := range fx.Receipts {
				receipts[i] = encodeReceipt(t, r)
			}
			if got := trie.OrderedRoot(receipts).Hex(); got != fx.ReceiptsRoot {
				t.Fatalf("receipts root %s want %s", got, fx.ReceiptsRoot)
			}
		})
	}
}
