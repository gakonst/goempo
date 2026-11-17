package executor_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/executor"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	firstT10Time = 1_787_320_800
	firstT13Time = 1_793_718_000
)

func emptyBlock(timestamp uint64) *types.Block {
	b := &types.Block{}
	b.Header.Inner.Timestamp = timestamp
	b.Header.Inner.Number = 1
	b.Header.Inner.GasLimit = 500_000_000
	b.Header.Inner.BaseFeePerGas = 1
	return b
}

func runEmpty(t *testing.T, st *memstate.State, timestamp uint64) {
	x := &executor.Executor{ChainID: hardfork.MainnetChainID}
	res, err := x.Execute(st, executor.Input{Block: emptyBlock(timestamp), GetHash: func(uint64) primitives.Hash { return primitives.Hash{} }})
	assert.NoError(err, "execute empty block")
	assert.Equal(res.GasUsed, uint64(0), "no gas")
}

func TestZoneFactoryAtT10(t *testing.T) {
	st := memstate.New()
	runEmpty(t, st, firstT10Time-1)
	assert.Equal(len(st.Code(precompiles.ZoneFactoryAddress)), 0, "no factory before T10")
	runEmpty(t, st, firstT10Time)
	assert.Equal(string(st.Code(precompiles.ZoneFactoryAddress)), "\xef", "factory marker")
	slot := st.Storage(precompiles.ZoneFactoryAddress, primitives.Hash{}).Bytes32()
	want := primitives.Hash{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xaf, 0x57, 0x1f, 0xd4, 0xb3, 0xad, 0x43, 0xa5,
		0x80, 0x7a, 0x5e, 0x58, 0xbf, 0xb2, 0x5e, 0xa1, 0xab, 0x32, 0x7a, 0x14, 0x00, 0x00, 0x00, 0x01,
	}
	assert.Equal(slot, [32]byte(want), "factory slot 0 as on mainnet at block 35816049")
	assert.Equal(len(st.Code(executor.ZonePortalAddress)), 23_359, "T10 portal")
	assert.Equal(len(st.Code(precompiles.ZoneVerifierAddress)), 373, "T10 verifier")
	assert.Equal(len(st.Code(executor.ZoneMessengerAddress)), 1_858, "T10 messenger")
	runEmpty(t, st, firstT13Time)
	assert.Equal(len(st.Code(executor.ZonePortalAddress)), 23_994, "T13 portal")
	assert.Equal(len(st.Code(precompiles.ZoneVerifierAddress)), 387, "T13 verifier")
}

func TestBoundaryMarkers(t *testing.T) {
	st := memstate.New()
	runEmpty(t, st, firstT13Time)
	assert.Equal(string(st.Code(precompiles.CurrentCommitteeAddress)), "\xef", "T8 marker")
	assert.Equal(string(st.Code(precompiles.ValidatorConfigV2Address)), "\xef", "T2 marker")
}
