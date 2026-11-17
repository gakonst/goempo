package precompiles

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	zoneCreationGas      = 15_000_000
	zoneMaxSequencers    = 8
	zoneMaxMetadataBytes = 31
	zoneMaxListItems     = maxNestedItems
)

const _ = uint(zoneMaxListItems - zoneMaxSequencers)

type zoneRole uint8

const (
	zoneRoleNone zoneRole = iota
	zoneRoleSequencer
	zoneRoleAccount
	zoneRoleCallbackGateway
)

var (
	ZoneMessengerAddress = prefixAddress(0x5a, 0x4d)
	zonePortalProxyCode  = []byte{
		0x36, 0x3d, 0x3d, 0x37, 0x3d, 0x3d, 0x3d, 0x36, 0x3d, 0x73,
		0x5a, 0xd1, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x5a, 0xf4, 0x3d, 0x82, 0x80, 0x3e, 0x90, 0x3d, 0x91, 0x60,
		0x2b, 0x57, 0xfd, 0x5b, 0xf3,
	}
)

var (
	slotZoneFactory0 = slotOf(0)
	slotZoneInfos    = slotOf(1)

	fZoneNextID    = field{offset: 0, size: 4}
	fZoneOwner     = field{offset: 4, size: 20}
	fZoneInfoID    = field{offset: 0, size: 4}
	fZoneInfoPort  = field{offset: 4, size: 20}
	fZoneInfoAcc   = field{offset: 24, size: 1}
	fZoneInfoGate  = field{offset: 25, size: 1}
	fZoneInfoThr   = field{offset: 0, size: 1}
	fZoneInfoVerif = field{offset: 1, size: 20}
)

var (
	slotPortalAdmin        = slotOf(0)
	slotPortalTokenConfigs = slotOf(6)
	slotPortalEnabled      = slotOf(7)
	slotPortalRPCURL       = slotOf(12)
	slotPortalIDs          = slotOf(15)
	slotPortalFlags        = slotOf(16)
	slotPortalSequencers   = slotOf(18)
	slotPortalRoles        = slotOf(20)
	slotPortalEnforcement  = slotOf(21)
	slotPortalLeader       = slotOf(23)
	slotPortalBlocks       = slotOf(24)
	slotPortalCounts       = slotOf(25)
	slotPortalEnableHash   = slotOf(26)
	slotPortalCursor       = slotOf(28)

	fPortalZoneID      = field{offset: 0, size: 4}
	fPortalMessenger   = field{offset: 4, size: 20}
	fPortalVerifier    = field{offset: 0, size: 20}
	fPortalInitialized = field{offset: 20, size: 1}
	fPortalThreshold   = field{offset: 29, size: 1}
	fPortalAccess      = field{offset: 0, size: 1}
	fPortalGateway     = field{offset: 1, size: 1}
	fPortalLeader      = field{offset: 0, size: 20}
	fPortalEpoch       = field{offset: 20, size: 8}
	fPortalActivation  = field{offset: 0, size: 8}
	fPortalEnableBlock = field{offset: 24, size: 8}
	fPortalEnabledN    = field{offset: 0, size: 8}
	fPortalCursorInit  = field{offset: 8, size: 1}
)

var (
	evOwnershipTransferred    = topic("OwnershipTransferred(address,address)")
	evZoneCreated             = topic("ZoneCreated(uint32,address,address,bool,bool,address,address[],uint8,address)")
	evEnforcementModesUpdated = topic("EnforcementModesUpdated(bool,bool)")
	evSequencerSetUpdated     = topic("SequencerSetUpdated(uint64,uint8,address[])")
	evLeaderUpdated           = topic("LeaderUpdated(address,address,uint64,uint64)")
	evRoleUpdated             = topic("RoleUpdated(address,uint8,uint8)")
	evTokenEnabled            = topic("TokenEnabled(address,string,string,string)")
)

type createZoneParams struct {
	token     Address
	access    flag
	gateway   flag
	allowed   []Address
	gateways  []Address
	admin     Address
	sequencer []Address
	threshold uint8
	rpcURL    []byte
}

