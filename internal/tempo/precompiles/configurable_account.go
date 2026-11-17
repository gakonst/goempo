package precompiles

import (
	"bytes"
	"encoding/binary"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	caMaxOwners         = 48
	caMaxSignatures     = 8
	caCommitmentGas     = 5_000
	caNewAccountGas     = 250_000
	caBasePrecompileMax = 0x11
	caP256Verify        = 0x100
)

const _ = uint(caNewAccountGas - caCommitmentGas)
const _ = uint(caP256Verify - caBasePrecompileMax)
const _ = uint(caMaxOwners - caMaxSignatures)
const _ = uint(maxNestedItems - caMaxOwners)

var (
	caAccountDomain         = []byte("tempo:configurable-account:account")
	caUpgradeDomain         = []byte("tempo:configurable-account:upgrade")
	caConfigDomain          = []byte("tempo:configurable-account:config")
	ConfigurableAccountCode = []byte{0xef}

	slotCATxOrigin  = slotOf(0)
	slotCAAuthority = slotOf(1)
	fCADirect       = field{offset: 0, size: 20}
	fCAEligibility  = field{offset: 20, size: 1}

	evCAConfigUpdated = topic("ConfigurableAccountConfigUpdated(address,bytes32,uint64,uint8,(address,uint8)[])")
	evCAUpgraded      = topic("AccountUpgraded(address,bytes32,bytes32,uint8,(address,uint8)[])")
)

type UpgradeEligibility uint8

const (
	UpgradeHasAuthorizationList UpgradeEligibility = iota
	UpgradeNotPrimitiveRoot
	UpgradePrimitiveRoot
)

type ConfigCommitmentState interface {
	ConfigCommitment(addr Address) Hash
	SetConfigCommitment(addr Address, commitment Hash)
}

type caOwner struct {
	owner  Address
	weight uint8
}

type caConfig struct {
	salt      Hash
	version   uint64
	threshold uint8
	owners    []caOwner
}

type ConfigurableAuthority struct {
	Origin      Address
	Direct      Address
	Eligibility UpgradeEligibility
}

func SetConfigurableAuthority(c *Context, a ConfigurableAuthority) {
	c.State.SetTransientStorage(ConfigurableAccountAddress, Hash(slotCATxOrigin), toWord(addressWord(a.Origin)))
	authority := pack(Word{}).with(fCADirect, toWord(addressWord(a.Direct))).with(fCAEligibility, u256.FromUint64(uint64(a.Eligibility)))
	c.State.SetTransientStorage(ConfigurableAccountAddress, Hash(slotCAAuthority), authority.w)
}

func caErrInvalidOwner() error { return errSel("InvalidConfigurableAccountOwner()") }

func (cfg caConfig) validate(account Address, bound flag) error {
	if len(cfg.owners) == 0 {
		return caErrInvalidOwner()
	}
	if len(cfg.owners) > caMaxOwners {
		return errSel("TooManyOwners()")
	}
	if cfg.threshold == 0 {
		return errSel("InvalidThreshold()")
	}
	for _, o := range cfg.owners {
		if o.owner == (Address{}) {
			return caErrInvalidOwner()
		}
		if cfg.version == 0 && bool(bound) && o.owner == account {
			return caErrInvalidOwner()
		}
	}
	for _, o := range cfg.owners {
		if o.weight == 0 {
			return errSel("InvalidWeight()")
		}
	}
	if err := cfg.validateOrder(); err != nil {
		return err
	}
	return cfg.validateWeights()
}

func (cfg caConfig) validateOrder() error {
	for i := 1; i < len(cfg.owners); i++ {
		if bytes.Compare(cfg.owners[i-1].owner[:], cfg.owners[i].owner[:]) < 0 {
			continue
		}
		for j, o := range cfg.owners {
			for _, prev := range cfg.owners[:j] {
				if prev.owner == o.owner {
					return errSel("DuplicateOwner()")
				}
			}
		}
		return errSel("InvalidOwnerOrder()")
	}
	return nil
}

func (cfg caConfig) validateWeights() error {
	total := 0
	var top [caMaxSignatures]uint8
	for _, o := range cfg.owners {
		total += int(o.weight)
		caInsertTop(&top, o.weight)
	}
	if total > 0xff {
		return errSel("InvalidWeight()")
	}
	reachable := 0
	for _, w := range top {
		reachable += int(w)
	}
	if int(cfg.threshold) > reachable {
		return errSel("InvalidThreshold()")
	}
	return nil
}

