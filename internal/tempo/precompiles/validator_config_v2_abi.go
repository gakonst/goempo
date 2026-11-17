package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

func v2ValidatorTuple(r v2Record) []byte {
	return newEncoder(8).word(r.publicKey).addr(r.validator).bytes(r.ingress).bytes(r.egress).
		addr(r.fee).u64(r.index).u64(r.added).u64(r.deactivated).out()
}

func (c *Context) v2ValidatorAtIndex1(index1 uint64) ([]byte, error) {
	if index1 == 0 {
		return nil, errSel("ValidatorNotFound()")
	}
	r, err := c.v2ReadRecord(index1 - 1)
	if err != nil {
		return nil, err
	}
	return wrapDynamic(v2ValidatorTuple(r)), nil
}

func (c *Context) v2ActiveRecords() ([]v2Record, error) {
	n, err := c.v2ActiveLen()
	if err != nil {
		return nil, err
	}
	if n > maxValidators {
		return nil, errFatal
	}
	out := make([]v2Record, 0, n)
	for i := uint64(0); i < n; i++ {
		idx1, err := c.v2ActiveAt(i)
		if err != nil {
			return nil, err
		}
		if idx1 == 0 {
			return nil, underOverflow()
		}
		r, err := c.v2ReadRecord(idx1 - 1)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (c *Context) v2GetActiveValidators() ([]byte, error) {
	recs, err := c.v2ActiveRecords()
	if err != nil {
		return nil, err
	}
	n := uint64(len(recs))
	heads := make([]byte, 0, 32*n)
	tails := make([]byte, 0)
	for _, r := range recs {
		off := wordOfU64(32*n + uint64(len(tails)))
		heads = append(heads, off[:]...)
		tails = append(tails, v2ValidatorTuple(r)...)
	}
	body := append(encodeWords(wordOfU64(n)), heads...)
	return wrapDynamic(append(body, tails...)), nil
}

func v2EndpointArgs(pair []abiValue) endpoints {
	return endpoints{ingress: pair[0].data, egress: pair[1].data}
}

var validatorConfigV2Table = register(ValidatorConfigV2Address, newTable(
	view("owner()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		k, err := c.v2ReadConfig()
		return encodeWords(addressWord(k.owner())), err
	}).from(hardfork.T2),
	view("getActiveValidators()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		return c.v2GetActiveValidators()
	}).from(hardfork.T2),
	view("getInitializedAtHeight()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		w, err := c.loadField(ValidatorConfigV2Address, slotV2Config, fV2InitHeight)
		return encodeWords(toHash(w)), err
	}).from(hardfork.T2),
	view("validatorCount()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		n, err := c.v2Count()
		return encodeWords(wordOfU64(n)), err
	}).from(hardfork.T2),
	view("validatorByIndex(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		n, err := c.v2Count()
		if err != nil {
			return nil, err
		}
		if a[0].u64() >= n {
			return nil, errSel("ValidatorNotFound()")
		}
		return c.v2ValidatorAtIndex1(a[0].u64() + 1)
	}).from(hardfork.T2),
	view("validatorByAddress(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		idx1, err := c.v2LoadU64(c.v2AddrIndexSlot(a[0].addr()))
		if err != nil {
			return nil, err
		}
		return c.v2ValidatorAtIndex1(idx1)
	}).from(hardfork.T2),
	view("validatorByPublicKey(bytes32)", []abiType{tB32}, func(c *Context, a []abiValue) ([]byte, error) {
		idx1, err := c.v2LoadU64(c.v2KeyIndexSlot(a[0].bytes32()))
		if err != nil {
			return nil, err
		}
		return c.v2ValidatorAtIndex1(idx1)
	}).from(hardfork.T2),
	view("getNextNetworkIdentityRotationEpoch()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		v, err := c.v2LoadU64(slotV2NextEpoch)
		return encodeWords(wordOfU64(v)), err
	}).from(hardfork.T2),
	view("isInitialized()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		w, err := c.loadField(ValidatorConfigV2Address, slotV2Config, fV2IsInit)
		return encodeWords(flag(!w.IsZero()).hash()), err
	}).from(hardfork.T2),
	mutate("addValidator(address,bytes32,string,string,address,bytes)",
		[]abiType{tAddress, tB32, tString, tString, tAddress, tBytes},
		func(c *Context, a []abiValue) ([]byte, error) {
			idx, err := c.v2AddValidator(addValidatorArgs{
				validator: a[0].addr(), publicKey: a[1].bytes32(), endpoints: v2EndpointArgs(a[2:4]),
				fee: a[4].addr(), signature: a[5].data,
			})
			if err != nil {
				return nil, err
			}
			return encodeWords(wordOfU64(idx)), nil
		}).from(hardfork.T2),
	mutate("deactivateValidator(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.v2DeactivateValidator(a[0].u64())
	}).from(hardfork.T2),
	mutate("rotateValidator(uint64,bytes32,string,string,bytes)",
		[]abiType{tU64, tB32, tString, tString, tBytes},
		func(c *Context, a []abiValue) ([]byte, error) {
			return nil, c.v2RotateValidator(rotateArgs{
				index: a[0].u64(), publicKey: a[1].bytes32(), endpoints: v2EndpointArgs(a[2:4]), signature: a[4].data,
			})
		}).from(hardfork.T2),
	mutate("setFeeRecipient(uint64,address)", []abiType{tU64, tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.v2SetFeeRecipient(feeUpdate{index: a[0].u64(), fee: a[1].addr()})
	}).from(hardfork.T2),
	mutate("setIpAddresses(uint64,string,string)", []abiType{tU64, tString, tString},
		func(c *Context, a []abiValue) ([]byte, error) {
			return nil, c.v2SetIPAddresses(ipUpdate{index: a[0].u64(), endpoints: v2EndpointArgs(a[1:3])})
		}).from(hardfork.T2),
	mutate("transferValidatorOwnership(uint64,address)", []abiType{tU64, tAddress},
		func(c *Context, a []abiValue) ([]byte, error) {
			return nil, c.v2TransferValidatorOwnership(addressMove{index: a[0].u64(), to: a[1].addr()})
		}).from(hardfork.T2),
	mutate("transferOwnership(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.v2TransferOwnership(a[0].addr())
	}).from(hardfork.T2),
	mutate("setNetworkIdentityRotationEpoch(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.v2SetRotationEpoch(a[0].u64())
	}).from(hardfork.T2),
	mutate("migrateValidator(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.v2MigrateValidator(a[0].u64())
	}).from(hardfork.T2),
	mutate("initializeIfMigrated()", nil, func(c *Context, _ []abiValue) ([]byte, error) {
		return nil, c.v2InitializeIfMigrated()
	}).from(hardfork.T2),
))