type zoneTokenMeta struct {
	name     []byte
	symbol   []byte
	currency []byte
}

type zoneInfo struct {
	id        uint32
	portal    Address
	access    flag
	gateway   flag
	admin     Address
	sequencer []Address
	threshold uint8
	verifier  Address
	rpcURL    []byte
}

type portalInit struct {
	at         Address
	id         uint32
	p          createZoneParams
	enableHash Hash
}

type fieldWrite struct {
	s slot
	f field
	v Word
}

type zoneRoleChange struct {
	account Address
	prev    zoneRole
	next    zoneRole
}

func ZonePortalAddress(zoneID uint32) Address {
	var a Address
	copy(a[:12], zonePortalPrefix[:])
	binary.BigEndian.PutUint64(a[12:], uint64(zoneID))
	return a
}

func (e *encoder) addrList(list []Address) *encoder {
	e.u64(uint64(e.n*32 + len(e.tail)))
	n := wordOfU64(uint64(len(list)))
	e.tail = append(e.tail, n[:]...)
	for _, a := range list {
		w := addressWord(a)
		e.tail = append(e.tail, w[:]...)
	}
	return e
}

func (cur abiCursor) text() ([]byte, error) {
	d, err := decodeDynamicValue(cur.data, dynAt{offset: cur.base, typ: abiType{kind: argString}}, decodeMode{strict: cur.strict})
	return d.value.data, err
}

func (cur abiCursor) followAddresses(i int) ([]Address, error) {
	next, err := cur.follow(i)
	if err != nil {
		return nil, err
	}
	return next.addresses()
}

func decodeCreateZone(c *Context, a []abiValue) (createZoneParams, error) {
	cur, err := rawCursor(c, a).follow(0)
	if err != nil {
		return createZoneParams{}, err
	}
	var words [5]Hash
	types := [5]abiType{tAddress, tBool, tBool, tAddress, tU8}
	at := [5]int{0, 1, 2, 5, 7}
	for i := range words {
		if words[i], err = cur.scalar(at[i], types[i]); err != nil {
			return createZoneParams{}, err
		}
	}
	p := createZoneParams{
		token: wordAddress(words[0]), access: flag(!words[1].IsZero()), gateway: flag(!words[2].IsZero()),
		admin: wordAddress(words[3]), threshold: words[4][31],
	}
	if p.allowed, err = cur.followAddresses(3); err != nil {
		return createZoneParams{}, err
	}
	if p.gateways, err = cur.followAddresses(4); err != nil {
		return createZoneParams{}, err
	}
	if p.sequencer, err = cur.followAddresses(6); err != nil {
		return createZoneParams{}, err
	}
	rpc, err := cur.follow(8)
	if err != nil {
		return createZoneParams{}, err
	}
	p.rpcURL, err = rpc.text()
	return p, err
}

func zoneContains(list []Address, a Address) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

func (c *Context) zoneOwner() (Address, error) {
	w, err := c.loadField(ZoneFactoryAddress, slotZoneFactory0, fZoneOwner)
	return wordAddress(toHash(w)), err
}

func (c *Context) zoneNextID() (uint32, error) {
	w, err := c.loadField(ZoneFactoryAddress, slotZoneFactory0, fZoneNextID)
	return uint32(w.Uint64()), err
}

func (c *Context) zoneTransferOwnership(next Address) error {
	prev, err := c.zoneOwner()
	if err != nil {
		return err
	}
	if c.Caller != prev {
		return errSel("NotOwner()")
	}
	err = c.storeField(ZoneFactoryAddress, slotZoneFactory0, fZoneOwner, toWord(addressWord(next)))
	if err != nil {
		return err
	}
	return c.emit(ZoneFactoryAddress, []Hash{evOwnershipTransferred, addressWord(prev), addressWord(next)}, nil)
}

func zoneClosedLoopLegacy(p createZoneParams) error {
	for _, g := range p.gateways {
		if zoneContains(p.allowed, g) {
			return errSel("InvalidClosedLoopConfig()")
		}
	}
	for _, s := range p.sequencer {
		if zoneContains(p.allowed, s) {
			return errSel("InvalidClosedLoopConfig()")
		}
		if zoneContains(p.gateways, s) {
			return errSel("InvalidClosedLoopConfig()")
		}
	}
	return nil
}

