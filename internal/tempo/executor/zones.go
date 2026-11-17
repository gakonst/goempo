package executor

import (
	"bytes"
	_ "embed"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	//go:embed runtimes/t10_zone_portal.bin
	t10ZonePortal []byte
	//go:embed runtimes/t10_zone_verifier.bin
	t10ZoneVerifier []byte
	//go:embed runtimes/t10_zone_messenger.bin
	t10ZoneMessenger []byte
	//go:embed runtimes/t13_zone_portal.bin
	t13ZonePortal []byte
	//go:embed runtimes/t13_zone_verifier.bin
	t13ZoneVerifier []byte
	//go:embed runtimes/t13_zone_messenger.bin
	t13ZoneMessenger []byte
)

var (
	ZonePortalAddress    = Address{0x5a, 0xd1}
	ZoneMessengerAddress = Address{0x5a, 0x4d}
	zoneFactoryOwner     = Address{
		0xaf, 0x57, 0x1f, 0xd4, 0xb3, 0xad, 0x43, 0xa5, 0x80, 0x7a,
		0x5e, 0x58, 0xbf, 0xb2, 0x5e, 0xa1, 0xab, 0x32, 0x7a, 0x14,
	}
)

const zoneOwnerShift = 4

type zoneRuntime struct {
	address Address
	code    []byte
}

func zoneRuntimes(fork hardfork.Fork) [3]zoneRuntime {
	if fork.AtLeast(hardfork.T13) {
		return [3]zoneRuntime{
			{address: ZonePortalAddress, code: t13ZonePortal},
			{address: precompiles.ZoneVerifierAddress, code: t13ZoneVerifier},
			{address: ZoneMessengerAddress, code: t13ZoneMessenger},
		}
	}
	return [3]zoneRuntime{
		{address: ZonePortalAddress, code: t10ZonePortal},
		{address: precompiles.ZoneVerifierAddress, code: t10ZoneVerifier},
		{address: ZoneMessengerAddress, code: t10ZoneMessenger},
	}
}

func installRuntimes(st State, runtimes [3]zoneRuntime) {
	for i := range runtimes {
		if bytes.Equal(st.Code(runtimes[i].address), runtimes[i].code) {
			continue
		}
		st.SetCode(runtimes[i].address, runtimes[i].code)
	}
}

func zoneFactorySlot0() u256.Int {
	var w primitives.Hash
	copy(w[len(w)-zoneOwnerShift-len(zoneFactoryOwner):], zoneFactoryOwner[:])
	w[len(w)-1] = 1
	return u256.FromBytes32(w)
}

func deployZones(st State, rules chain.Rules) {
	if rules.AtLeast(hardfork.T10) && len(st.Code(precompiles.ZoneFactoryAddress)) == 0 {
		st.SetCode(precompiles.ZoneFactoryAddress, precompileMarker)
		st.SetStorage(precompiles.ZoneFactoryAddress, primitives.Hash{}, zoneFactorySlot0())
		installRuntimes(st, zoneRuntimes(hardfork.T10))
	}
	if rules.AtLeast(hardfork.T13) {
		installRuntimes(st, zoneRuntimes(rules.Fork))
	}
	st.FinalizeTx()
}
