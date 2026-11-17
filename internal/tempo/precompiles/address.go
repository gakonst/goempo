package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

func prefixAddress(prefix ...byte) Address {
	var a Address
	copy(a[:], prefix)
	return a
}

var (
	TIP403RegistryAddress      = prefixAddress(0x40, 0x3c)
	FeeManagerAddress          = prefixAddress(0xfe, 0xec)
	StablecoinDEXAddress       = prefixAddress(0xde, 0xc0)
	NonceManagerAddress        = prefixAddress(0x4e, 0x4f, 0x4e, 0x43, 0x45)
	AccountKeychainAddress     = prefixAddress(0xaa, 0xaa, 0xaa, 0xaa)
	ValidatorConfigAddress     = prefixAddress(0xcc, 0xcc, 0xcc, 0xcc)
	ValidatorConfigV2Address   = validatorConfigV2()
	TIP20FactoryAddress        = prefixAddress(0x20, 0xfc)
	AddressRegistryAddress     = prefixAddress(0xfd, 0xc0)
	SignatureVerifierAddress   = prefixAddress(0x51, 0x65, 0x30)
	ChannelReserveAddress      = prefixAddress(0x4d, 0x50, 0x50)
	ReceivePolicyGuardAddress  = prefixAddress(0xb1, 0x0c)
	StorageCreditsAddress      = prefixAddress(0x10, 0x60)
	CurrentCommitteeAddress    = prefixAddress(0xc0, 0x77, 0xe0)
	ZoneFactoryAddress         = prefixAddress(0x5a, 0xf2)
	ZoneVerifierAddress        = prefixAddress(0x5a, 0x56)
	ConfigurableAccountAddress = prefixAddress(0xaa, 0xcc)
	PathUSDAddress             = prefixAddress(0x20, 0xc0)
)

var tip20Prefix = [12]byte{0x20, 0xc0}

func validatorConfigV2() Address {
	a := prefixAddress(0xcc, 0xcc, 0xcc, 0xcc)
	a[19] = 1
	return a
}

type systemPrecompile struct {
	address Address
	since   hardfork.Fork
}

var systemPrecompiles = [...]systemPrecompile{
	{TIP403RegistryAddress, hardfork.Genesis},
	{FeeManagerAddress, hardfork.Genesis},
	{StablecoinDEXAddress, hardfork.Genesis},
	{NonceManagerAddress, hardfork.Genesis},
	{AccountKeychainAddress, hardfork.Genesis},
	{ValidatorConfigAddress, hardfork.Genesis},
	{ValidatorConfigV2Address, hardfork.Genesis},
	{TIP20FactoryAddress, hardfork.Genesis},
	{AddressRegistryAddress, hardfork.T3},
	{SignatureVerifierAddress, hardfork.T3},
	{ChannelReserveAddress, hardfork.T5},
	{ReceivePolicyGuardAddress, hardfork.T6},
	{StorageCreditsAddress, hardfork.T7},
	{CurrentCommitteeAddress, hardfork.T8},
	{ZoneFactoryAddress, hardfork.T10},
	{ZoneVerifierAddress, hardfork.T13},
	{ConfigurableAccountAddress, hardfork.T14},
}

func IsTIP20(a Address) bool { return [12]byte(a[:12]) == tip20Prefix }

func TIP20ID(a Address) (uint64, bool) {
	if !IsTIP20(a) {
		return 0, false
	}
	id := uint64(0)
	for _, b := range a[12:] {
		id = id<<8 | uint64(b)
	}
	return id, true
}

func TIP20Address(id uint64) Address {
	a := Address{}
	copy(a[:], tip20Prefix[:])
	for i := 19; i >= 12; i-- {
		a[i] = byte(id)
		id >>= 8
	}
	return a
}

func IsPrecompile(a Address, fork hardfork.Fork) bool {
	if IsTIP20(a) {
		return true
	}
	for _, p := range systemPrecompiles {
		if p.address == a && fork >= p.since {
			return true
		}
	}
	return false
}

var virtualMagic = [10]byte{0xfd, 0xfd, 0xfd, 0xfd, 0xfd, 0xfd, 0xfd, 0xfd, 0xfd, 0xfd}

func IsVirtual(a Address) bool { return [10]byte(a[4:14]) == virtualMagic }

func virtualMaster(a Address) [4]byte { return [4]byte(a[:4]) }
