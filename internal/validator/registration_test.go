package validator_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/validator"
	"github.com/kapetan-io/tiger/assert"
)

const (
	word          = 32
	mainnetCalls  = 14
	selectorAdd   = "6dcf616a"
	selectorRot   = "82f20dbe"
	mainnetChain  = 4217
	callFieldsMin = 4
	callFieldsMax = 4
	fixtureLines  = 64
)

const _ = uint(fixtureLines - mainnetCalls)

const _ = uint(callFieldsMax - callFieldsMin)

func wordAt(b []byte, i int) []byte { return b[4+i*word : 4+(i+1)*word] }

func dynAt(b []byte, i int) []byte {
	off := int(binary.BigEndian.Uint64(wordAt(b, i)[word-8:]))
	n := int(binary.BigEndian.Uint64(b[4+off+word-8 : 4+off+word]))
	return b[4+off+word : 4+off+word+n]
}

func TestMainnetRegistrationCalls(_ *testing.T) {
	raw, err := os.ReadFile("testdata/vcv2_mainnet_calls.txt")
	assert.NoError(err, "read fixture")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	assert.Ok(len(lines) <= fixtureLines, "fixture size")
	seen := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		assert.Between(len(fields), callFieldsMin, callFieldsMax, "fixture line")
		chain, err := strconv.ParseUint(fields[0], 10, 64)
		assert.NoError(err, "chain id")
		assert.Equal(chain, uint64(mainnetChain), "mainnet")
		input, err := hex.DecodeString(strings.TrimPrefix(fields[2], "0x"))
		assert.NoError(err, "calldata hex")
		checkCall(chain, input, fields[3])
		seen++
	}
	assert.Equal(seen, mainnetCalls, "every mainnet add/rotate call")
}

func checkCall(chain uint64, input []byte, tx string) {
	sel := hex.EncodeToString(input[:4])
	var pub [validator.PublicKeyBytes]byte
	copy(pub[:], wordAt(input, 1))
	ingress, egress := string(dynAt(input, 2)), string(dynAt(input, 3))
	switch sel {
	case selectorAdd:
		who := primitives.BytesToAddress(wordAt(input, 0)[12:])
		fee := primitives.BytesToAddress(wordAt(input, 4)[12:])
		sig := dynAt(input, 5)
		r, err := validator.Registration{ChainID: chain, Validator: who, PublicKey: pub, Ingress: ingress, Egress: egress}.Canonical()
		assert.NoError(err, "registration "+tx)
		assert.Equal(r.Ingress, ingress, "ingress is canonical "+tx)
		assert.NoError(r.CheckAdd(fee, sig), "mainnet add signature verifies "+tx)
		assert.Ok(bytes.Equal(r.AddCalldata(fee, sig), input), "add calldata is byte-identical "+tx)
	case selectorRot:
		idx := binary.BigEndian.Uint64(wordAt(input, 0)[word-8:])
		sig := dynAt(input, 4)
		r := validator.Registration{ChainID: chain, PublicKey: pub, Ingress: ingress, Egress: egress}
		assert.Ok(bytes.Equal(r.RotateCalldata(idx, sig), input), "rotate calldata is byte-identical "+tx)
	default:
		assert.Fail("unexpected selector %s in %s", sel, tx)
	}
}

func TestRegistrationSignRoundTrip(_ *testing.T) {
	k, err := validator.ParseSigningKey(rustSigningKey)
	assert.NoError(err, "key")
	who := primitives.Address{1, 2, 3}
	fee := primitives.Address{9}
	r, err := validator.Registration{ChainID: mainnetChain, Validator: who, PublicKey: k.Public(), Ingress: "[2001:db8::0001]:8000", Egress: "2001:0db8::1"}.Canonical()
	assert.NoError(err, "registration")
	assert.Equal(r.Ingress, "[2001:db8::1]:8000", "ipv6 ingress canonical like Rust SocketAddr Display")
	assert.Equal(r.Egress, "2001:db8::1", "ipv6 egress canonical")
	sig := r.SignAdd(k, fee)
	assert.NoError(r.CheckAdd(fee, sig[:]), "add signature verifies")
	assert.Ok(r.CheckAdd(primitives.Address{8}, sig[:]) == validator.ErrSignature, "fee recipient is bound")
	rot := r.SignRotate(k)
	assert.NoError(r.CheckRotate(rot[:]), "rotate signature verifies")
	assert.Ok(r.CheckRotate(sig[:]) == validator.ErrSignature, "namespaces differ")
	_, err = validator.Registration{Ingress: "1.2.3.4", Egress: "1.2.3.4"}.Canonical()
	assert.Ok(err == validator.ErrIngress, "ingress needs a port")
	_, err = validator.Registration{Ingress: "1.2.3.4:1", Egress: "1.2.3.4:1"}.Canonical()
	assert.Ok(err == validator.ErrEgress, "egress is an ip")
}
