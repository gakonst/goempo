package precompiles_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/kapetan-io/tiger/assert"
)

type commitState struct {
	*memstate.State
	commits map[primitives.Address]primitives.Hash
}

func (s *commitState) ConfigCommitment(a primitives.Address) primitives.Hash { return s.commits[a] }

func (s *commitState) SetConfigCommitment(a primitives.Address, h primitives.Hash) { s.commits[a] = h }

type caOwner struct {
	owner  primitives.Address
	weight uint64
}

type caCfg struct {
	salt      primitives.Hash
	version   uint64
	threshold uint64
	owners    []caOwner
}

type caEnv struct {
	st   *commitState
	fork hardfork.Fork
}

var (
	caOwnerAddr = repeatAddr(0x22)
	caEOA       = repeatAddr(0x33)
)

func repeatAddr(b byte) primitives.Address {
	var a primitives.Address
	for i := range a {
		a[i] = b
	}
	return a
}

func newCAEnv() caEnv {
	return caEnv{st: &commitState{State: memstate.New(), commits: map[primitives.Address]primitives.Hash{}}, fork: hardfork.T14}
}

func (e caEnv) ctx(sender primitives.Address) *precompiles.Context {
	c := precompiles.NewContext(e.st, e.fork, 30_000_000)
	c.Caller = sender
	return c
}

func (e caEnv) seed(a primitives.Address, h primitives.Hash) {
	e.st.commits[a] = h
	e.st.SetCode(a, precompiles.ConfigurableAccountCode)
}

func (e caEnv) authority(a precompiles.ConfigurableAuthority) {
	precompiles.SetConfigurableAuthority(e.ctx(primitives.Address{}), a)
}

func ownerTail(owners []caOwner) []byte {
	out := amount(uint64(len(owners)))
	for _, o := range owners {
		out = append(append(out, word(o.owner)...), amount(o.weight)...)
	}
	return out
}

func deriveInput(salt primitives.Hash, threshold uint64, owners []caOwner) []byte {
	args := append(append(append([]byte{}, salt[:]...), amount(threshold)...), amount(96)...)
	return calldata("deriveAccount(bytes32,uint8,(address,uint8)[])", append(args, ownerTail(owners)...))
}

func upgradeInput(threshold uint64, owners []caOwner) []byte {
	args := append(amount(threshold), amount(64)...)
	return calldata("upgradeAccount(uint8,(address,uint8)[])", append(args, ownerTail(owners)...))
}

func updateInput(cur caCfg, threshold uint64, owners []caOwner) []byte {
	tuple := append(append(append(append([]byte{}, cur.salt[:]...), amount(cur.version)...), amount(cur.threshold)...), amount(128)...)
	tuple = append(tuple, ownerTail(cur.owners)...)
	head := append(append(amount(96), amount(threshold)...), amount(uint64(96+len(tuple)))...)
	return calldata("updateConfig((bytes32,uint64,uint8,(address,uint8)[]),uint8,(address,uint8)[])", append(append(head, tuple...), ownerTail(owners)...))
}

func ownerSet(c caCfg) []byte {
	out := []byte{byte(c.threshold), byte(len(c.owners))}
	for _, o := range c.owners {
		out = append(append(out, o.owner[:]...), byte(o.weight))
	}
	return out
}

func refDerive(c caCfg) primitives.Address {
	pre := append(append([]byte("tempo:configurable-account:account"), c.salt[:]...), ownerSet(c)...)
	h := keccak.Sum256(pre)
	return primitives.Address(h[12:])
}

func refCommitment(c caCfg) primitives.Hash {
	pre := append([]byte("tempo:configurable-account:config"), c.salt[:]...)
	var v [8]byte
	for i := range v {
		v[i] = byte(c.version >> (56 - 8*i))
	}
	pre = append(append(pre, v[:]...), ownerSet(c)...)
	return primitives.Hash(keccak.Sum256(pre))
}

type caResult struct {
	out []byte
	err error
	gas uint64
}

func (e caEnv) call(sender primitives.Address, input []byte) caResult {
	c := e.ctx(sender)
	out, err := precompiles.Call(c, precompiles.ConfigurableAccountAddress, input)
	return caResult{out: out, err: err, gas: c.GasUsed()}
}

func expectCARevert(t *testing.T, r caResult, sig string) {
	t.Helper()
	assert.Equal(hex.EncodeToString(revertSelector(r.err)), hex.EncodeToString(selectorOf(sig)), sig)
}

func expectOK(t *testing.T, r caResult) []byte {
	t.Helper()
	assert.NoError(r.err, "call succeeds")
	return r.out
}

