package genesis_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/genesis"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/trie"
)

func TestMainnetStateRoot(t *testing.T) {
	g, err := genesis.Mainnet()
	if err != nil {
		t.Fatal(err)
	}
	if g.Config.ChainID != genesis.MainnetChainID {
		t.Fatalf("chain id %d", g.Config.ChainID)
	}
	if len(g.Alloc) != 14 {
		t.Fatalf("alloc %d accounts", len(g.Alloc))
	}
	if g.GasLimit != 500000000 {
		t.Fatalf("gas limit %d", g.GasLimit)
	}
	if g.BaseFee != 10000000000 {
		t.Fatalf("base fee %d", g.BaseFee)
	}
	if g.Config.ForkTimes["t1"] != 1770908400 {
		t.Fatalf("t1 time %d", g.Config.ForkTimes["t1"])
	}
	if got := g.StateRoot(); got != genesis.MainnetStateRoot {
		t.Fatalf("state root %s want %s", got, genesis.MainnetStateRoot)
	}
}

func TestAccountEncoding(t *testing.T) {
	g, err := genesis.Parse([]byte(`{"config":{"chainId":1,"pragueTime":5},"nonce":"0x0","alloc":{
		"0x00000000000000000000000000000000000000aa":{"balance":"0x1"},
		"0x00000000000000000000000000000000000000bb":{"nonce":"0x2","code":"0x6000","storage":{"0x01":"0x0","0x02":"0x0100"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if g.Config.ForkTimes["prague"] != 5 {
		t.Fatal("fork time")
	}
	plain := &g.Alloc[0]
	if plain.StorageRoot() != trie.EmptyRoot {
		t.Fatal("empty storage root")
	}
	enc := plain.EncodeRLP(trie.EmptyRoot)
	want := "0xf8448001a056e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421a0c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"
	if primitives.EncodeHex(enc) != want {
		t.Fatalf("account rlp %x", enc)
	}
	withCode := &g.Alloc[1]
	storage := trie.NewSecure()
	key := primitives.Uint64ToHash(2)
	storage.Update(key[:], []byte{0x82, 0x01, 0x00})
	if withCode.StorageRoot() != storage.Hash() {
		t.Fatal("storage root skips zero slots and trims values")
	}
	state := trie.NewSecure()
	state.Update(plain.Address[:], enc)
	state.Update(withCode.Address[:], withCode.EncodeRLP(storage.Hash()))
	if g.StateRoot() != state.Hash() {
		t.Fatal("state root")
	}
	if keccak.Sum256(withCode.Code).IsZero() {
		t.Fatal("code hash")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{name: "no chain", in: `{"config":{}}`},
		{name: "bad address", in: `{"config":{"chainId":1},"alloc":{"0x12":{}}}`},
		{name: "balance too long", in: `{"config":{"chainId":1},"alloc":{"0x00000000000000000000000000000000000000aa":{"balance":"0x1000000000000000000000000000000000000000000000000000000000000000000"}}}`},
		{name: "balance no prefix", in: `{"config":{"chainId":1},"alloc":{"0x00000000000000000000000000000000000000aa":{"balance":"10"}}}`},
		{name: "bad json", in: `{`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := genesis.Parse([]byte(test.in)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