func caInsertTop(top *[caMaxSignatures]uint8, w uint8) {
	if w <= top[0] {
		return
	}
	top[0] = w
	for i := 1; i < len(top); i++ {
		if top[i-1] <= top[i] {
			return
		}
		top[i-1], top[i] = top[i], top[i-1]
	}
}

func (cfg caConfig) ownerSet() []byte {
	out := []byte{cfg.threshold, byte(len(cfg.owners))}
	for _, o := range cfg.owners {
		out = append(out, o.owner[:]...)
		out = append(out, o.weight)
	}
	return out
}

func (cfg caConfig) accountPreimage() []byte {
	out := append(append([]byte{}, caAccountDomain...), cfg.salt[:]...)
	return append(out, cfg.ownerSet()...)
}

func (cfg caConfig) commitmentPreimage() []byte {
	out := append(append([]byte{}, caConfigDomain...), cfg.salt[:]...)
	out = binary.BigEndian.AppendUint64(out, cfg.version)
	return append(out, cfg.ownerSet()...)
}

func (cfg caConfig) ownerWeight(a Address) (uint8, bool) {
	for _, o := range cfg.owners {
		if o.owner == a {
			return o.weight, true
		}
	}
	return 0, false
}

func ValidConfigurableAccount(a Address, fork hardfork.Fork) bool {
	if a == (Address{}) || IsVirtual(a) || IsPrecompile(a, fork) {
		return false
	}
	if [12]byte(a[:12]) == zonePortalPrefix {
		return false
	}
	var high [18]byte
	if [18]byte(a[:18]) != high {
		return true
	}
	n := int(a[18])<<8 | int(a[19])
	return n == 0 || (n > caBasePrecompileMax && n != caP256Verify)
}

func (c *Context) commitmentOf(addr Address) Hash {
	if s, ok := c.State.(ConfigCommitmentState); ok {
		return s.ConfigCommitment(addr)
	}
	return Hash{}
}

func (c *Context) loadAccountGas(addr Address) error {
	if c.at(hardfork.T4) {
		if err := c.charge(gasWarmRead); err != nil {
			return err
		}
		if !c.State.AddressWarm(addr) && c.GasLeft() < c.cost(gasColdAccountExtra) {
			return c.outOfGas()
		}
	}
	cold := !c.State.AddressWarm(addr)
	c.State.WarmAddress(addr)
	if !c.at(hardfork.T4) {
		if err := c.charge(gasWarmRead); err != nil {
			return err
		}
	}
	if cold {
		return c.charge(c.cost(gasColdAccountExtra))
	}
	return nil
}

func (c *Context) configCommitment(addr Address) (Hash, error) {
	if err := c.loadAccountGas(addr); err != nil {
		return Hash{}, err
	}
	return c.commitmentOf(addr), nil
}

func (c *Context) setConfigCommitment(addr Address, commitment Hash) error {
	s, ok := c.State.(ConfigCommitmentState)
	if !ok || !c.at(hardfork.T14) || commitment == (Hash{}) {
		return errFatal
	}
	if err := c.ensureMutable(); err != nil {
		return err
	}
	if err := c.charge(caCommitmentGas); err != nil {
		return err
	}
	previous := s.ConfigCommitment(addr)
	creates := c.State.Empty(addr)
	if previous == (Hash{}) {
		if err := c.charge(caNewAccountGas - caCommitmentGas); err != nil {
			return err
		}
	}
	if creates {
		if err := c.charge(caNewAccountGas); err != nil {
			return err
		}
	}
	if previous == (Hash{}) {
		c.State.SetCode(addr, ConfigurableAccountCode)
	}
	s.SetConfigCommitment(addr, commitment)
	return nil
}

var (
	emptyCodeHash        = Hash(keccak.Sum256(nil))
	configurableCodeHash = Hash(keccak.Sum256(ConfigurableAccountCode))
)

func (c *Context) caDerive(cfg caConfig) ([]byte, error) {
	if err := cfg.validate(Address{}, false); err != nil {
		return nil, err
	}
	if err := c.chargeKeccak(len(cfg.accountPreimage())); err != nil {
		return nil, err
	}
	h := c.keccak(cfg.accountPreimage())
	account := wordAddress(h)
	if account == (Address{}) {
		return nil, errSel("InvalidAccount()")
	}
	if _, owner := cfg.ownerWeight(account); owner {
		return nil, caErrInvalidOwner()
	}
	if !ValidConfigurableAccount(account, c.Fork) {
		return nil, errSel("InvalidAccount()")
	}
	return retAddr(account), nil
}