func (e caEnv) commitment(a primitives.Address) primitives.Hash {
	r := e.call(primitives.Address{}, calldata("getConfigCommitment(address)", word(a)))
	assert.NoError(r.err, "getConfigCommitment")
	return primitives.Hash(r.out)
}

func (e caEnv) derive(c caCfg) primitives.Address {
	r := e.call(primitives.Address{}, deriveInput(c.salt, c.threshold, c.owners))
	assert.NoError(r.err, "deriveAccount")
	return primitives.Address(r.out[12:32])
}

func TestConfigurableAccountNamespace(t *testing.T) {
	type nsCase struct {
		addr primitives.Address
		want func(hardfork.Fork) bool
	}
	never := func(hardfork.Fork) bool { return false }
	cases := []nsCase{
		{primitives.Address{}, never},
		{primitives.Address{19: 1}, never},
		{primitives.Address{18: 1}, never},
		{precompiles.ConfigurableAccountAddress, func(f hardfork.Fork) bool { return f < hardfork.T14 }},
		{primitives.Address{0: 0x20, 1: 0xc0, 19: 1}, never},
		{primitives.Address{4: 0xfd, 5: 0xfd, 6: 0xfd, 7: 0xfd, 8: 0xfd, 9: 0xfd, 10: 0xfd, 11: 0xfd, 12: 0xfd, 13: 0xfd}, never},
		{primitives.Address{0: 0x5a, 1: 0xd0, 19: 1}, never},
		{repeatAddr(0x71), func(hardfork.Fork) bool { return true }},
	}
	for _, f := range []hardfork.Fork{hardfork.T12, hardfork.T13, hardfork.T14} {
		for _, tc := range cases {
			assert.Equal(precompiles.ValidConfigurableAccount(tc.addr, f), tc.want(f), hex.EncodeToString(tc.addr[:]))
		}
	}
}

func TestConfigurableAccountDerive(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{repeatAddr(0x11), 1}, {caOwnerAddr, 2}}
	cfg := caCfg{salt: primitives.Hash{0: 5, 31: 5}, threshold: 2, owners: owners}
	assert.Equal(e.derive(cfg), refDerive(cfg), "derivation matches the spec preimage")
	other := cfg
	other.threshold = 1
	assert.Ok(e.derive(other) != e.derive(cfg), "threshold changes the account")
	for _, list := range [][]caOwner{{{caOwnerAddr, 1}}, {{repeatAddr(0x11), 1}, {caOwnerAddr, 1}}} {
		ok := e.call(primitives.Address{}, deriveInput(primitives.Hash{}, 1, list))
		bad := e.call(primitives.Address{}, deriveInput(primitives.Hash{}, 0, list))
		expectOK(t, ok)
		expectCARevert(t, bad, "InvalidThreshold()")
		preimage := 34 + 32 + 2 + 21*len(list)
		assert.Equal(ok.gas-bad.gas, uint64(30+6*((preimage+31)/32)), "derivation keccak gas on top of input cost")
	}
}

type precedenceCase struct {
	threshold uint64
	owners    []caOwner
	want      string
}

func TestConfigurableAccountErrorPrecedence(t *testing.T) {
	zeros := make([]caOwner, 49)
	cases := []precedenceCase{
		{0, nil, "InvalidConfigurableAccountOwner()"},
		{0, zeros, "TooManyOwners()"},
		{0, []caOwner{{}}, "InvalidThreshold()"},
		{1, []caOwner{{repeatAddr(0x11), 0}, {primitives.Address{}, 1}}, "InvalidConfigurableAccountOwner()"},
		{1, []caOwner{{repeatAddr(0x11), 1}, {repeatAddr(0x11), 1}, {repeatAddr(0x22), 0}}, "InvalidWeight()"},
		{1, []caOwner{{repeatAddr(0x22), 1}, {repeatAddr(0x11), 1}, {repeatAddr(0x22), 1}}, "DuplicateOwner()"},
		{1, []caOwner{{repeatAddr(0x22), 128}, {repeatAddr(0x11), 128}}, "InvalidOwnerOrder()"},
	}
	for _, tc := range cases {
		e := newCAEnv()
		expectCARevert(t, e.call(primitives.Address{}, deriveInput(primitives.Hash{}, tc.threshold, tc.owners)), tc.want)
	}
}