type V2Validator struct {
	PublicKey           Hash
	ValidatorAddress    Address
	Ingress             []byte
	Egress              []byte
	FeeRecipient        Address
	Index               uint64
	AddedAtHeight       uint64
	DeactivatedAtHeight uint64
}

func (r v2Record) exported() V2Validator {
	return V2Validator{
		PublicKey: r.publicKey, ValidatorAddress: r.validator, Ingress: r.ingress, Egress: r.egress,
		FeeRecipient: r.fee, Index: r.index, AddedAtHeight: r.added, DeactivatedAtHeight: r.deactivated,
	}
}

func (v V2Validator) IsDkgPlayer() bool {
	if _, err := parseSocketAddr(v.Ingress); err != nil {
		return false
	}
	if _, err := parseIPAddr(v.Egress); err != nil {
		return false
	}
	return ed25519PublicKeyDecodes(v.PublicKey)
}

func ActiveValidatorsV2(c *Context) ([]V2Validator, error) {
	recs, err := c.v2ActiveRecords()
	if err != nil {
		return nil, err
	}
	out := make([]V2Validator, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.exported())
	}
	return out, nil
}

func (c *Context) v2InitializedBy(parent uint64) (bool, error) {
	k, err := c.v2ReadConfig()
	if err != nil {
		return false, err
	}
	if !k.isInit() {
		return false, nil
	}
	return fV2InitHeight.u64(k.w) <= parent, nil
}

type ProposerFeeRecipient struct {
	Address Address
	Found   bool
}

type ProposerQuery struct {
	BlockNumber uint64
	PublicKey   Hash
}

func ProposerFeeRecipientV2(c *Context, q ProposerQuery) (ProposerFeeRecipient, error) {
	if !c.at(hardfork.T2) {
		return ProposerFeeRecipient{}, nil
	}
	ok, err := c.v2InitializedBy(q.BlockNumber - min(q.BlockNumber, 1))
	if err != nil {
		return ProposerFeeRecipient{}, err
	}
	if !ok {
		return ProposerFeeRecipient{}, nil
	}
	idx1, err := c.v2LoadU64(c.v2KeyIndexSlot(q.PublicKey))
	if err != nil {
		return ProposerFeeRecipient{}, err
	}
	if idx1 == 0 {
		return ProposerFeeRecipient{}, nil
	}
	r, err := c.v2ReadRecord(idx1 - 1)
	if err != nil {
		return ProposerFeeRecipient{}, err
	}
	return ProposerFeeRecipient{Address: r.fee, Found: true}, nil
}

type DkgCeremonyConfig struct {
	ActiveValidators []V2Validator
	NextFullDkgEpoch uint64
	Found            bool
}

func DkgCeremonyConfigV2(c *Context, blockNumber uint64) (DkgCeremonyConfig, error) {
	ok, err := c.v2InitializedBy(blockNumber - min(blockNumber, 1))
	if err != nil {
		return DkgCeremonyConfig{}, err
	}
	if !ok {
		return DkgCeremonyConfig{}, nil
	}
	vals, err := ActiveValidatorsV2(c)
	if err != nil {
		return DkgCeremonyConfig{}, err
	}
	next, err := c.v2LoadU64(slotV2NextEpoch)
	if err != nil {
		return DkgCeremonyConfig{}, err
	}
	return DkgCeremonyConfig{ActiveValidators: vals, NextFullDkgEpoch: next, Found: true}, nil
}
