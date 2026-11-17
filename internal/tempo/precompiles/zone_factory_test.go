package precompiles_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/kapetan-io/tiger/assert"
)

const newAccountGas = 250_000

var zoneFixtureNewSender = map[string]uint64{
	"t10_2_zf_89677d9e_35816964.json": newAccountGas,
	"t10_2_zf_89677d9e_31704623.json": newAccountGas,
}

func intrinsicGas(input []byte) uint64 {
	gas := uint64(21_000)
	for _, b := range input {
		if b == 0 {
			gas += 4
			continue
		}
		gas += 16
	}
	return gas
}

type gasPair struct {
	got  uint64
	want uint64
}

func replayTopLevelGas(t *testing.T, path string) gasPair {
	raw, err := os.ReadFile(path)
	assert.NoError(err, "read fixture")
	var fx replayFixture
	assert.NoError(json.Unmarshal(raw, &fx), "parse fixture")
	run := replayRun{fx: fx, st: loadState(fx), fork: hardfork.At(hardfork.ChainID(fx.ChainID), fx.Timestamp)}
	run.st.WarmAddress(mustAddr(fx.Sender))
	run.st.WarmAddress(mustAddr(fx.Coinbase))
	run.st.WarmSlot(mustAddr(fx.FeeToken), primitives.Hash{31: 4})
	h := run.context(handlerGas)
	precompiles.SetTxOrigin(h, mustAddr(fx.Sender))
	collection := precompiles.FeeCollection{FeePayer: mustAddr(fx.FeePayer), Token: mustAddr(fx.FeeToken), MaxAmount: mustWord(fx.MaxFee), Beneficiary: mustAddr(fx.Coinbase)}
	_, err = precompiles.CollectFeePreTx(h, collection)
	assert.NoError(err, "collect fee pre tx")
	if run.fork >= hardfork.T7 {
		precompiles.MarkFeeSlotsNonCreditable(h, precompiles.FeeSlots{FeePayer: mustAddr(fx.FeePayer), FeeToken: mustAddr(fx.FeeToken)})
	}
	call := fx.Calls[0]
	input := mustBytes(call.Input)
	c := run.context(call.Gas)
	c.Env = h.Env
	run.st.WarmAddress(mustAddr(call.To))
	_, err = precompiles.Call(c, mustAddr(call.To), input)
	assert.Equal(err != nil, call.Reverted, "revert status")
	return gasPair{got: c.GasUsed() + intrinsicGas(input) + zoneFixtureNewSender[filepath.Base(path)], want: call.GasUsed}
}

func zoneFixtures() []string {
	mainnet, err := filepath.Glob("testdata/mainnet/t1*_2_zf_*.json")
	assert.NoError(err, "glob mainnet")
	moderato, err := filepath.Glob("testdata/moderato/t1*_2_zf_*.json")
	assert.NoError(err, "glob moderato")
	return append(mainnet, moderato...)
}

func TestReplayModerato(t *testing.T) {
	paths, err := filepath.Glob("testdata/moderato/*.json")
	assert.NoError(err, "glob")
	assert.Ok(len(paths) > 0, "fixtures present")
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) { replay(t, p) })
	}
}

func TestZoneFactoryTopLevelGas(t *testing.T) {
	paths := zoneFixtures()
	assert.Ok(len(paths) > 0, "fixtures present")
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			g := replayTopLevelGas(t, p)
			assert.Equal(g.got, g.want, "top-level gas")
		})
	}
}

type zoneParams struct {
	token     primitives.Address
	access    uint64
	gateway   uint64
	allowed   []primitives.Address
	gateways  []primitives.Address
	admin     primitives.Address
	sequencer []primitives.Address
	threshold uint64
	rpcURL    string
}

func addrListTail(list []primitives.Address) []byte {
	out := amount(uint64(len(list)))
	for _, a := range list {
		out = append(out, word(a)...)
	}
	return out
}