func TestConfigurableAccountRotation(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{caOwnerAddr, 1}}
	cur := caCfg{threshold: 1, owners: owners}
	account := e.derive(cur)
	assert.Equal(e.commitment(account), primitives.Hash{}, "unregistered")
	expectCARevert(t, e.call(account, updateInput(cur, 1, owners)), "UnauthorizedConfigurableAccountCaller()")
	e.authority(precompiles.ConfigurableAuthority{Origin: account, Direct: account, Eligibility: precompiles.UpgradeHasAuthorizationList})
	expectCARevert(t, e.call(account, updateInput(cur, 1, owners)), "InvalidConfig()")
	e.seed(account, refCommitment(cur))
	expectOK(t, e.call(account, updateInput(cur, 1, owners)))
	expectCARevert(t, e.call(account, updateInput(cur, 1, owners)), "InvalidConfig()")
	cur.version = 1
	expectOK(t, e.call(account, updateInput(cur, 1, owners)))
	assert.Equal(e.commitment(account), refCommitment(caCfg{version: 2, threshold: 1, owners: owners}), "version two committed")
	logs := e.st.Logs()
	assert.Equal(len(logs), 2, "two config updates")
	for i, l := range logs {
		assert.Equal(l.Topics[1], primitives.Hash(append(make([]byte, 12), account[:]...)), "indexed account")
		assert.Equal(l.Data[63], byte(i+1), "version")
		assert.Equal(len(l.Data), 32*7, "salt, version, threshold, offset, one owner")
	}
}

func TestConfigurableAccountUpdateAuthority(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{caOwnerAddr, 1}}
	cur := caCfg{threshold: 1, owners: owners}
	account := e.derive(cur)
	e.seed(account, refCommitment(cur))
	e.authority(precompiles.ConfigurableAuthority{Eligibility: precompiles.UpgradePrimitiveRoot})
	expectCARevert(t, e.call(primitives.Address{}, updateInput(cur, 1, owners)), "UnauthorizedConfigurableAccountCaller()")
	e.authority(precompiles.ConfigurableAuthority{Origin: account, Direct: caEOA, Eligibility: precompiles.UpgradePrimitiveRoot})
	expectCARevert(t, e.call(account, updateInput(cur, 1, owners)), "UnauthorizedConfigurableAccountCaller()")
	e.authority(precompiles.ConfigurableAuthority{Origin: caEOA, Direct: account, Eligibility: precompiles.UpgradeHasAuthorizationList})
	expectOK(t, e.call(account, updateInput(cur, 1, owners)))
}

func TestConfigurableAccountUpdateRejectsInvalidConfigs(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{caOwnerAddr, 1}}
	account := e.derive(caCfg{threshold: 1, owners: owners})
	e.authority(precompiles.ConfigurableAuthority{Origin: account, Direct: account, Eligibility: precompiles.UpgradeHasAuthorizationList})
	initial := refCommitment(caCfg{threshold: 1, owners: owners})
	e.seed(account, initial)
	expectCARevert(t, e.call(account, updateInput(caCfg{threshold: 1, owners: []caOwner{{account, 1}}}, 1, owners)), "InvalidConfig()")
	many := make([]caOwner, 49)
	for i := range many {
		many[i] = caOwner{primitives.Address{19: byte(i + 1)}, 1}
	}
	expectCARevert(t, e.call(account, updateInput(caCfg{threshold: 1, owners: many}, 1, owners)), "InvalidConfig()")
	cur := caCfg{threshold: 1, owners: owners}
	expectCARevert(t, e.call(account, updateInput(cur, 0, owners)), "InvalidThreshold()")
	expectCARevert(t, e.call(account, updateInput(cur, 1, nil)), "InvalidConfigurableAccountOwner()")
	assert.Equal(e.commitment(account), initial, "unchanged")
	last := caCfg{version: ^uint64(0), threshold: 1, owners: owners}
	e.st.commits[account] = refCommitment(last)
	expectCARevert(t, e.call(account, updateInput(last, 1, owners)), "InvalidConfig()")
}

func TestConfigurableAccountStaticCalls(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{caOwnerAddr, 1}}
	cur := caCfg{threshold: 1, owners: owners}
	account := e.derive(cur)
	for _, in := range [][]byte{updateInput(cur, 1, owners), upgradeInput(1, owners)} {
		c := e.ctx(account)
		c.Static = true
		out, err := precompiles.Call(c, precompiles.ConfigurableAccountAddress, in)
		assert.Ok(err != nil, "static call halts")
		assert.Equal(len(revertSelector(err)), 0, "halt, not a revert")
		assert.Equal(len(out), 0, "no output")
	}
	assert.Equal(e.commitment(account), primitives.Hash{}, "nothing written")
}

type upgradeCase struct {
	sender primitives.Address
	elig   precompiles.UpgradeEligibility
	want   string
}

