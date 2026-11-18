package precompiles_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
)

func benchTransfer(b *testing.B, input []byte) {
	f := setupToken(hardfork.T13)
	f.st.SetStorage(precompiles.PathUSDAddress, precompiles.TIP20BalanceSlot(alice), u256.Max())
	f.st.FinalizeTx()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := precompiles.NewContext(f.st, hardfork.T13, 1_000_000)
		c.Caller = alice
		if _, err := precompiles.Call(c, precompiles.PathUSDAddress, input); err != nil {
			b.Fatal(err)
		}
		if i%64 == 63 {
			f.st.FinalizeTx()
		}
	}
}

func BenchmarkTIP20Transfer(b *testing.B) {
	benchTransfer(b, calldata("transfer(address,uint256)", word(bob), amount(1)))
}

func BenchmarkTIP20TransferWithMemo(b *testing.B) {
	memo := primitives.Hash{31: 9}
	benchTransfer(b, calldata("transferWithMemo(address,uint256,bytes32)", word(bob), amount(1), memo[:]))
}

func BenchmarkTIP20Approve(b *testing.B) {
	benchTransfer(b, calldata("approve(address,uint256)", word(bob), amount(1)))
}
