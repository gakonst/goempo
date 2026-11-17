package precompiles

import (
	"github.com/gakonst/goempo/internal/u256"
)

func (c *Context) v1NextDKG() (uint64, error) {
	w, err := c.sload(ValidatorConfigAddress, slotVCNextDKG)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

func (c *Context) v1ValidatorAt(index uint64) (Address, error) {
	n, err := c.validatorCount()
	if err != nil {
		return Address{}, err
	}
	if index >= n {
		return Address{}, v2Panic(panicOutOfBounds)
	}
	return c.loadAddress(ValidatorConfigAddress, c.validatorArraySlot(index))
}

func (c *Context) v2RequireMigrationOwner() (v2Config, error) {
	k, err := c.v2ReadConfig()
	if err != nil {
		return k, err
	}
	if k.isInit() {
		return k, errSel("AlreadyInitialized()")
	}
	if k.owner() == (Address{}) {
		owner, err := c.loadAddress(ValidatorConfigAddress, slotVCOwner)
		if err != nil {
			return k, err
		}
		n, err := c.validatorCount()
		if err != nil {
			return k, err
		}
		if n == 0 {
			return k, errSel("EmptyV1ValidatorSet()")
		}
		ow := toWord(addressWord(owner))
		cnt := u256.FromUint64(n & 0xff)
		k.w = pack(k.w).with(fV2Owner, ow).with(fV2V1Count, cnt).w
		if err := c.v2WriteConfig(pack(Word{}).with(fV2Owner, ow).with(fV2V1Count, cnt).w); err != nil {
			return k, err
		}
	}
	return k, v2RequireOwner(k, c.Caller)
}

type v1Entry struct {
	slotIndex uint64
	record    validatorRecord
}

func (c *Context) v2SkipMigration(e v1Entry, k v2Config) error {
	topics := []Hash{evV2SkippedMigration, v2Indexed(e.slotIndex), addressWord(e.record.address)}
	if err := c.emit(ValidatorConfigV2Address, topics, encodeWords(e.record.publicKey)); err != nil {
		return err
	}
	return c.storeField(ValidatorConfigV2Address, slotV2Config, fV2Skipped, u256.FromUint64(min(k.skipped()+1, v2SkippedMax)))
}

func (c *Context) v2MigrateValidator(idx uint64) error {
	k, err := c.v2RequireMigrationOwner()
	if err != nil {
		return err
	}
	height := c.v2Height()
	migrated, err := c.v2Count()
	if err != nil {
		return err
	}
	processed := migrated + k.skipped()
	if processed >= k.v1Count() {
		return errSel("InvalidMigrationIndex()")
	}
	if idx != k.v1Count()-processed-1 {
		return errSel("InvalidMigrationIndex()")
	}
	who, err := c.v1ValidatorAt(idx)
	if err != nil {
		return err
	}
	old, err := c.readValidator(who)
	if err != nil {
		return err
	}
	e := v1Entry{slotIndex: idx, record: old}
	if !ed25519PublicKeyDecodes(old.publicKey) {
		return c.v2SkipMigration(e, k)
	}
	sock, err := parseSocketAddr(old.outbound)
	if err != nil {
		return c.v2SkipMigration(e, k)
	}
	egress := formatIP(sock.ip)
	keyW, err := c.v2Load(c.v2KeyIndexSlot(old.publicKey))
	if err != nil {
		return err
	}
	if !keyW.IsZero() {
		return c.v2SkipMigration(e, k)
	}
	if err := c.v2RequireAddressFree(old.address); err != nil {
		return err
	}
	ingressKey, err := c.v2IngressKey(old.inbound)
	if err != nil {
		return err
	}
	if old.active {
		w, err := c.v2Load(c.v2IngressSlot(ingressKey))
		if err != nil {
			return err
		}
		if !w.IsZero() {
			return c.v2SkipMigration(e, k)
		}
	}
	deactivated := height
	if old.active {
		deactivated = 0
	}
	index, err := c.v2AppendValidator(v2Append{
		validator: old.address, publicKey: old.publicKey, added: height, deactivated: deactivated,
		endpoints: endpoints{ingress: old.inbound, egress: egress},
	})
	if err != nil {
		return err
	}
	if old.active {
		if err := c.v2Store(c.v2IngressSlot(ingressKey), u256.FromUint64(1)); err != nil {
			return err
		}
	}
	topics := []Hash{evV2ValidatorMigrated, v2Indexed(index), addressWord(old.address)}
	return c.emit(ValidatorConfigV2Address, topics, encodeWords(old.publicKey))
}

func (c *Context) v2RequireAddressFree(a Address) error {
	idx1, err := c.v2LoadU64(c.v2AddrIndexSlot(a))
	if err != nil {
		return err
	}
	if idx1 == 0 {
		return nil
	}
	d, err := c.v2DeactivatedAt(idx1 - 1)
	if err != nil {
		return err
	}
	if d == 0 {
		return errSel("AddressAlreadyHasValidator()")
	}
	return nil
}

func (c *Context) v2MigrationComplete(k v2Config) (bool, error) {
	if k.v1Count() == 0 {
		return false, nil
	}
	n, err := c.v2Count()
	return n+k.skipped() >= k.v1Count(), err
}

func (c *Context) v2InitializeIfMigrated() error {
	k, err := c.v2RequireMigrationOwner()
	if err != nil {
		return err
	}
	done, err := c.v2MigrationComplete(k)
	if err != nil {
		return err
	}
	if !done {
		return errSel("MigrationNotComplete()")
	}
	next, err := c.v1NextDKG()
	if err != nil {
		return err
	}
	if err := c.v2Store(slotV2NextEpoch, u256.FromUint64(next)); err != nil {
		return err
	}
	height := c.v2Height()
	w := pack(k.w).withU64(fV2InitHeight, height).withU64(fV2IsInit, 1).w
	if err := c.v2WriteConfig(w); err != nil {
		return err
	}
	return c.emit(ValidatorConfigV2Address, []Hash{evV2Initialized}, encodeWords(wordOfU64(height)))
}