func (c *Context) caHashConfig(cfg caConfig) (Hash, error) {
	if len(cfg.owners) > caMaxOwners {
		return Hash{}, errSel("InvalidConfig()")
	}
	pre := cfg.commitmentPreimage()
	if err := c.chargeKeccak(len(pre)); err != nil {
		return Hash{}, err
	}
	return c.keccak(pre), nil
}

func (e *encoder) ownerList(owners []caOwner) *encoder {
	e.u64(uint64(e.n*32 + len(e.tail)))
	n := wordOfU64(uint64(len(owners)))
	e.tail = append(e.tail, n[:]...)
	for _, o := range owners {
		a, w := addressWord(o.owner), wordOfU64(uint64(o.weight))
		e.tail = append(append(e.tail, a[:]...), w[:]...)
	}
	return e
}

type caUpdate struct {
	current   caConfig
	threshold uint8
	owners    []caOwner
}

func (c *Context) caUpdateConfig(u caUpdate) error {
	sender := c.Caller
	if sender == (Address{}) {
		return errSel("UnauthorizedConfigurableAccountCaller()")
	}
	authority, err := c.tload(ConfigurableAccountAddress, slotCAAuthority)
	if err != nil {
		return err
	}
	direct := fCADirect.of(authority)
	if wordAddress(toHash(direct)) != sender {
		return errSel("UnauthorizedConfigurableAccountCaller()")
	}
	stored, err := c.configCommitment(sender)
	if err != nil {
		return err
	}
	h, err := c.caHashConfig(u.current)
	if err != nil {
		return err
	}
	if stored != h || u.current.version == ^uint64(0) {
		return errSel("InvalidConfig()")
	}
	next := caConfig{salt: u.current.salt, version: u.current.version + 1, threshold: u.threshold, owners: u.owners}
	if err := next.validate(sender, true); err != nil {
		return err
	}
	h, err = c.caHashConfig(next)
	if err != nil {
		return err
	}
	if h == (Hash{}) {
		return errSel("InvalidConfig()")
	}
	if err := c.setConfigCommitment(sender, h); err != nil {
		return err
	}
	data := newEncoder(4).word(next.salt).u64(next.version).u64(uint64(next.threshold)).ownerList(next.owners).out()
	return c.emit(ConfigurableAccountAddress, []Hash{evCAConfigUpdated, addressWord(sender)}, data)
}

func (c *Context) caUpgradeChecks(sender Address) error {
	origin, err := c.tload(ConfigurableAccountAddress, slotCATxOrigin)
	if err != nil {
		return err
	}
	if wordAddress(toHash(origin)) != sender {
		return errSel("UpgradeMustBeDirectCall()")
	}
	authority, err := c.tload(ConfigurableAccountAddress, slotCAAuthority)
	if err != nil {
		return err
	}
	if err := caEligibilityError(UpgradeEligibility(fCAEligibility.u64(authority))); err != nil {
		return err
	}
	if err := c.loadAccountGas(sender); err != nil {
		return err
	}
	commitment := c.commitmentOf(sender)
	codeHash := c.State.CodeHash(sender)
	if codeHash == (Hash{}) {
		codeHash = emptyCodeHash
	}
	markerOK := commitment != (Hash{}) && codeHash == configurableCodeHash
	if codeHash != emptyCodeHash && !markerOK {
		return errSel("AccountHasCode()")
	}
	if commitment != (Hash{}) {
		return errSel("AccountAlreadyConfigurable()")
	}
	return nil
}

func caEligibilityError(e UpgradeEligibility) error {
	if e == UpgradePrimitiveRoot {
		return nil
	}
	if e == UpgradeNotPrimitiveRoot {
		return errSel("PrimitiveRootRequired()")
	}
	return errSel("UpgradeMustBeDirectCall()")
}