func TestConfigurableAccountUpgradeChecksInOrder(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{caOwnerAddr, 1}}
	withCode := repeatAddr(0x44)
	e.seed(withCode, primitives.Hash{31: 9})
	e.st.SetCode(withCode, []byte{0x00})
	configured := repeatAddr(0x55)
	e.seed(configured, primitives.Hash{31: 9})
	expectCARevert(t, e.call(caEOA, upgradeInput(1, owners)), "UpgradeMustBeDirectCall()")
	cases := []upgradeCase{
		{withCode, precompiles.UpgradeHasAuthorizationList, "UpgradeMustBeDirectCall()"},
		{withCode, precompiles.UpgradeNotPrimitiveRoot, "PrimitiveRootRequired()"},
		{withCode, precompiles.UpgradePrimitiveRoot, "AccountHasCode()"},
		{configured, precompiles.UpgradePrimitiveRoot, "AccountAlreadyConfigurable()"},
	}
	for _, tc := range cases {
		e.authority(precompiles.ConfigurableAuthority{Origin: tc.sender, Direct: tc.sender, Eligibility: tc.elig})
		expectCARevert(t, e.call(tc.sender, upgradeInput(1, owners)), tc.want)
	}
	for _, elig := range []precompiles.UpgradeEligibility{precompiles.UpgradeNotPrimitiveRoot, precompiles.UpgradePrimitiveRoot} {
		e.authority(precompiles.ConfigurableAuthority{Origin: caOwnerAddr, Direct: caEOA, Eligibility: elig})
		expectCARevert(t, e.call(caEOA, upgradeInput(1, owners)), "UpgradeMustBeDirectCall()")
	}
	e.authority(precompiles.ConfigurableAuthority{Origin: caEOA, Direct: caEOA, Eligibility: precompiles.UpgradePrimitiveRoot})
	bad := []caCfg{
		{threshold: 0, owners: owners},
		{threshold: 1},
		{threshold: 2, owners: owners},
		{threshold: 1, owners: []caOwner{{caOwnerAddr, 1}, {repeatAddr(0x11), 1}}},
	}
	for _, b := range bad {
		expectCARevert(t, e.call(caEOA, upgradeInput(b.threshold, b.owners)), "InvalidConfig()")
	}
	assert.Equal(e.commitment(caEOA), primitives.Hash{}, "not upgraded")
}

func TestConfigurableAccountUpgradeInstallsCommitment(t *testing.T) {
	e := newCAEnv()
	owners := []caOwner{{caOwnerAddr, 1}, {caEOA, 1}}
	salt := primitives.Hash(keccak.Sum256(append([]byte("tempo:configurable-account:upgrade"), caEOA[:]...)))
	want := refCommitment(caCfg{salt: salt, version: 1, threshold: 2, owners: owners})
	e.authority(precompiles.ConfigurableAuthority{Origin: caEOA, Direct: caEOA, Eligibility: precompiles.UpgradePrimitiveRoot})
	r := e.call(caEOA, upgradeInput(2, owners))
	assert.Equal(primitives.Hash(expectOK(t, r)), want, "returns commitment")
	assert.Equal(e.commitment(caEOA), want, "stored")
	assert.Ok(bytes.Equal(e.st.Code(caEOA), precompiles.ConfigurableAccountCode), "marker code installed")
	expectCARevert(t, e.call(caEOA, upgradeInput(2, owners)), "AccountAlreadyConfigurable()")
	cur := caCfg{salt: salt, version: 1, threshold: 2, owners: owners}
	expectOK(t, e.call(caEOA, updateInput(cur, 1, owners)))
	assert.Equal(e.commitment(caEOA), refCommitment(caCfg{salt: salt, version: 2, threshold: 1, owners: owners}), "rotated")
	logs := e.st.Logs()
	assert.Equal(len(logs), 2, "upgrade and update events")
	assert.Equal(logs[0].Topics[2], want, "indexed commitment")
	assert.Equal(primitives.Hash(logs[0].Data[:32]), salt, "upgrade salt")
}

func TestConfigurableAccountRetiredRoot(t *testing.T) {
	e := newCAEnv()
	assert.Equal(e.commitment(caEOA), primitives.Hash{}, "no commitment")
	e.seed(caEOA, primitives.Hash{31: 1})
	assert.Equal(e.commitment(caEOA), primitives.Hash{31: 1}, "committed")
	e.fork = hardfork.T13
	r := e.call(primitives.Address{}, calldata("getConfigCommitment(address)", word(caEOA)))
	assert.Ok(r.err != nil, "precompile inactive before T14")
}