func (c *Context) zoneClosedLoop(p createZoneParams) error {
	if zoneContains(p.allowed, ZoneMessengerAddress) {
		return errSel("InvalidClosedLoopConfig()")
	}
	if !c.at(hardfork.T11) {
		return zoneClosedLoopLegacy(p)
	}
	all := make([]Hash, 0, len(p.allowed)+len(p.gateways)+len(p.sequencer))
	for _, list := range [3][]Address{p.allowed, p.gateways, p.sequencer} {
		for _, a := range list {
			all = append(all, addressWord(a))
		}
	}
	dup, err := c.hasDuplicates(all)
	if err != nil {
		return err
	}
	if dup {
		return errSel("InvalidClosedLoopConfig()")
	}
	return nil
}

func zoneSequencerSet(p createZoneParams) error {
	n := len(p.sequencer)
	if n == 0 || n > zoneMaxSequencers || p.threshold == 0 || int(p.threshold) > n {
		return errSel("InvalidSequencerSet()")
	}
	for i, s := range p.sequencer {
		if s == (Address{}) || zoneContains(p.sequencer[:i], s) {
			return errSel("InvalidSequencerSet()")
		}
	}
	return nil
}

func (c *Context) zoneTokenMeta(token Address) (zoneTokenMeta, error) {
	var m zoneTokenMeta
	var err error
	if m.name, err = c.loadString(token, slotName); err != nil {
		return m, err
	}
	if m.symbol, err = c.loadString(token, slotSymbol); err != nil {
		return m, err
	}
	if m.currency, err = c.loadString(token, slotCurrency); err != nil {
		return m, err
	}
	for _, s := range [3][]byte{m.name, m.symbol, m.currency} {
		if len(s) > zoneMaxMetadataBytes {
			return m, errSel("TokenMetadataTooLong()")
		}
	}
	return m, nil
}

func (c *Context) zoneCreateChecks(p createZoneParams) error {
	owner, err := c.zoneOwner()
	if err != nil {
		return err
	}
	if c.Caller != owner {
		return errSel("NotOwner()")
	}
	ok, err := IsTIP20Deployed(c, p.token)
	if err != nil {
		return err
	}
	if !ok {
		return errSel("InvalidToken()")
	}
	pol, err := c.registeredTokenPolicy(p.token)
	if err != nil {
		return err
	}
	if !pol.set {
		return errSel("TokenTransferPolicyNotSet()")
	}
	if err := c.zoneClosedLoop(p); err != nil {
		return err
	}
	if p.admin == (Address{}) {
		return errSel("InvalidAdmin()")
	}
	return zoneSequencerSet(p)
}

func (c *Context) zoneEnablementHash(token Address, meta zoneTokenMeta) Hash {
	return c.keccak(newEncoder(5).word(Hash{}).addr(token).bytes(meta.name).bytes(meta.symbol).bytes(meta.currency).out())
}

func (c *Context) createZone(p createZoneParams) ([]byte, error) {
	if err := c.charge(zoneCreationGas); err != nil {
		return nil, err
	}
	if err := c.zoneCreateChecks(p); err != nil {
		return nil, err
	}
	id, err := c.zoneNextID()
	if err != nil {
		return nil, err
	}
	portal := ZonePortalAddress(id)
	meta, err := c.zoneTokenMeta(p.token)
	if err != nil {
		return nil, err
	}
	enableHash := c.zoneEnablementHash(p.token, meta)
	if id == ^uint32(0) {
		return nil, underOverflow()
	}
	err = c.storeField(ZoneFactoryAddress, slotZoneFactory0, fZoneNextID, u256.FromUint64(uint64(id)+1))
	if err != nil {
		return nil, err
	}
	in := portalInit{at: portal, id: id, p: p, enableHash: enableHash}
	if err := c.initPortal(in); err != nil {
		return nil, err
	}
	info := zoneInfo{
		id: id, portal: portal, access: p.access, gateway: p.gateway, admin: p.admin,
		sequencer: p.sequencer, threshold: p.threshold, verifier: ZoneVerifierAddress, rpcURL: p.rpcURL,
	}
	if err := c.writeZoneInfo(info); err != nil {
		return nil, err
	}
	if err := c.emitPortalEvents(in, meta); err != nil {
		return nil, err
	}
	data := newEncoder(7).addr(p.token).word(p.access.hash()).word(p.gateway.hash()).addr(p.admin).
		addrList(p.sequencer).u64(uint64(p.threshold)).addr(ZoneVerifierAddress).out()
	err = c.emit(ZoneFactoryAddress, []Hash{evZoneCreated, wordOfU64(uint64(id)), addressWord(portal)}, data)
	if err != nil {
		return nil, err
	}
	return encodeWords(wordOfU64(uint64(id)), addressWord(portal)), nil
}

