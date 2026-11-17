package precompiles

import (
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

type addValidatorArgs struct {
	validator Address
	publicKey Hash
	fee       Address
	signature []byte
	endpoints
}

func (c *Context) v2AddValidator(a addValidatorArgs) (uint64, error) {
	k, err := c.v2ReadConfig()
	if err != nil {
		return 0, err
	}
	if err := v2RequireInit(k); err != nil {
		return 0, err
	}
	if err := v2RequireOwner(k, c.Caller); err != nil {
		return 0, err
	}
	if err := c.v2RequireNewKey(a.publicKey); err != nil {
		return 0, err
	}
	if err := c.v2RequireNewAddress(a.validator); err != nil {
		return 0, err
	}
	if err := c.v2ValidateEndpoints(a.endpoints); err != nil {
		return 0, err
	}
	key, err := c.v2RequireUniqueIngress(a.ingress)
	if err != nil {
		return 0, err
	}
	err = c.v2VerifySignature(signedEndpoints{
		publicKey: a.publicKey, signature: a.signature, validator: a.validator, fee: a.fee, endpoints: a.endpoints,
	})
	if err != nil {
		return 0, err
	}
	if err := c.v2Store(c.v2IngressSlot(key), u256.FromUint64(1)); err != nil {
		return 0, err
	}
	index, err := c.v2AppendValidator(v2Append{
		validator: a.validator, publicKey: a.publicKey, fee: a.fee, added: c.v2Height(), endpoints: a.endpoints,
	})
	if err != nil {
		return 0, err
	}
	data := newEncoder(4).word(a.publicKey).bytes(a.ingress).bytes(a.egress).addr(a.fee).out()
	topics := []Hash{evV2ValidatorAdded, v2Indexed(index), addressWord(a.validator)}
	return index, c.emit(ValidatorConfigV2Address, topics, data)
}

func (c *Context) v2DeactivateValidator(index uint64) error {
	v, err := c.v2ActiveValidator(index)
	if err != nil {
		return err
	}
	k, err := c.v2ReadConfig()
	if err != nil {
		return err
	}
	if err := v2RequireOwnerOrValidator(k, authority{caller: c.Caller, validator: v.validator}); err != nil {
		return err
	}
	key, err := c.v2IngressKey(v.ingress)
	if err != nil {
		return err
	}
	if err := c.v2Store(c.v2IngressSlot(key), Word{}); err != nil {
		return err
	}
	a := ValidatorConfigV2Address
	if err := c.storeField(a, c.v2ActiveIdxSlot(index), fV2Deactivated, u256.FromUint64(c.v2Height())); err != nil {
		return err
	}
	if err := c.v2SwapOutActive(v.activeIdx); err != nil {
		return err
	}
	if err := c.v2PopActive(); err != nil {
		return err
	}
	if err := c.storeField(a, c.v2ActiveIdxSlot(index), fV2ActiveIdx, Word{}); err != nil {
		return err
	}
	return c.emit(a, []Hash{evV2ValidatorDeactivated, v2Indexed(index), addressWord(v.validator)}, nil)
}

func (c *Context) v2SwapOutActive(activeIdx1 uint64) error {
	if activeIdx1 == 0 {
		return underOverflow()
	}
	pos := activeIdx1 - 1
	n, err := c.v2ActiveLen()
	if err != nil {
		return err
	}
	if n == 0 {
		return underOverflow()
	}
	last := n - 1
	if pos == last {
		return nil
	}
	moved, err := c.v2ActiveAt(last)
	if err != nil {
		return err
	}
	if moved == 0 {
		return underOverflow()
	}
	if err := c.v2SetActiveAt(activeWrite{pos: pos, index: moved}); err != nil {
		return err
	}
	return c.storeField(ValidatorConfigV2Address, c.v2ActiveIdxSlot(moved-1), fV2ActiveIdx, u256.FromUint64(pos+1))
}

type rotateArgs struct {
	index     uint64
	publicKey Hash
	signature []byte
	endpoints
}

func (c *Context) v2RotateValidator(a rotateArgs) error {
	v, err := c.v2ActiveValidator(a.index)
	if err != nil {
		return err
	}
	k, err := c.v2ReadConfig()
	if err != nil {
		return err
	}
	if err := v2RequireInit(k); err != nil {
		return err
	}
	if err := v2RequireOwnerOrValidator(k, authority{caller: c.Caller, validator: v.validator}); err != nil {
		return err
	}
	if err := c.v2RequireNewKey(a.publicKey); err != nil {
		return err
	}
	if err := c.v2ValidateEndpoints(a.endpoints); err != nil {
		return err
	}
	if _, err := c.v2RequireUniqueIngress(a.ingress); err != nil {
		return err
	}
	err = c.v2VerifySignature(signedEndpoints{
		publicKey: a.publicKey, signature: a.signature, validator: v.validator, rotate: true, endpoints: a.endpoints,
	})
	if err != nil {
		return err
	}
	height := c.v2Height()
	if err := c.v2UpdateIngress(ingressMove{old: v.ingress, new: a.ingress}); err != nil {
		return err
	}
	appended, err := c.v2Count()
	if err != nil {
		return err
	}
	snapshot := v
	snapshot.index = appended
	snapshot.activeIdx = 0
	snapshot.deactivated = height
	if err := c.v2PushRecord(snapshot); err != nil {
		return err
	}
	if err := c.v2Store(c.v2KeyIndexSlot(v.publicKey), u256.FromUint64(appended+1)); err != nil {
		return err
	}
	updated, err := c.v2ReadRecord(a.index)
	if err != nil {
		return err
	}
	updated.publicKey = a.publicKey
	updated.ingress = a.ingress
	updated.egress = a.egress
	updated.added = height
	if err := c.v2WriteRecord(a.index, updated, false); err != nil {
		return err
	}
	if err := c.v2Store(c.v2KeyIndexSlot(a.publicKey), u256.FromUint64(a.index+1)); err != nil {
		return err
	}
	data := newEncoder(5).word(v.publicKey).word(a.publicKey).bytes(a.ingress).bytes(a.egress).addr(c.Caller).out()
	topics := []Hash{evV2ValidatorRotated, v2Indexed(a.index), v2Indexed(appended), addressWord(v.validator)}
	return c.emit(ValidatorConfigV2Address, topics, data)
}

func (c *Context) v2ActiveForUpdate(index uint64) (v2Record, error) {
	v, err := c.v2ActiveValidator(index)
	if err != nil {
		return v2Record{}, err
	}
	k, err := c.v2ReadConfig()
	if err != nil {
		return v2Record{}, err
	}
	if err := v2RequireInit(k); err != nil {
		return v2Record{}, err
	}
	return v, v2RequireOwnerOrValidator(k, authority{caller: c.Caller, validator: v.validator})
}

type feeUpdate struct {
	index uint64
	fee   Address
}

func (c *Context) v2SetFeeRecipient(u feeUpdate) error {
	v, err := c.v2ActiveForUpdate(u.index)
	if err != nil {
		return err
	}
	v.fee = u.fee
	if err := c.v2WriteRecord(u.index, v, false); err != nil {
		return err
	}
	data := newEncoder(2).addr(u.fee).addr(c.Caller).out()
	return c.emit(ValidatorConfigV2Address, []Hash{evV2FeeRecipientUpdated, v2Indexed(u.index)}, data)
}

type ipUpdate struct {
	index uint64
	endpoints
}

func (c *Context) v2SetIPAddresses(u ipUpdate) error {
	v, err := c.v2ActiveForUpdate(u.index)
	if err != nil {
		return err
	}
	if err := c.v2ValidateEndpoints(u.endpoints); err != nil {
		return err
	}
	if err := c.v2UpdateIngress(ingressMove{old: v.ingress, new: u.ingress}); err != nil {
		return err
	}
	v.ingress = u.ingress
	v.egress = u.egress
	if err := c.v2WriteRecord(u.index, v, false); err != nil {
		return err
	}
	data := newEncoder(3).bytes(u.ingress).bytes(u.egress).addr(c.Caller).out()
	return c.emit(ValidatorConfigV2Address, []Hash{evV2IPAddressesUpdated, v2Indexed(u.index)}, data)
}

type addressMove struct {
	index uint64
	to    Address
}

func (c *Context) v2TransferValidatorOwnership(m addressMove) error {
	v, err := c.v2ActiveForUpdate(m.index)
	if err != nil {
		return err
	}
	if err := c.v2RequireNewAddress(m.to); err != nil {
		return err
	}
	old := v.validator
	v.validator = m.to
	if err := c.v2WriteRecord(m.index, v, false); err != nil {
		return err
	}
	if err := c.v2Store(c.v2AddrIndexSlot(old), Word{}); err != nil {
		return err
	}
	if err := c.v2Store(c.v2AddrIndexSlot(m.to), u256.FromUint64(m.index+1)); err != nil {
		return err
	}
	topics := []Hash{evV2OwnershipMoved, v2Indexed(m.index), addressWord(old), addressWord(m.to)}
	return c.emit(ValidatorConfigV2Address, topics, newEncoder(1).addr(c.Caller).out())
}

func (c *Context) v2TransferOwnership(to Address) error {
	if to == (Address{}) {
		return errSel("InvalidOwner()")
	}
	k, err := c.v2ReadConfig()
	if err != nil {
		return err
	}
	if err := v2RequireInit(k); err != nil {
		return err
	}
	if err := v2RequireOwner(k, c.Caller); err != nil {
		return err
	}
	old := k.owner()
	if err := c.v2WriteConfig(pack(k.w).with(fV2Owner, toWord(addressWord(to))).w); err != nil {
		return err
	}
	topics := []Hash{evV2OwnershipTransferred, addressWord(old), addressWord(to)}
	return c.emit(ValidatorConfigV2Address, topics, nil)
}

func (c *Context) v2SetRotationEpoch(epoch uint64) error {
	k, err := c.v2ReadConfig()
	if err != nil {
		return err
	}
	if err := v2RequireInit(k); err != nil {
		return err
	}
	if err := v2RequireOwner(k, c.Caller); err != nil {
		return err
	}
	prev, err := c.v2LoadU64(slotV2NextEpoch)
	if err != nil {
		return err
	}
	if err := c.v2Store(slotV2NextEpoch, u256.FromUint64(epoch)); err != nil {
		return err
	}
	topics := []Hash{evV2RotationEpochSet, v2Indexed(prev), v2Indexed(epoch)}
	return c.emit(ValidatorConfigV2Address, topics, nil)
}

func dormantPrecompile(c *Context, addr Address) bool {
	return addr == ValidatorConfigV2Address && !c.at(hardfork.T2)
}
