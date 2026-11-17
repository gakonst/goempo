package precompiles

import (
	"unicode/utf8"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	slotV2Config     = slotOf(0)
	slotV2Validators = slotOf(1)
	slotV2AddrIndex  = slotOf(2)
	slotV2KeyIndex   = slotOf(3)
	slotV2NextEpoch  = slotOf(4)
	slotV2Ingress    = slotOf(5)
	slotV2Active     = slotOf(6)

	fV2Owner       = field{offset: 0, size: 20}
	fV2IsInit      = field{offset: 20, size: 1}
	fV2InitHeight  = field{offset: 21, size: 8}
	fV2Skipped     = field{offset: 29, size: 1}
	fV2V1Count     = field{offset: 30, size: 1}
	fV2Address     = field{offset: 0, size: 20}
	fV2Index       = field{offset: 20, size: 8}
	fV2ActiveIdx   = field{offset: 0, size: 8}
	fV2Added       = field{offset: 8, size: 8}
	fV2Deactivated = field{offset: 16, size: 8}
)

const (
	v2RecordSlots     = 6
	v2ActivePerSlot   = 4
	v2ActiveBytes     = 8
	v2MaxValidators   = 1<<32 - 1
	v2EndpointMax     = 255
	v2NamespaceAdd    = "TEMPO_VALIDATOR_CONFIG_V2_ADD_VALIDATOR"
	v2NamespaceRotate = "TEMPO_VALIDATOR_CONFIG_V2_ROTATE_VALIDATOR"
	v2IPPortMessage   = "input was not of the form `<ip>:<port>`"
	v2IPMessage       = "input was not a valid IP address"
	v2TooLongReason   = "IP address exceeds 255 bytes"
	v2MappedReason    = "IPv4-mapped IPv6 address; use the IPv4 form"
	panicOutOfBounds  = 0x32
	v2SignatureBytes  = 64
	v2SkippedMax      = 0xff
)

const (
	_ = uint(32 - v2ActivePerSlot*v2ActiveBytes)
	_ = uint(maxStringBytes - v2EndpointMax)
	_ = uint(1<<(8*1) - 1 - v2SkippedMax)
)

var (
	evV2ValidatorAdded       = topic("ValidatorAdded(uint64,address,bytes32,string,string,address)")
	evV2ValidatorDeactivated = topic("ValidatorDeactivated(uint64,address)")
	evV2ValidatorRotated     = topic("ValidatorRotated(uint64,uint64,address,bytes32,bytes32,string,string,address)")
	evV2FeeRecipientUpdated  = topic("FeeRecipientUpdated(uint64,address,address)")
	evV2IPAddressesUpdated   = topic("IpAddressesUpdated(uint64,string,string,address)")
	evV2OwnershipMoved       = topic("ValidatorOwnershipTransferred(uint64,address,address,address)")
	evV2OwnershipTransferred = topic("OwnershipTransferred(address,address)")
	evV2RotationEpochSet     = topic("NetworkIdentityRotationEpochSet(uint64,uint64)")
	evV2ValidatorMigrated    = topic("ValidatorMigrated(uint64,address,bytes32)")
	evV2SkippedMigration     = topic("SkippedValidatorMigration(uint64,address,bytes32)")
	evV2Initialized          = topic("Initialized(uint64)")
)

type v2Config struct{ w Word }

func (k v2Config) owner() Address  { return wordAddress(toHash(fV2Owner.of(k.w))) }
func (k v2Config) isInit() bool    { return !fV2IsInit.of(k.w).IsZero() }
func (k v2Config) skipped() uint64 { return fV2Skipped.u64(k.w) }
func (k v2Config) v1Count() uint64 { return fV2V1Count.u64(k.w) }

type v2Record struct {
	publicKey   Hash
	validator   Address
	ingress     []byte
	egress      []byte
	fee         Address
	index       uint64
	activeIdx   uint64
	added       uint64
	deactivated uint64
}