func (c *Context) storedVecLen(addr Address, s slot) (int, error) {
	n, err := c.sload(addr, s)
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() || n.Uint64() > uint64(^uint32(0)) {
		return 0, underOverflow()
	}
	if n.Uint64() > zoneMaxListItems {
		return 0, errFatal
	}
	return int(n.Uint64()), nil
}

func (c *Context) storeAddressVec(addr Address, s slot, values []Address) error {
	if len(values) > zoneMaxListItems {
		return errFatal
	}
	base := slot(c.keccak(s[:]))
	if c.at(hardfork.T5) {
		old, err := c.storedVecLen(addr, s)
		if err != nil {
			return err
		}
		for i := len(values); i < min(old, zoneMaxListItems); i++ {
			if err := c.sstore(addr, base.plus(uint64(i)), Word{}); err != nil {
				return err
			}
		}
	}
	if err := c.sstore(addr, s, u256.FromUint64(uint64(len(values)))); err != nil {
		return err
	}
	for i, v := range values {
		if err := c.sstore(addr, base.plus(uint64(i)), toWord(addressWord(v))); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) loadAddressVec(addr Address, s slot) ([]Address, error) {
	n, err := c.storedVecLen(addr, s)
	if err != nil {
		return nil, err
	}
	out := make([]Address, min(n, zoneMaxListItems))
	base := slot(c.keccak(s[:]))
	for i := range out {
		if out[i], err = c.loadAddress(addr, base.plus(uint64(i))); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *Context) setPortalRoles(at Address, list []Address, role zoneRole) error {
	for _, a := range list {
		if err := c.sstore(at, c.mapAddr(a, slotPortalRoles), u256.FromUint64(uint64(role))); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) storeFields(addr Address, writes ...fieldWrite) error {
	for _, w := range writes {
		if err := c.storeField(addr, w.s, w.f, w.v); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) initPortalHead(in portalInit) error {
	at, p := in.at, in.p
	inited, err := c.loadField(at, slotPortalFlags, fPortalInitialized)
	if err != nil {
		return err
	}
	if !inited.IsZero() {
		return errSel("AlreadyInitialized()")
	}
	if err := c.setCode(at, zonePortalProxyCode); err != nil {
		return err
	}
	if err := c.storeAddress(at, slotPortalAdmin, p.admin); err != nil {
		return err
	}
	if err := c.sstore(at, c.mapAddr(p.token, slotPortalTokenConfigs), u256.FromUint64(0x0101)); err != nil {
		return err
	}
	if err := c.storeAddressVec(at, slotPortalEnabled, []Address{p.token}); err != nil {
		return err
	}
	if err := c.storeString(at, slotPortalRPCURL, p.rpcURL); err != nil {
		return err
	}
	return c.storeFields(at,
		fieldWrite{slotPortalIDs, fPortalZoneID, u256.FromUint64(uint64(in.id))},
		fieldWrite{slotPortalIDs, fPortalMessenger, toWord(addressWord(ZoneMessengerAddress))},
		fieldWrite{slotPortalFlags, fPortalVerifier, toWord(addressWord(ZoneVerifierAddress))},
		fieldWrite{slotPortalFlags, fPortalInitialized, u256.One()},
		fieldWrite{slotPortalFlags, fPortalThreshold, u256.FromUint64(uint64(p.threshold))},
	)
}

func (c *Context) initPortal(in portalInit) error {
	at, p := in.at, in.p
	if err := c.initPortalHead(in); err != nil {
		return err
	}
	if err := c.storeAddressVec(at, slotPortalSequencers, p.sequencer); err != nil {
		return err
	}
	if err := c.setPortalRoles(at, p.sequencer, zoneRoleSequencer); err != nil {
		return err
	}
	block := u256.FromUint64(c.Block.Number)
	err := c.storeFields(at,
		fieldWrite{slotPortalEnforcement, fPortalAccess, p.access.word()},
		fieldWrite{slotPortalEnforcement, fPortalGateway, p.gateway.word()},
		fieldWrite{slotPortalLeader, fPortalLeader, toWord(addressWord(p.sequencer[0]))},
		fieldWrite{slotPortalLeader, fPortalEpoch, u256.One()},
		fieldWrite{slotPortalBlocks, fPortalActivation, block},
		fieldWrite{slotPortalBlocks, fPortalEnableBlock, block},
		fieldWrite{slotPortalCounts, fPortalEnabledN, u256.One()},
	)
	if err != nil {
		return err
	}
	if err := c.sstore(at, slotPortalEnableHash, toWord(in.enableHash)); err != nil {
		return err
	}
	if c.at(hardfork.T13) {
		if err := c.storeField(at, slotPortalCursor, fPortalCursorInit, u256.One()); err != nil {
			return err
		}
	}
	if err := c.setPortalRoles(at, p.gateways, zoneRoleCallbackGateway); err != nil {
		return err
	}
	return c.setPortalRoles(at, p.allowed, zoneRoleAccount)
}

func (c *Context) zoneInfoSlot(id uint32) slot { return c.mapKey(wordOfU64(uint64(id)), slotZoneInfos) }

func (c *Context) writeZoneInfo(z zoneInfo) error {
	base := c.zoneInfoSlot(z.id)
	w0 := pack(Word{}).withU64(fZoneInfoID, uint64(z.id)).with(fZoneInfoPort, toWord(addressWord(z.portal))).
		with(fZoneInfoAcc, z.access.word()).with(fZoneInfoGate, z.gateway.word()).w
	if err := c.sstore(ZoneFactoryAddress, base, w0); err != nil {
		return err
	}
	if err := c.storeAddress(ZoneFactoryAddress, base.plus(1), z.admin); err != nil {
		return err
	}
	if err := c.storeAddressVec(ZoneFactoryAddress, base.plus(2), z.sequencer); err != nil {
		return err
	}
	w3 := pack(Word{}).withU64(fZoneInfoThr, uint64(z.threshold)).with(fZoneInfoVerif, toWord(addressWord(z.verifier))).w
	if err := c.sstore(ZoneFactoryAddress, base.plus(3), w3); err != nil {
		return err
	}
	return c.storeString(ZoneFactoryAddress, base.plus(4), z.rpcURL)
}

func (c *Context) readZoneInfo(id uint32) (zoneInfo, error) {
	base := c.zoneInfoSlot(id)
	w0, err := c.sload(ZoneFactoryAddress, base)
	if err != nil {
		return zoneInfo{}, err
	}
	z := zoneInfo{
		id: uint32(fZoneInfoID.u64(w0)), portal: wordAddress(toHash(fZoneInfoPort.of(w0))),
		access: flag(!fZoneInfoAcc.of(w0).IsZero()), gateway: flag(!fZoneInfoGate.of(w0).IsZero()),
	}
	if z.admin, err = c.loadAddress(ZoneFactoryAddress, base.plus(1)); err != nil {
		return zoneInfo{}, err
	}
	if z.sequencer, err = c.loadAddressVec(ZoneFactoryAddress, base.plus(2)); err != nil {
		return zoneInfo{}, err
	}
	w3, err := c.sload(ZoneFactoryAddress, base.plus(3))
	if err != nil {
		return zoneInfo{}, err
	}
	z.threshold = uint8(fZoneInfoThr.u64(w3))
	z.verifier = wordAddress(toHash(fZoneInfoVerif.of(w3)))
	z.rpcURL, err = c.loadString(ZoneFactoryAddress, base.plus(4))
	return z, err
}

func encodeZoneInfo(z zoneInfo) []byte {
	body := newEncoder(9).u64(uint64(z.id)).addr(z.portal).word(z.access.hash()).word(z.gateway.hash()).
		addr(z.admin).addrList(z.sequencer).u64(uint64(z.threshold)).addr(z.verifier).bytes(z.rpcURL).out()
	return append(encodeWords(wordOfU64(32)), body...)
}

func (c *Context) emitRole(at Address, r zoneRoleChange) error {
	data := encodeWords(wordOfU64(uint64(r.prev)), wordOfU64(uint64(r.next)))
	return c.emit(at, []Hash{evRoleUpdated, addressWord(r.account)}, data)
}

func (c *Context) emitPortalRoles(at Address, p createZoneParams) error {
	for i, g := range p.gateways {
		r := zoneRoleChange{account: g, next: zoneRoleCallbackGateway}
		if zoneContains(p.gateways[:i], g) {
			r.prev = zoneRoleCallbackGateway
		}
		if err := c.emitRole(at, r); err != nil {
			return err
		}
	}
	for i, a := range p.allowed {
		r := zoneRoleChange{account: a, next: zoneRoleAccount}
		if zoneContains(p.allowed[:i], a) {
			r.prev = zoneRoleAccount
		} else if zoneContains(p.gateways, a) {
			r.prev = zoneRoleCallbackGateway
		}
		if err := c.emitRole(at, r); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) emitPortalEvents(in portalInit, meta zoneTokenMeta) error {
	at, p := in.at, in.p
	if err := c.emit(at, []Hash{evEnforcementModesUpdated}, encodeWords(p.access.hash(), p.gateway.hash())); err != nil {
		return err
	}
	data := newEncoder(2).u64(uint64(p.threshold)).addrList(p.sequencer).out()
	if err := c.emit(at, []Hash{evSequencerSetUpdated, {}}, data); err != nil {
		return err
	}
	topics := []Hash{evLeaderUpdated, {}, addressWord(p.sequencer[0]), wordOfU64(1)}
	if err := c.emit(at, topics, encodeWords(wordOfU64(c.Block.Number))); err != nil {
		return err
	}
	if err := c.emitPortalRoles(at, p); err != nil {
		return err
	}
	data = newEncoder(3).bytes(meta.name).bytes(meta.symbol).bytes(meta.currency).out()
	return c.emit(at, []Hash{evTokenEnabled, addressWord(p.token)}, data)
}

func (c *Context) isZonePortal(a Address) (bool, error) {
	if [12]byte(a[:12]) != zonePortalPrefix {
		return false, nil
	}
	next, err := c.zoneNextID()
	if err != nil {
		return false, err
	}
	return binary.BigEndian.Uint64(a[12:]) < uint64(next), nil
}

var zoneFactoryTable = register(ZoneFactoryAddress, newTable(
	view("owner()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		o, err := c.zoneOwner()
		return retAddr(o), err
	}),
	mutate("transferOwnership(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		return nil, c.zoneTransferOwnership(a[0].addr())
	}),
	mutate("createZone((address,bool,bool,address[],address[],address,address[],uint8,string))", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		p, err := decodeCreateZone(c, a)
		if err != nil {
			return nil, decodeOrRevert(err)
		}
		return c.createZone(p)
	}),
	view("nextZoneId()", nil, func(c *Context, a []abiValue) ([]byte, error) {
		id, err := c.zoneNextID()
		return retU64(uint64(id)), err
	}),
	view("zones(uint32)", []abiType{tU32}, func(c *Context, a []abiValue) ([]byte, error) {
		z, err := c.readZoneInfo(uint32(a[0].u64()))
		if err != nil {
			return nil, err
		}
		return encodeZoneInfo(z), nil
	}),
	view("isZonePortal(address)", []abiType{tAddress}, func(c *Context, a []abiValue) ([]byte, error) {
		ok, err := c.isZonePortal(a[0].addr())
		return retBool(flag(ok)), err
	}),
))
