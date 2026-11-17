package precompiles

import (
	"net/netip"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotVCOwner      = slotOf(0)
	slotVCArray      = slotOf(1)
	slotVCValidators = slotOf(2)
	slotVCNextDKG    = slotOf(3)
	fVCActive        = field{offset: 0, size: 1}
	fVCIndex         = field{offset: 1, size: 8}
	fVCAddress       = field{offset: 9, size: 20}
)

const maxValidators = 1 << 16

type validatorRecord struct {
	publicKey Hash
	active    bool
	index     uint64
	address   Address
	inbound   []byte
	outbound  []byte
}

func (c *Context) validatorSlot(v Address) slot { return c.mapAddr(v, slotVCValidators) }

func (c *Context) readValidator(v Address) (validatorRecord, error) {
	base := c.validatorSlot(v)
	pk, err := c.sload(ValidatorConfigAddress, base)
	if err != nil {
		return validatorRecord{}, err
	}
	packedW, err := c.sload(ValidatorConfigAddress, base.plus(1))
	if err != nil {
		return validatorRecord{}, err
	}
	in, err := c.loadString(ValidatorConfigAddress, base.plus(2))
	if err != nil {
		return validatorRecord{}, err
	}
	out, err := c.loadString(ValidatorConfigAddress, base.plus(3))
	return validatorRecord{
		publicKey: toHash(pk), active: !fVCActive.of(packedW).IsZero(), index: fVCIndex.u64(packedW),
		address: wordAddress(toHash(fVCAddress.of(packedW))), inbound: in, outbound: out,
	}, err
}

func (c *Context) storePackedGroup(addr Address, s slot, w Word) error {
	if !c.at(hardfork.T4) {
		if _, err := c.sload(addr, s); err != nil {
			return err
		}
	}
	return c.sstore(addr, s, w)
}

func (c *Context) writeValidator(v Address, r validatorRecord) error {
	base := c.validatorSlot(v)
	if err := c.sstore(ValidatorConfigAddress, base, toWord(r.publicKey)); err != nil {
		return err
	}
	w := pack(Word{}).with(fVCActive, flag(r.active).word()).withU64(fVCIndex, r.index).with(fVCAddress, toWord(addressWord(r.address))).w
	if err := c.storePackedGroup(ValidatorConfigAddress, base.plus(1), w); err != nil {
		return err
	}
	if err := c.storeString(ValidatorConfigAddress, base.plus(2), r.inbound); err != nil {
		return err
	}
	return c.storeString(ValidatorConfigAddress, base.plus(3), r.outbound)
}

func (c *Context) deleteString(addr Address, s slot) error {
	head, err := c.sload(addr, s)
	if err != nil {
		return err
	}
	if head[0]&1 == 1 {
		words, err := longStringWords(head)
		if err != nil {
			return err
		}
		if err := c.clearStringTail(addr, s, wordSpan{from: 0, to: words}); err != nil {
			return err
		}
	}
	return c.sstore(addr, s, Word{})
}

func (c *Context) deleteValidator(v Address) error {
	base := c.validatorSlot(v)
	if err := c.deleteString(ValidatorConfigAddress, base.plus(2)); err != nil {
		return err
	}
	if err := c.deleteString(ValidatorConfigAddress, base.plus(3)); err != nil {
		return err
	}
	if err := c.sstore(ValidatorConfigAddress, base, Word{}); err != nil {
		return err
	}
	return c.sstore(ValidatorConfigAddress, base.plus(1), Word{})
}

func (c *Context) validatorExists(v Address) (bool, error) {
	r, err := c.readValidator(v)
	return r.publicKey != (Hash{}), err
}

func (c *Context) checkVCOwner(sender Address) error {
	owner, err := c.loadAddress(ValidatorConfigAddress, slotVCOwner)
	if err != nil {
		return err
	}
	if owner != sender {
		return errSel("Unauthorized()")
	}
	return nil
}

