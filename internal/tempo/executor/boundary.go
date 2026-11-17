package executor

import (
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
)

type boundaryPrecompile struct {
	fork    hardfork.Fork
	address Address
}

var boundaryPrecompiles = [...]boundaryPrecompile{
	{fork: hardfork.T2, address: precompiles.ValidatorConfigV2Address},
	{fork: hardfork.T3, address: precompiles.SignatureVerifierAddress},
	{fork: hardfork.T3, address: precompiles.AddressRegistryAddress},
	{fork: hardfork.T5, address: precompiles.ChannelReserveAddress},
	{fork: hardfork.T6, address: precompiles.ReceivePolicyGuardAddress},
	{fork: hardfork.T7, address: precompiles.StorageCreditsAddress},
	{fork: hardfork.T8, address: precompiles.CurrentCommitteeAddress},
}

var precompileMarker = []byte{0xef}

func deployBoundaryPrecompiles(st State, rules chain.Rules) {
	for i := range boundaryPrecompiles {
		p := &boundaryPrecompiles[i]
		if !rules.AtLeast(p.fork) {
			continue
		}
		if len(st.Code(p.address)) != 0 {
			continue
		}
		st.SetCode(p.address, precompileMarker)
	}
	st.FinalizeTx()
}