func (c *Context) caUpgrade(threshold uint8, owners []caOwner) ([]byte, error) {
	sender := c.Caller
	if err := c.caUpgradeChecks(sender); err != nil {
		return nil, err
	}
	pre := append(append([]byte{}, caUpgradeDomain...), sender[:]...)
	if err := c.chargeKeccak(len(pre)); err != nil {
		return nil, err
	}
	cfg := caConfig{salt: c.keccak(pre), version: 1, threshold: threshold, owners: owners}
	if err := cfg.validate(sender, true); err != nil {
		return nil, errSel("InvalidConfig()")
	}
	h, err := c.caHashConfig(cfg)
	if err != nil {
		return nil, err
	}
	if h == (Hash{}) {
		return nil, errSel("InvalidConfig()")
	}
	if err := c.setConfigCommitment(sender, h); err != nil {
		return nil, err
	}
	data := newEncoder(3).word(cfg.salt).u64(uint64(threshold)).ownerList(owners).out()
	if err := c.emit(ConfigurableAccountAddress, []Hash{evCAUpgraded, addressWord(sender), h}, data); err != nil {
		return nil, err
	}
	return encodeWords(h), nil
}

func (c *Context) isRetiredRootT14(owner Address) (bool, error) {
	h, err := c.configCommitment(owner)
	return h != (Hash{}), err
}

func (cur abiCursor) owners(i int) ([]caOwner, error) {
	at, err := cur.follow(i)
	if err != nil {
		return nil, err
	}
	arr, err := at.array()
	if err != nil {
		return nil, err
	}
	out := make([]caOwner, min(arr.n, maxNestedItems))
	for k := range out {
		a, err := arr.items.scalar(2*k, tAddress)
		if err != nil {
			return nil, err
		}
		w, err := arr.items.scalar(2*k+1, tU8)
		if err != nil {
			return nil, err
		}
		out[k] = caOwner{owner: wordAddress(a), weight: w[31]}
	}
	return out, nil
}

func (cur abiCursor) u8(i int) (uint8, error) {
	w, err := cur.scalar(i, tU8)
	return w[31], err
}

func decodeCADerive(c *Context, a []abiValue) (caConfig, error) {
	cur := rawCursor(c, a)
	salt, err := cur.scalar(0, tB32)
	if err != nil {
		return caConfig{}, err
	}
	thr, err := cur.u8(1)
	if err != nil {
		return caConfig{}, err
	}
	owners, err := cur.owners(2)
	return caConfig{salt: salt, threshold: thr, owners: owners}, err
}

func decodeCAUpdate(c *Context, a []abiValue) (caUpdate, error) {
	cur := rawCursor(c, a)
	tup, err := cur.follow(0)
	if err != nil {
		return caUpdate{}, err
	}
	var u caUpdate
	if u.current.salt, err = tup.scalar(0, tB32); err != nil {
		return caUpdate{}, err
	}
	ver, err := tup.scalar(1, tU64)
	if err != nil {
		return caUpdate{}, err
	}
	u.current.version = binary.BigEndian.Uint64(ver[24:])
	if u.current.threshold, err = tup.u8(2); err != nil {
		return caUpdate{}, err
	}
	if u.current.owners, err = tup.owners(3); err != nil {
		return caUpdate{}, err
	}
	if u.threshold, err = cur.u8(1); err != nil {
		return caUpdate{}, err
	}
	u.owners, err = cur.owners(2)
	return u, err
}

func decodeCAUpgrade(c *Context, a []abiValue) (caConfig, error) {
	cur := rawCursor(c, a)
	thr, err := cur.u8(0)
	if err != nil {
		return caConfig{}, err
	}
	owners, err := cur.owners(1)
	return caConfig{threshold: thr, owners: owners}, err
}

var configurableAccountTable = register(ConfigurableAccountAddress, newTable(
	view("deriveAccount(bytes32,uint8,(address,uint8)[])", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		cfg, err := decodeCADerive(c, a)
		if err != nil {
			return nil, decodeOrRevert(err)
		}
		return c.caDerive(cfg)
	}).from(hardfork.T14),
	view("getConfigCommitment(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		h, err := c.configCommitment(a[0].addr())
		return encodeWords(h), err
	}).from(hardfork.T14),
	mutate("updateConfig((bytes32,uint64,uint8,(address,uint8)[]),uint8,(address,uint8)[])", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		u, err := decodeCAUpdate(c, a)
		if err != nil {
			return nil, decodeOrRevert(err)
		}
		return nil, c.caUpdateConfig(u)
	}).from(hardfork.T14),
	mutate("upgradeAccount(uint8,(address,uint8)[])", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		cfg, err := decodeCAUpgrade(c, a)
		if err != nil {
			return nil, decodeOrRevert(err)
		}
		return c.caUpgrade(cfg.threshold, cfg.owners)
	}).from(hardfork.T14),
))
