package builder_test

import (
	"slices"
	"testing"

	"github.com/gakonst/goempo/internal/builder"
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

var (
	token   = primitives.Address{0x20, 0xc0, 11: 0, 19: 0x01}
	reserve = primitives.Address{0x4d, 0x50, 0x50}
	other   = primitives.Address{0x42}
)

func selector(sig string) []byte {
	h := keccak.Sum256([]byte(sig))
	return h[:4]
}

type shape struct {
	words    int
	trailing int
}

func calldata(sig string, sh shape) []byte {
	return append(selector(sig), make([]byte, sh.words*32+sh.trailing)...)
}

func dynamic(to primitives.Address, data []byte) *types.Transaction {
	return &types.Transaction{Data: &types.DynamicFeeTx{To: &to, Data: data}}
}

func rules(f hardfork.Fork) chain.Rules {
	return chain.Rules{ChainID: hardfork.MainnetChainID, Fork: f}
}

func TestPaymentV1IsAnyTIP20Target(t *testing.T) {
	assert.Ok(builder.IsPayment(dynamic(token, []byte{1, 2, 3}), rules(hardfork.T4)), "any call to a TIP-20 address")
	assert.Ok(!builder.IsPayment(dynamic(other, nil), rules(hardfork.T4)), "a call elsewhere is not")
	aa := &types.Transaction{Data: &types.TempoTx{Calls: []types.Call{{To: &token}, {To: &other}}}}
	assert.Ok(!builder.IsPayment(aa, rules(hardfork.T4)), "every AA call must target a TIP-20")
}

func TestPaymentV2NeedsAPaymentSelectorAndExactCalldata(t *testing.T) {
	r := rules(hardfork.T5)
	assert.Ok(builder.IsPayment(dynamic(token, calldata("transfer(address,uint256)", shape{words: 2, trailing: 0})), r), "transfer")
	assert.Ok(builder.IsPayment(dynamic(token, calldata("burnWithMemo(uint256,bytes32)", shape{words: 2, trailing: 0})), r), "burnWithMemo")
	assert.Ok(!builder.IsPayment(dynamic(token, calldata("transfer(address,uint256)", shape{words: 2, trailing: 1})), r), "no trailing bytes before T15")
	assert.Ok(!builder.IsPayment(dynamic(token, calldata("transfer(address,uint256)", shape{words: 1, trailing: 0})), r), "short calldata")
	assert.Ok(!builder.IsPayment(dynamic(token, calldata("balanceOf(address)", shape{words: 1, trailing: 0})), r), "a view selector")
	assert.Ok(!builder.IsPayment(dynamic(other, calldata("transfer(address,uint256)", shape{words: 2, trailing: 0})), r), "not a TIP-20")
	withList := &types.Transaction{Data: &types.DynamicFeeTx{To: &token, Data: calldata("transfer(address,uint256)", shape{words: 2, trailing: 0}), AccessList: types.AccessList{{Address: other}}}}
	assert.Ok(!builder.IsPayment(withList, r), "an access list disqualifies")
}

func TestPaymentV3AllowsAWordOfTrailingBytes(t *testing.T) {
	r := rules(hardfork.T15)
	assert.Ok(builder.IsPayment(dynamic(token, calldata("transfer(address,uint256)", shape{words: 2, trailing: 32})), r), "32 trailing bytes")
	assert.Ok(!builder.IsPayment(dynamic(token, calldata("transfer(address,uint256)", shape{words: 2, trailing: 33})), r), "33 trailing bytes")
}

func TestPaymentChannelReserveStaticCalls(t *testing.T) {
	r := rules(hardfork.T5)
	desc := "(address,address,address,address,bytes32,address,bytes32)"
	assert.Ok(builder.IsPayment(dynamic(reserve, calldata("open(address,address,address,uint96,bytes32,address)", shape{words: 6, trailing: 0})), r), "open")
	assert.Ok(builder.IsPayment(dynamic(reserve, calldata("topUp("+desc+",uint96)", shape{words: 8, trailing: 0})), r), "topUp")
	assert.Ok(builder.IsPayment(dynamic(reserve, calldata("requestClose("+desc+")", shape{words: 7, trailing: 0})), r), "requestClose")
	assert.Ok(!builder.IsPayment(dynamic(reserve, calldata("settle("+desc+",uint96,bytes)", shape{words: 9, trailing: 0})), r), "settle without a valid signature")
}

func TestPaymentAAShapes(t *testing.T) {
	r := rules(hardfork.T5)
	pay := types.Call{To: &token, Input: calldata("transfer(address,uint256)", shape{words: 2, trailing: 0})}
	ok := &types.Transaction{Data: &types.TempoTx{Calls: []types.Call{pay, pay}}}
	assert.Ok(builder.IsPayment(ok, r), "a batch of transfers")
	empty := &types.Transaction{Data: &types.TempoTx{}}
	assert.Ok(!builder.IsPayment(empty, r), "no calls")
	mixed := &types.Transaction{Data: &types.TempoTx{Calls: slices.Concat([]types.Call{pay}, []types.Call{{To: &other}})}}
	assert.Ok(!builder.IsPayment(mixed, r), "one non-payment call")
}