func createZoneInput(p zoneParams) []byte {
	head := [][]byte{word(p.token), amount(p.access), amount(p.gateway), nil, nil, word(p.admin), nil, amount(p.threshold), nil}
	tails := map[int][]byte{3: addrListTail(p.allowed), 4: addrListTail(p.gateways), 6: addrListTail(p.sequencer)}
	url := amount(uint64(len(p.rpcURL)))
	padded := make([]byte, (len(p.rpcURL)+31)/32*32)
	copy(padded, p.rpcURL)
	tails[8] = append(url, padded...)
	var tail []byte
	for i := range head {
		if t, ok := tails[i]; ok {
			head[i] = amount(uint64(len(head)*32 + len(tail)))
			tail = append(tail, t...)
		}
	}
	body := []byte{}
	for _, h := range head {
		body = append(body, h...)
	}
	return calldata("createZone((address,bool,bool,address[],address[],address,address[],uint8,string))", append(amount(32), append(body, tail...)...))
}

const zoneMainnetFixture = "testdata/mainnet/t10_2_zf_89677d9e_35816964.json"

func zoneRun(fork hardfork.Fork) replayRun {
	raw, err := os.ReadFile(zoneMainnetFixture)
	assert.NoError(err, "read fixture")
	var fx replayFixture
	assert.NoError(json.Unmarshal(raw, &fx), "parse fixture")
	return replayRun{fx: fx, st: loadState(fx), fork: fork}
}

func zoneOwner(r replayRun) primitives.Address { return mustAddr(r.fx.Sender) }

func baseZoneParams() zoneParams {
	return zoneParams{
		token:     primitives.Address{0: 0x20, 1: 0xc0},
		admin:     primitives.Address{19: 0xad},
		sequencer: []primitives.Address{{19: 0x51}, {19: 0x52}},
		threshold: 1,
		rpcURL:    "https://zone.example",
	}
}

func revertSelector(err error) []byte {
	var rv *precompiles.Revert
	if !errors.As(err, &rv) {
		return nil
	}
	return rv.Data
}

func TestZoneFactoryViewsMatchMainnet(t *testing.T) {
	r := zoneRun(hardfork.T10)
	c := r.context(handlerGas)
	_, err := precompiles.Call(c, precompiles.ZoneFactoryAddress, mustBytes(r.fx.Calls[0].Input))
	assert.NoError(err, "createZone")
	out, err := precompiles.Call(r.context(handlerGas), precompiles.ZoneFactoryAddress, calldata("zones(uint32)", amount(1)))
	assert.NoError(err, "zones(1)")
	want := "0x0000000000000000000000000000000000000000000000000000000000000020" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000005ad0000000000000000000000000000000000001" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"000000000000000000000000a4144948c68458d7972e3115def7f06c7d43bc88" +
		"0000000000000000000000000000000000000000000000000000000000000120" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"0000000000000000000000005a56000000000000000000000000000000000000" +
		"00000000000000000000000000000000000000000000000000000000000001a0" +
		"0000000000000000000000000000000000000000000000000000000000000003" +
		"000000000000000000000000f0d4945f4d9996471bc4d2e7d0cff7a2f9eeb5a4" +
		"0000000000000000000000005b8bd9cb404e72192954d7c0240525c8cb6788e5" +
		"000000000000000000000000ea45a23d303f43491f5db087dca0bef2832d3f2a" +
		"0000000000000000000000000000000000000000000000000000000000000000"
	assert.Equal("0x"+hex.EncodeToString(out), want, "zones(1) matches mainnet eth_call")
	out, err = precompiles.Call(r.context(handlerGas), precompiles.ZoneFactoryAddress, calldata("nextZoneId()"))
	assert.NoError(err, "nextZoneId")
	assert.Equal(out[31], byte(2), "next zone id")
	portal := precompiles.ZonePortalAddress(1)
	out, err = precompiles.Call(r.context(handlerGas), precompiles.ZoneFactoryAddress, calldata("isZonePortal(address)", word(portal)))
	assert.NoError(err, "isZonePortal(1)")
	assert.Equal(out[31], byte(1), "portal 1 registered")
	out, err = precompiles.Call(r.context(handlerGas), precompiles.ZoneFactoryAddress, calldata("isZonePortal(address)", word(precompiles.ZonePortalAddress(2))))
	assert.NoError(err, "isZonePortal(2)")
	assert.Equal(out[31], byte(0), "portal 2 not yet registered")
	code := r.st.Code(portal)
	assert.Equal(len(code), 45, "portal proxy runtime installed")
}

