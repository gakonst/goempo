package precompiles_test

import (
	"encoding/hex"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

const vcv2T14Time = 1_796_742_000

func vcv2FindCase(name string) vcv2Case {
	for _, p := range []string{"testdata/vcv2/t13_mut_49400000_a.json", "testdata/vcv2/t13_mut_49400000_b.json", "testdata/vcv2/t13_views_49400000.json"} {
		for _, c := range loadVCV2Cases(p) {
			if c.Name == name {
				return c
			}
		}
	}
	assert.Ok(false, "vcv2 case "+name)
	return vcv2Case{}
}

type vcv2Run struct {
	fx    vcv2Case
	time  uint64
	input []byte
}

func vcv2Context(r vcv2Run) *precompiles.Context {
	st := memstate.New()
	for _, a := range slices.Sorted(maps.Keys(r.fx.Pre)) {
		st.SetCode(mustAddr(a), []byte{0xef})
		for _, k := range slices.Sorted(maps.Keys(r.fx.Pre[a])) {
			st.SetStorage(mustAddr(a), mustHash(k), mustWord(r.fx.Pre[a][k]))
		}
	}
	st.FinalizeTx()
	c := precompiles.NewContext(st, hardfork.At(hardfork.MainnetChainID, r.time), r.fx.Gas)
	c.Block.Number = r.fx.Block
	c.Block.Time = r.time
	c.ChainID = u256.FromUint64(uint64(hardfork.MainnetChainID))
	c.Caller = mustAddr(r.fx.Caller)
	c.Static = r.fx.Static
	return c
}

func vcv2Revert(r vcv2Run) []byte {
	c := vcv2Context(r)
	_, err := precompiles.Call(c, precompiles.ValidatorConfigV2Address, r.input)
	var rev *precompiles.Revert
	assert.Ok(errors.As(err, &rev), "expected revert")
	return rev.Data
}

func abiWord(v uint64) []byte {
	w := make([]byte, 32)
	for i := 0; i < 8; i++ {
		w[31-i] = byte(v >> (8 * uint(i)))
	}
	return w
}

func abiStrings(head [][]byte, strs []string) []byte {
	out := []byte{}
	for _, h := range head {
		out = append(out, h...)
	}
	off := uint64(32 * (len(head) + len(strs)))
	tails := []byte{}
	for _, s := range strs {
		out = append(out, abiWord(off+uint64(len(tails)))...)
		padded := make([]byte, (len(s)+31)/32*32)
		copy(padded, s)
		tails = append(append(tails, abiWord(uint64(len(s)))...), padded...)
	}
	return append(out, tails...)
}

func invalidIPData(inputAndReason []string) []byte {
	return append(selectorOf("InvalidIp(string,string)"), abiStrings(nil, inputAndReason)...)
}

func setIPInput(index uint64, endpoints []string) []byte {
	return append(selectorOf("setIpAddresses(uint64,string,string)"), abiStrings([][]byte{abiWord(index)}, endpoints)...)
}

func TestValidatorConfigV2T14RejectsMappedIngress(t *testing.T) {
	fx := vcv2FindCase("setIpMapped")
	got := vcv2Revert(vcv2Run{fx: fx, time: vcv2T14Time, input: mustBytes(fx.Input)})
	want := invalidIPData([]string{"[::ffff:1.2.3.4]:80", "IPv4-mapped IPv6 address; use the IPv4 form"})
	assert.Equal(hex.EncodeToString(got), hex.EncodeToString(want), "mapped ingress at T14")
}

func TestValidatorConfigV2T14RejectsMappedEgress(t *testing.T) {
	fx := vcv2FindCase("setIpV4")
	index := mustWord("0x" + strings.TrimPrefix(fx.Input, "0x")[8:72]).Uint64()
	in := setIPInput(index, []string{"10.1.2.3:30303", "::ffff:10.1.2.3"})
	got := vcv2Revert(vcv2Run{fx: fx, time: vcv2T14Time, input: in})
	want := invalidIPData([]string{"::ffff:10.1.2.3", "IPv4-mapped IPv6 address; use the IPv4 form"})
	assert.Equal(hex.EncodeToString(got), hex.EncodeToString(want), "mapped egress at T14")
}

func TestValidatorConfigV2T14RejectsLongEndpoint(t *testing.T) {
	fx := vcv2FindCase("setIpV4")
	index := mustWord("0x" + strings.TrimPrefix(fx.Input, "0x")[8:72]).Uint64()
	long := "10.1.2.3:" + strings.Repeat("0", 250) + "1"
	got := vcv2Revert(vcv2Run{fx: fx, time: vcv2T14Time, input: setIPInput(index, []string{long, "10.1.2.3"})})
	want := invalidIPData([]string{long, "IP address exceeds 255 bytes"})
	assert.Equal(hex.EncodeToString(got), hex.EncodeToString(want), "long ingress at T14")
}

func TestValidatorConfigV2T14AcceptsPlainUpdate(t *testing.T) {
	fx := vcv2FindCase("setIpV4")
	c := vcv2Context(vcv2Run{fx: fx, time: vcv2T14Time})
	_, err := precompiles.Call(c, precompiles.ValidatorConfigV2Address, mustBytes(fx.Input))
	assert.NoError(err, "plain setIpAddresses at T14")
}

func vcv2MergedViews(names []string) vcv2Case {
	fx := vcv2FindCase(names[0])
	merged := map[string]map[string]string{}
	for _, n := range names {
		pre := vcv2FindCase(n).Pre
		for _, a := range slices.Sorted(maps.Keys(pre)) {
			if merged[a] == nil {
				merged[a] = map[string]string{}
			}
			maps.Copy(merged[a], pre[a])
		}
	}
	fx.Pre = merged
	return fx
}

func TestValidatorConfigV2ActiveValidatorsExport(t *testing.T) {
	fx := vcv2MergedViews([]string{"getActiveValidators", "isInitialized", "getInitializedAtHeight", "validatorByPublicKeyFirst", "getNextNetworkIdentityRotationEpoch"})
	c := vcv2Context(vcv2Run{fx: fx, time: fx.Timestamp})
	vals, err := precompiles.ActiveValidatorsV2(c)
	assert.NoError(err, "active validators")
	out := mustBytes(fx.Output)
	n := u256.FromBytes32([32]byte(out[32:64])).Uint64()
	assert.Equal(uint64(len(vals)), n, "active count matches mainnet")
	assert.Ok(n > 0, "mainnet has active validators")
	first := 64 + int(u256.FromBytes32([32]byte(out[64:96])).Uint64())
	assert.Equal(vals[0].PublicKey, precompiles.Hash(out[first:first+32]), "first active key matches mainnet")
	for _, v := range vals {
		assert.Equal(v.DeactivatedAtHeight, uint64(0), "active validators are not deactivated")
		assert.Ok(v.IsDkgPlayer(), "mainnet validators are DKG players")
	}
	fee, err := precompiles.ProposerFeeRecipientV2(c, precompiles.ProposerQuery{BlockNumber: fx.Block, PublicKey: vals[0].PublicKey})
	assert.NoError(err, "proposer fee recipient")
	assert.Ok(fee.Found, "proposer found")
	assert.Equal(fee.Address, vals[0].FeeRecipient, "proposer fee recipient")
	missing, err := precompiles.ProposerFeeRecipientV2(c, precompiles.ProposerQuery{BlockNumber: fx.Block, PublicKey: precompiles.Hash{1}})
	assert.NoError(err, "unknown proposer")
	assert.Ok(!missing.Found, "unknown proposer not found")
	dkg, err := precompiles.DkgCeremonyConfigV2(c, fx.Block)
	assert.NoError(err, "dkg config")
	assert.Ok(dkg.Found, "dkg config after init")
	assert.Equal(len(dkg.ActiveValidators), len(vals), "dkg players")
	early, err := precompiles.DkgCeremonyConfigV2(c, 1)
	assert.NoError(err, "dkg config before init")
	assert.Ok(!early.Found, "dkg config not found before init height")
}
