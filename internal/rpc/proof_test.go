package rpc_test

import (
	"encoding/json"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type proofState struct {
	head  uint64
	asked []primitives.Hash
}

func (p *proofState) StateAt(uint64) (rpc.StateReader, error) { return nil, nil }

func (p *proofState) ProofAt(number uint64, addr primitives.Address, keys []primitives.Hash) (rpc.AccountProof, error) {
	p.asked = keys
	if addr == (primitives.Address{0xee}) {
		return rpc.AccountProof{Storage: make([]rpc.StorageProof, len(keys))}, nil
	}
	out := rpc.AccountProof{Exists: true, Balance: u256.FromUint64(10), Nonce: number, CodeHash: primitives.Hash{1}, StorageRoot: primitives.Hash{2}, Proof: [][]byte{{0xc0}}}
	for range keys {
		out.Storage = append(out.Storage, rpc.StorageProof{Value: u256.FromUint64(3), Proof: [][]byte{{0x80}}})
	}
	return out, nil
}

func call(t *testing.T, srv *rpc.Server, req string) map[string]any {
	resp := srv.Handle([]byte(req))
	var out map[string]any
	assert.NoError(json.Unmarshal(resp.Body, &out), "response json")
	return out
}

func TestGetProofAndAccount(t *testing.T) {
	chain := loadChain()
	st := &proofState{}
	srv := rpc.NewServer(rpc.Config{ProofWindow: 1 << 40}, chain, st, nil)
	out := call(t, srv, `{"jsonrpc":"2.0","id":1,"method":"eth_getProof","params":["0x0100000000000000000000000000000000000000",["0x02","0x0000000000000000000000000000000000000000000000000000000000000001"],"latest"]}`)
	res, ok := out["result"].(map[string]any)
	assert.Ok(ok, "proof result")
	assert.Equal(res["balance"], any("0xa"), "balance")
	assert.Equal(res["storageHash"], any(primitives.Hash{2}.Hex()), "storage root")
	sp := res["storageProof"].([]any)
	assert.Equal(sp[0].(map[string]any)["key"], any("0x2"), "short key echoed as a quantity")
	assert.Equal(sp[1].(map[string]any)["key"], any("0x0000000000000000000000000000000000000000000000000000000000000001"), "full key echoed as given")
	assert.Equal(st.asked[1], primitives.Hash{31: 1}, "key decoded")
	acct := call(t, srv, `{"jsonrpc":"2.0","id":2,"method":"eth_getAccount","params":["0x0100000000000000000000000000000000000000","latest"]}`)
	assert.Equal(acct["result"].(map[string]any)["storageRoot"], any(primitives.Hash{2}.Hex()), "account storage root")
	missing := call(t, srv, `{"jsonrpc":"2.0","id":3,"method":"eth_getAccount","params":["0xee00000000000000000000000000000000000000","latest"]}`)
	assert.Equal(missing["result"], any(nil), "absent account is null")
}
