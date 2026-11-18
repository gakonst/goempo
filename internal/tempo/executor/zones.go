package executor

import (
	_ "embed"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
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
	// hash is keccak256(code), so a block checks an installed runtime by
	// its code hash instead of comparing kilobytes of code.
	hash Hash
}

var t10ZoneRuntimes, t13ZoneRuntimes = [3]zoneRuntime{
	newZoneRuntime(ZonePortalAddress, t10ZonePortal),
	newZoneRuntime(precompiles.ZoneVerifierAddress, t10ZoneVerifier),
	newZoneRuntime(ZoneMessengerAddress, t10ZoneMessenger),
}, [3]zoneRuntime{
	newZoneRuntime(ZonePortalAddress, t13ZonePortal),
	newZoneRuntime(precompiles.ZoneVerifierAddress, t13ZoneVerifier),
	newZoneRuntime(ZoneMessengerAddress, t13ZoneMessenger),
}

func newZoneRuntime(address Address, code []byte) zoneRuntime {
	assert.Ok(len(code) > 0, "zone runtimes hold code")
	return zoneRuntime{address: address, code: code, hash: keccak.Sum256(code)}
}

func zoneRuntimes(fork hardfork.Fork) [3]zoneRuntime {
	if fork.AtLeast(hardfork.T13) {
		return t13ZoneRuntimes
	}
	return t10ZoneRuntimes
}

// installRuntimes sets each runtime's code where it is not installed yet
// and reports whether it wrote any. A missing account's code hash is zero
// and never matches.
func installRuntimes(st State, runtimes *[3]zoneRuntime) bool {
	wrote := false
	for i := range runtimes {
		if st.CodeHash(runtimes[i].address) == runtimes[i].hash {
			continue
		}
		st.SetCode(runtimes[i].address, runtimes[i].code)
		wrote = true
	}
	return wrote
}

func zoneFactorySlot0() u256.Int {
	var w primitives.Hash
	copy(w[len(w)-zoneOwnerShift-len(zoneFactoryOwner):], zoneFactoryOwner[:])
	w[len(w)-1] = 1
	return u256.FromBytes32(w)
}

// deployZones installs the zone contracts due at rules' fork and reports
// whether it wrote anything; the caller finalizes the writes.
func deployZones(st State, rules chain.Rules) bool {
	wrote := false
	if rules.AtLeast(hardfork.T10) && len(st.Code(precompiles.ZoneFactoryAddress)) == 0 {
		st.SetCode(precompiles.ZoneFactoryAddress, precompileMarker)
		st.SetStorage(precompiles.ZoneFactoryAddress, primitives.Hash{}, zoneFactorySlot0())
		installRuntimes(st, &t10ZoneRuntimes)
		wrote = true
	}
	if rules.AtLeast(hardfork.T13) {
		runtimes := zoneRuntimes(rules.Fork)
		wrote = installRuntimes(st, &runtimes) || wrote
	}
	return wrote
}