func (c *Context) validatorCount() (uint64, error) {
	w, err := c.sload(ValidatorConfigAddress, slotVCArray)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() || w.Uint64() > maxValidators {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

func (c *Context) validatorArraySlot(i uint64) slot {
	return slot(c.keccak(slotVCArray[:])).plus(i)
}

type endpointField struct {
	name  string
	value []byte
	sig   string
}

func (c *Context) checkEndpoint(e endpointField) error {
	if _, err := netip.ParseAddrPort(string(e.value)); err == nil {
		return nil
	}
	detail := "input was not of the form `<ip>:<port>`"
	if !c.at(hardfork.T2) {
		detail = "Parse(AddrParseError(Socket))"
	}
	data := newEncoder(3).bytes([]byte(e.name)).bytes(e.value).bytes([]byte(detail)).out()
	sel := selector(e.sig)
	return revert(append(sel[:], data...))
}

func (c *Context) checkEndpoints(r validatorRecord) error {
	if err := c.checkEndpoint(endpointField{name: "inboundAddress", value: r.inbound, sig: "NotHostPort(string,string,string)"}); err != nil {
		return err
	}
	return c.checkEndpoint(endpointField{name: "outboundAddress", value: r.outbound, sig: "NotIpPort(string,string,string)"})
}

func (c *Context) addValidator(sender Address, r validatorRecord) error {
	if r.publicKey == (Hash{}) {
		return errSel("InvalidPublicKey()")
	}
	if err := c.checkVCOwner(sender); err != nil {
		return err
	}
	exists, err := c.validatorExists(r.address)
	if err != nil {
		return err
	}
	if exists {
		return errSel("ValidatorAlreadyExists()")
	}
	if err := c.checkEndpoints(r); err != nil {
		return err
	}
	n, err := c.validatorCount()
	if err != nil {
		return err
	}
	r.index = n
	if err := c.writeValidator(r.address, r); err != nil {
		return err
	}
	return c.pushValidator(r.address)
}

func (c *Context) pushValidator(v Address) error {
	n, err := c.validatorCount()
	if err != nil {
		return err
	}
	if err := c.sstore(ValidatorConfigAddress, c.validatorArraySlot(n), toWord(addressWord(v))); err != nil {
		return err
	}
	return c.sstore(ValidatorConfigAddress, slotVCArray, u256.FromUint64(n+1))
}

func (c *Context) updateValidator(sender Address, r validatorRecord) error {
	if r.publicKey == (Hash{}) {
		return errSel("InvalidPublicKey()")
	}
	exists, err := c.validatorExists(sender)
	if err != nil {
		return err
	}
	if !exists {
		return errSel("ValidatorNotFound()")
	}
	old, err := c.readValidator(sender)
	if err != nil {
		return err
	}
	if r.address != sender {
		taken, err := c.validatorExists(r.address)
		if err != nil {
			return err
		}
		if taken {
			return errSel("ValidatorAlreadyExists()")
		}
		if err := c.writeArrayElem(old.index, r.address); err != nil {
			return err
		}
		if err := c.deleteValidator(sender); err != nil {
			return err
		}
	}
	if err := c.checkEndpoints(r); err != nil {
		return err
	}
	r.active, r.index = old.active, old.index
	return c.writeValidator(r.address, r)
}

func (c *Context) writeArrayElem(i uint64, v Address) error {
	return c.sstore(ValidatorConfigAddress, c.validatorArraySlot(i), toWord(addressWord(v)))
}

type statusChange struct {
	sender    Address
	validator Address
	active    bool
}

func (c *Context) changeValidatorStatus(s statusChange) error {
	if err := c.checkVCOwner(s.sender); err != nil {
		return err
	}
	exists, err := c.validatorExists(s.validator)
	if err != nil {
		return err
	}
	if !exists {
		return errSel("ValidatorNotFound()")
	}
	r, err := c.readValidator(s.validator)
	if err != nil {
		return err
	}
	r.active = s.active
	return c.writeValidator(s.validator, r)
}

func (c *Context) changeValidatorStatusByIndex(sender Address, index uint64, active flag) error {
	if err := c.checkVCOwner(sender); err != nil {
		return err
	}
	n, err := c.validatorCount()
	if err != nil {
		return err
	}
	if index >= n {
		return errSel("ValidatorNotFound()")
	}
	v, err := c.loadAddress(ValidatorConfigAddress, c.validatorArraySlot(index))
	if err != nil {
		return err
	}
	r, err := c.readValidator(v)
	if err != nil {
		return err
	}
	r.active = bool(active)
	return c.writeValidator(v, r)
}

func validatorTuple(r validatorRecord) []byte {
	return newEncoder(6).word(r.publicKey).word(flag(r.active).hash()).u64(r.index).addr(r.address).bytes(r.inbound).bytes(r.outbound).out()
}

func wrapDynamic(body []byte) []byte {
	return append(encodeWords(wordOfU64(32)), body...)
}

func (c *Context) getValidators() ([]byte, error) {
	n, err := c.validatorCount()
	if err != nil {
		return nil, err
	}
	n = min(n, maxValidators)
	heads := make([]byte, 0, 32*n)
	tails := make([]byte, 0)
	for i := uint64(0); i < n; i++ {
		v, err := c.loadAddress(ValidatorConfigAddress, c.validatorArraySlot(i))
		if err != nil {
			return nil, err
		}
		r, err := c.readValidator(v)
		if err != nil {
			return nil, err
		}
		r.address = v
		off := wordOfU64(32*n + uint64(len(tails)))
		heads = append(heads, off[:]...)
		tails = append(tails, validatorTuple(r)...)
	}
	body := append(encodeWords(wordOfU64(n)), heads...)
	return wrapDynamic(append(body, tails...)), nil
}

func vcRecordArgs(a []abiValue, withActive flag) validatorRecord {
	if withActive {
		return validatorRecord{address: a[0].addr(), publicKey: a[1].bytes32(), active: a[2].boolean(), inbound: a[3].data, outbound: a[4].data}
	}
	return validatorRecord{address: a[0].addr(), publicKey: a[1].bytes32(), inbound: a[2].data, outbound: a[3].data}
}

var validatorConfigTable = register(ValidatorConfigAddress, newTable(
	view("owner()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		o, err := c.loadAddress(ValidatorConfigAddress, slotVCOwner)
		return retAddr(o), err
	}).before(hardfork.T15),
	view("getValidators()", nil, func(c *Context, a []abiValue) ([]byte, error) { return c.getValidators() }).before(hardfork.T15),
	view("getNextFullDkgCeremony()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		w, err := c.loadField(ValidatorConfigAddress, slotVCNextDKG, fU64At0)
		return retWord(w), err
	}).before(hardfork.T15),
	view("validatorsArray(uint256)", []abiType{tU256}, func(c *Context, a []abiValue) ([]byte, error) {
		n, err := c.validatorCount()
		if err != nil {
			return nil, err
		}
		i := a[0].u256()
		if !i.IsUint64() || i.Uint64() >= n {
			return nil, revert(abiError(selPanic, wordOfU64(0x32)))
		}
		v, err := c.loadAddress(ValidatorConfigAddress, c.validatorArraySlot(i.Uint64()))
		return retAddr(v), err
	}).before(hardfork.T15),
	view("validators(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		r, err := c.readValidator(a[0].addr())
		return wrapDynamic(validatorTuple(r)), err
	}).before(hardfork.T15),
	view("validatorCount()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		n, err := c.validatorCount()
		return retU64(n), err
	}).before(hardfork.T15),
	mutate("addValidator(address,bytes32,bool,string,string)", []abiType{tAddress, tB32, tBool, tString, tString}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.addValidator(c.Caller, vcRecordArgs(a, true))
	}).before(hardfork.T15),
	mutate("updateValidator(address,bytes32,string,string)", []abiType{tAddress, tB32, tString, tString}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.updateValidator(c.Caller, vcRecordArgs(a, false))
	}).before(hardfork.T15),
	mutate("changeValidatorStatus(address,bool)", []abiType{tAddress, tBool}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.changeValidatorStatus(statusChange{sender: c.Caller, validator: a[0].addr(), active: a[1].boolean()})
	}).before(hardfork.T15),
	mutate("changeValidatorStatusByIndex(uint64,bool)", []abiType{tU64, tBool}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.changeValidatorStatusByIndex(c.Caller, a[0].u64(), flag(a[1].boolean()))
	}).from(hardfork.T1).before(hardfork.T15),
	mutate("changeOwner(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		if err := c.checkVCOwner(c.Caller); err != nil {
			return nil, err
		}
		return nil, c.storeAddress(ValidatorConfigAddress, slotVCOwner, a[0].addr())
	}).before(hardfork.T15),
	mutate("setNextFullDkgCeremony(uint64)", []abiType{tU64}, func(c *Context, a []abiValue) ([]byte, error) {
		if err := c.checkVCOwner(c.Caller); err != nil {
			return nil, err
		}
		return nil, c.sstore(ValidatorConfigAddress, slotVCNextDKG, u256.FromUint64(a[0].u64()))
	}).before(hardfork.T15),
))