type endpoints struct {
	ingress []byte
	egress  []byte
}

func v2Panic(code uint64) error { return revert(abiError(selPanic, wordOfU64(code))) }

func v2RevertStrings(sig string, args ...[]byte) error {
	enc := newEncoder(len(args))
	for _, a := range args {
		enc.bytes(a)
	}
	s := selector(sig)
	return revert(append(s[:], enc.out()...))
}

func (c *Context) v2Load(s slot) (Word, error) { return c.sload(ValidatorConfigV2Address, s) }

func (c *Context) v2Store(s slot, v Word) error { return c.sstore(ValidatorConfigV2Address, s, v) }

func (c *Context) v2LoadU64(s slot) (uint64, error) {
	w, err := c.v2Load(s)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

func (c *Context) v2LoadString(s slot) ([]byte, error) {
	b, err := c.loadString(ValidatorConfigV2Address, s)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) {
		return nil, errFatal
	}
	return b, nil
}

func (c *Context) v2StoreFreshString(s slot, value []byte) error {
	if len(value) > maxStringBytes {
		return errFatal
	}
	if len(value) < 32 {
		var b [32]byte
		copy(b[:], value)
		b[31] = byte(len(value) * 2)
		return c.v2Store(s, u256.FromBytes32(b))
	}
	if err := c.v2Store(s, u256.FromUint64(uint64(len(value))*2+1)); err != nil {
		return err
	}
	base := slot(c.keccak(s[:]))
	words := min((len(value)+31)/32, maxStringWords)
	for i := 0; i < words; i++ {
		var b [32]byte
		copy(b[:], value[i*32:])
		if err := c.v2Store(base.plus(uint64(i)), u256.FromBytes32(b)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Context) v2StoreString(s slot, value []byte, fresh flag) error {
	if fresh {
		return c.v2StoreFreshString(s, value)
	}
	return c.storeString(ValidatorConfigV2Address, s, value)
}

func (c *Context) v2ReadConfig() (v2Config, error) {
	w, err := c.v2Load(slotV2Config)
	return v2Config{w: w}, err
}

func (c *Context) v2WriteConfig(w Word) error {
	return c.storeRun(ValidatorConfigV2Address, slotV2Config,
		lane{fV2Owner, fV2Owner.of(w)}, lane{fV2IsInit, fV2IsInit.of(w)},
		lane{fV2InitHeight, fV2InitHeight.of(w)}, lane{fV2Skipped, fV2Skipped.of(w)},
		lane{fV2V1Count, fV2V1Count.of(w)})
}

func v2RequireInit(k v2Config) error {
	if !k.isInit() {
		return errSel("NotInitialized()")
	}
	return nil
}

func v2RequireOwner(k v2Config, caller Address) error {
	if k.owner() != caller {
		return errSel("Unauthorized()")
	}
	return nil
}

type authority struct {
	caller    Address
	validator Address
}

func v2RequireOwnerOrValidator(k v2Config, a authority) error {
	if a.caller == a.validator {
		return nil
	}
	return v2RequireOwner(k, a.caller)
}

func (c *Context) v2RecordSlot(index uint64) slot {
	return slot(c.keccak(slotV2Validators[:])).plus(index * v2RecordSlots)
}

func (c *Context) v2ReadRecord(index uint64) (v2Record, error) {
	base := c.v2RecordSlot(index)
	pk, err := c.v2Load(base)
	if err != nil {
		return v2Record{}, err
	}
	addrW, err := c.v2Load(base.plus(1))
	if err != nil {
		return v2Record{}, err
	}
	ingress, err := c.v2LoadString(base.plus(2))
	if err != nil {
		return v2Record{}, err
	}
	egress, err := c.v2LoadString(base.plus(3))
	if err != nil {
		return v2Record{}, err
	}
	w4, err := c.v2Load(base.plus(4))
	if err != nil {
		return v2Record{}, err
	}
	w5, err := c.v2Load(base.plus(5))
	return v2Record{
		publicKey: toHash(pk), validator: wordAddress(toHash(fV2Address.of(addrW))),
		ingress: ingress, egress: egress, fee: wordAddress(toHash(fV2Address.of(w4))),
		index: fV2Index.u64(w4), activeIdx: fV2ActiveIdx.u64(w5), added: fV2Added.u64(w5),
		deactivated: fV2Deactivated.u64(w5),
	}, err
}

func (c *Context) v2WriteRecord(index uint64, r v2Record, fresh flag) error {
	a := ValidatorConfigV2Address
	base := c.v2RecordSlot(index)
	if err := c.v2Store(base, toWord(r.publicKey)); err != nil {
		return err
	}
	if err := c.storeRun(a, base.plus(1), lane{fV2Address, toWord(addressWord(r.validator))}); err != nil {
		return err
	}
	if err := c.v2StoreString(base.plus(2), r.ingress, fresh); err != nil {
		return err
	}
	if err := c.v2StoreString(base.plus(3), r.egress, fresh); err != nil {
		return err
	}
	err := c.storeRun(a, base.plus(4), lane{fV2Address, toWord(addressWord(r.fee))},
		lane{fV2Index, u256.FromUint64(r.index)})
	if err != nil {
		return err
	}
	return c.storeRun(a, base.plus(5), lane{fV2ActiveIdx, u256.FromUint64(r.activeIdx)},
		lane{fV2Added, u256.FromUint64(r.added)}, lane{fV2Deactivated, u256.FromUint64(r.deactivated)})
}

func (c *Context) v2Count() (uint64, error) {
	w, err := c.v2Load(slotV2Validators)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() || w.Uint64() > v2MaxValidators {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

func (c *Context) v2PushRecord(r v2Record) error {
	n, err := c.v2Count()
	if err != nil {
		return err
	}
	if err := c.v2WriteRecord(n, r, true); err != nil {
		return err
	}
	return c.v2Store(slotV2Validators, u256.FromUint64(n+1))
}

func (c *Context) v2DeactivatedAt(index uint64) (uint64, error) {
	w, err := c.loadField(ValidatorConfigV2Address, c.v2RecordSlot(index).plus(5), fV2Deactivated)
	return w.Uint64(), err
}

func (c *Context) v2ActiveIdxSlot(index uint64) slot {
	return c.v2RecordSlot(index).plus(5)
}

func (c *Context) v2ActiveLen() (uint64, error) {
	w, err := c.v2Load(slotV2Active)
	if err != nil {
		return 0, err
	}
	if !w.IsUint64() || w.Uint64() > v2MaxValidators {
		return 0, underOverflow()
	}
	return w.Uint64(), nil
}

type activeElem struct {
	s slot
	f field
}

func (c *Context) v2ActiveElem(i uint64) activeElem {
	base := slot(c.keccak(slotV2Active[:]))
	return activeElem{s: base.plus(i / v2ActivePerSlot), f: field{offset: uint(i%v2ActivePerSlot) * v2ActiveBytes, size: v2ActiveBytes}}
}

func (c *Context) v2ActiveAt(i uint64) (uint64, error) {
	e := c.v2ActiveElem(i)
	w, err := c.loadField(ValidatorConfigV2Address, e.s, e.f)
	return w.Uint64(), err
}

type activeWrite struct {
	pos   uint64
	index uint64
}

func (c *Context) v2SetActiveAt(w activeWrite) error {
	e := c.v2ActiveElem(w.pos)
	return c.storeField(ValidatorConfigV2Address, e.s, e.f, u256.FromUint64(w.index))
}

func (c *Context) v2PushActive(v uint64) error {
	n, err := c.v2ActiveLen()
	if err != nil {
		return err
	}
	if err := c.v2SetActiveAt(activeWrite{pos: n, index: v}); err != nil {
		return err
	}
	return c.v2Store(slotV2Active, u256.FromUint64(n+1))
}

func (c *Context) v2PopActive() error {
	n, err := c.v2ActiveLen()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if _, err := c.v2ActiveAt(n - 1); err != nil {
		return err
	}
	if err := c.v2SetActiveAt(activeWrite{pos: n - 1}); err != nil {
		return err
	}
	return c.v2Store(slotV2Active, u256.FromUint64(n-1))
}

func (c *Context) v2ActiveValidator(index uint64) (v2Record, error) {
	n, err := c.v2Count()
	if err != nil {
		return v2Record{}, err
	}
	if index >= n {
		return v2Record{}, errSel("ValidatorNotFound()")
	}
	r, err := c.v2ReadRecord(index)
	if err != nil {
		return v2Record{}, err
	}
	if r.deactivated != 0 {
		return v2Record{}, errSel("ValidatorAlreadyDeactivated()")
	}
	return r, nil
}

func (c *Context) v2AddrIndexSlot(a Address) slot { return c.mapAddr(a, slotV2AddrIndex) }

func (c *Context) v2KeyIndexSlot(k Hash) slot { return c.mapKey(k, slotV2KeyIndex) }

func (c *Context) v2IngressSlot(k Hash) slot { return c.mapKey(k, slotV2Ingress) }

func notIPPort(input []byte) error {
	return v2RevertStrings("NotIpPort(string,string)", input, []byte(v2IPPortMessage))
}

func invalidIP(input []byte, reason string) error {
	return v2RevertStrings("InvalidIp(string,string)", input, []byte(reason))
}

func (c *Context) v2ValidateEndpoints(e endpoints) error {
	t14 := c.at(hardfork.T14)
	if t14 && len(e.ingress) > v2EndpointMax {
		return invalidIP(e.ingress, v2TooLongReason)
	}
	sock, err := parseSocketAddr(e.ingress)
	if err != nil {
		return notIPPort(e.ingress)
	}
	if t14 && sock.ip.ipv4Mapped() {
		return invalidIP(e.ingress, v2MappedReason)
	}
	if t14 && len(e.egress) > v2EndpointMax {
		return invalidIP(e.egress, v2TooLongReason)
	}
	ip, err := parseIPAddr(e.egress)
	if err != nil {
		return v2RevertStrings("NotIp(string,string)", e.egress, []byte(v2IPMessage))
	}
	if t14 && ip.ipv4Mapped() {
		return invalidIP(e.egress, v2MappedReason)
	}
	return nil
}

func (c *Context) v2IngressKey(ingress []byte) (Hash, error) {
	sock, err := parseSocketAddr(ingress)
	if err != nil {
		return Hash{}, notIPPort(ingress)
	}
	buf := make([]byte, 0, 22)
	if sock.ip.v6 {
		buf = append(buf, sock.ip.bytes[:]...)
		buf = append(buf, byte(sock.scope>>24), byte(sock.scope>>16), byte(sock.scope>>8), byte(sock.scope))
	} else {
		buf = append(buf, sock.ip.bytes[:4]...)
	}
	buf = append(buf, byte(sock.port>>8), byte(sock.port))
	return c.keccak(buf), nil
}

func ingressExists(ingress []byte) error {
	return v2RevertStrings("IngressAlreadyExists(string)", ingress)
}

func (c *Context) v2RequireUniqueIngress(ingress []byte) (Hash, error) {
	k, err := c.v2IngressKey(ingress)
	if err != nil {
		return Hash{}, err
	}
	w, err := c.v2Load(c.v2IngressSlot(k))
	if err != nil {
		return Hash{}, err
	}
	if !w.IsZero() {
		return Hash{}, ingressExists(ingress)
	}
	return k, nil
}

type ingressMove struct {
	old []byte
	new []byte
}

func (c *Context) v2UpdateIngress(m ingressMove) error {
	oldKey, err := c.v2IngressKey(m.old)
	if err != nil {
		return err
	}
	newKey, err := c.v2IngressKey(m.new)
	if err != nil {
		return err
	}
	if oldKey == newKey {
		return nil
	}
	w, err := c.v2Load(c.v2IngressSlot(newKey))
	if err != nil {
		return err
	}
	if !w.IsZero() {
		return ingressExists(m.new)
	}
	if err := c.v2Store(c.v2IngressSlot(oldKey), Word{}); err != nil {
		return err
	}
	return c.v2Store(c.v2IngressSlot(newKey), u256.FromUint64(1))
}

func (c *Context) v2RequireNewAddress(a Address) error {
	if a == (Address{}) {
		return errSel("InvalidValidatorAddress()")
	}
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

func (c *Context) v2RequireNewKey(k Hash) error {
	if k == (Hash{}) {
		return errSel("InvalidPublicKey()")
	}
	w, err := c.v2Load(c.v2KeyIndexSlot(k))
	if err != nil {
		return err
	}
	if !w.IsZero() {
		return errSel("PublicKeyAlreadyExists()")
	}
	return nil
}

type signedEndpoints struct {
	publicKey Hash
	signature []byte
	validator Address
	fee       Address
	rotate    flag
	endpoints
}

func (c *Context) v2VerifySignature(s signedEndpoints) error {
	if len(s.signature) != v2SignatureBytes {
		return errSel("InvalidSignatureFormat()")
	}
	if len(s.ingress) > v2EndpointMax || len(s.egress) > v2EndpointMax {
		return errFatal
	}
	msg := make([]byte, 0, 8+20+20+2+len(s.ingress)+len(s.egress)+20)
	chain := c.ChainID.Uint64()
	for i := 7; i >= 0; i-- {
		msg = append(msg, byte(chain>>(8*uint(i))))
	}
	msg = append(msg, ValidatorConfigV2Address[:]...)
	msg = append(msg, s.validator[:]...)
	msg = append(msg, byte(len(s.ingress)))
	msg = append(msg, s.ingress...)
	msg = append(msg, byte(len(s.egress)))
	msg = append(msg, s.egress...)
	ns := v2NamespaceRotate
	if !s.rotate {
		msg = append(msg, s.fee[:]...)
		ns = v2NamespaceAdd
	}
	digest := c.keccak(msg)
	if !ed25519PublicKeyDecodes(s.publicKey) {
		return errSel("InvalidPublicKey()")
	}
	signed := make([]byte, 0, 1+len(ns)+32)
	signed = append(signed, byte(len(ns)))
	signed = append(signed, ns...)
	signed = append(signed, digest[:]...)
	if !ed25519VerifyZIP215(edSigned{pub: s.publicKey, sig: s.signature, msg: signed}) {
		return errSel("InvalidSignature()")
	}
	return nil
}

type v2Append struct {
	validator   Address
	publicKey   Hash
	fee         Address
	added       uint64
	deactivated uint64
	endpoints
}

func (c *Context) v2AppendValidator(a v2Append) (uint64, error) {
	n, err := c.v2Count()
	if err != nil {
		return 0, err
	}
	activeIdx := uint64(0)
	if a.deactivated == 0 {
		if err := c.v2PushActive(n + 1); err != nil {
			return 0, err
		}
		if activeIdx, err = c.v2ActiveLen(); err != nil {
			return 0, err
		}
	}
	r := v2Record{
		publicKey: a.publicKey, validator: a.validator, ingress: a.ingress, egress: a.egress, fee: a.fee,
		index: n, activeIdx: activeIdx, added: a.added, deactivated: a.deactivated,
	}
	if err := c.v2PushRecord(r); err != nil {
		return 0, err
	}
	if err := c.v2Store(c.v2KeyIndexSlot(a.publicKey), u256.FromUint64(n+1)); err != nil {
		return 0, err
	}
	return n, c.v2Store(c.v2AddrIndexSlot(a.validator), u256.FromUint64(n+1))
}

func (c *Context) v2Height() uint64 { return c.Block.Number }

func v2Indexed(v uint64) Hash { return wordOfU64(v) }