func TestZoneFactoryNotOwnerReverts(t *testing.T) {
	r := zoneRun(hardfork.T10)
	c := r.context(handlerGas)
	c.Caller = alice
	_, err := precompiles.Call(c, precompiles.ZoneFactoryAddress, calldata("transferOwnership(address)", word(bob)))
	assert.Equal(hex.EncodeToString(revertSelector(err)), "30cd7471", "transferOwnership NotOwner")
	c = r.context(handlerGas)
	c.Caller = alice
	_, err = precompiles.Call(c, precompiles.ZoneFactoryAddress, createZoneInput(baseZoneParams()))
	assert.Equal(hex.EncodeToString(revertSelector(err)), "30cd7471", "createZone NotOwner")
}

type zoneRevertCase struct {
	name   string
	fork   hardfork.Fork
	mutate func(*zoneParams)
	want   string
}

func TestZoneFactoryCreateZoneValidation(t *testing.T) {
	messenger := primitives.Address{0: 0x5a, 1: 0x4d}
	cases := []zoneRevertCase{
		{"non tip20 token", hardfork.T10, func(p *zoneParams) { p.token = alice }, "InvalidToken()"},
		{"zero admin", hardfork.T10, func(p *zoneParams) { p.admin = primitives.Address{} }, "InvalidAdmin()"},
		{"zero threshold", hardfork.T10, func(p *zoneParams) { p.threshold = 0 }, "InvalidSequencerSet()"},
		{"threshold above set", hardfork.T10, func(p *zoneParams) { p.threshold = 3 }, "InvalidSequencerSet()"},
		{"no sequencers", hardfork.T10, func(p *zoneParams) { p.sequencer = nil }, "InvalidSequencerSet()"},
		{"zero sequencer", hardfork.T10, func(p *zoneParams) { p.sequencer[1] = primitives.Address{} }, "InvalidSequencerSet()"},
		{"duplicate sequencer", hardfork.T10, func(p *zoneParams) { p.sequencer[1] = p.sequencer[0] }, "InvalidSequencerSet()"},
		{"messenger allowed", hardfork.T10, func(p *zoneParams) { p.allowed = []primitives.Address{messenger} }, "InvalidClosedLoopConfig()"},
		{"gateway also allowed", hardfork.T10, func(p *zoneParams) { p.allowed, p.gateways = []primitives.Address{bob}, []primitives.Address{bob} }, "InvalidClosedLoopConfig()"},
		{"sequencer allowed", hardfork.T10, func(p *zoneParams) { p.allowed = []primitives.Address{p.sequencer[0]} }, "InvalidClosedLoopConfig()"},
		{"duplicate allowed after T11", hardfork.T11, func(p *zoneParams) { p.allowed = []primitives.Address{bob, bob} }, "InvalidClosedLoopConfig()"},
		{"duplicate sequencer after T11", hardfork.T11, func(p *zoneParams) { p.sequencer[1] = p.sequencer[0] }, "InvalidClosedLoopConfig()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := zoneRun(tc.fork)
			c := r.context(handlerGas)
			c.Caller = zoneOwner(r)
			p := baseZoneParams()
			tc.mutate(&p)
			_, err := precompiles.Call(c, precompiles.ZoneFactoryAddress, createZoneInput(p))
			assert.Equal(hex.EncodeToString(revertSelector(err)), hex.EncodeToString(selectorOf(tc.want)), tc.name)
		})
	}
}

func TestZoneFactoryDuplicateAllowedBeforeT11(t *testing.T) {
	r := zoneRun(hardfork.T10)
	c := r.context(handlerGas)
	c.Caller = zoneOwner(r)
	p := baseZoneParams()
	p.allowed = []primitives.Address{bob, bob}
	p.gateways = []primitives.Address{alice}
	out, err := precompiles.Call(c, precompiles.ZoneFactoryAddress, createZoneInput(p))
	assert.NoError(err, "duplicate allowed accepted before T11")
	portal := precompiles.ZonePortalAddress(1)
	assert.Equal(primitives.Address(out[44:64]), portal, "returns portal")
	logs := r.st.Logs()
	assert.Equal(len(logs), 8, "portal setup, role, token and zone created logs")
	assert.Equal(logs[4].Data[31], byte(0), "first allowed role had no previous role")
	assert.Equal(logs[5].Data[31], byte(2), "duplicate allowed reports previous account role")
}
