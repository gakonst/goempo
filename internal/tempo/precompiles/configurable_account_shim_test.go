package precompiles_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
	"github.com/kapetan-io/tiger/assert"
)

type shimSigner struct {
	key  secp256k1.PrivateKey
	addr primitives.Address
}

func shimSigners(t *testing.T) [2]shimSigner {
	var out [2]shimSigner
	for i := range out {
		var k secp256k1.PrivateKey
		k[31] = byte(i + 7)
		a, err := k.Address()
		assert.NoError(err, "address")
		out[i] = shimSigner{key: k, addr: a}
	}
	if bytes.Compare(out[0].addr[:], out[1].addr[:]) > 0 {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

type shimSig struct {
	account primitives.Address
	cfg     caCfg
	signers []shimSigner
	inner   primitives.Hash
}

func (s shimSig) encode(t *testing.T) []byte {
	owners := make([][]byte, len(s.cfg.owners))
	for i, o := range s.cfg.owners {
		owners[i] = rlp.EncodeList(rlp.EncodeBytes(o.owner[:]), rlp.EncodeUint64(o.weight))
	}
	cfg := rlp.EncodeList(rlp.EncodeBytes(s.cfg.salt[:]), rlp.EncodeUint64(s.cfg.version), rlp.EncodeUint64(s.cfg.threshold), rlp.EncodeList(owners...))
	pre := append(append(append([]byte("tempo:configurable-account:signature"), s.inner[:]...), s.account[:]...), binary.BigEndian.AppendUint64(nil, s.cfg.version)...)
	digest := primitives.Hash(keccak.Sum256(pre))
	sigs := make([][]byte, len(s.signers))
	for i, sg := range s.signers {
		sig, err := secp256k1.Sign(digest, sg.key)
		assert.NoError(err, "sign")
		raw := append(append(append([]byte{}, sig.R[:]...), sig.S[:]...), 27+sig.RecoveryID)
		sigs[i] = rlp.EncodeBytes(raw)
	}
	body := rlp.EncodeList(rlp.EncodeBytes(s.account[:]), cfg, rlp.EncodeList(sigs...))
	return append([]byte{0x05}, body...)
}

func shimInput(hash primitives.Hash, sig []byte) []byte {
	pad := make([]byte, (32-len(sig)%32)%32)
	args := append(append(append(append([]byte{}, hash[:]...), amount(64)...), amount(uint64(len(sig)))...), append(sig, pad...)...)
	return calldata("isValidSignature(bytes32,bytes)", args)
}

func (e caEnv) shim(account primitives.Address, input []byte) caResult {
	c := e.ctx(account)
	out, err := precompiles.ConfigurableAccountShim(c, account, input)
	return caResult{out: out, err: err, gas: c.GasUsed()}
}

func shimFixture(t *testing.T) shimSig {
	signers := shimSigners(t)
	cfg := caCfg{salt: primitives.Hash{31: 3}, threshold: 2, owners: []caOwner{{signers[0].addr, 1}, {signers[1].addr, 1}}}
	return shimSig{account: refDerive(cfg), cfg: cfg, signers: signers[:], inner: primitives.Hash{0: 0xab}}
}

func tokensOf(b []byte) uint64 {
	n := uint64(0)
	for _, x := range b {
		if x == 0 {
			n++
			continue
		}
		n += 4
	}
	return n
}

func keccakCost(n int) uint64 { return 30 + 6*uint64((n+31)/32) }

func TestConfigurableShimValidSignature(t *testing.T) {
	e := newCAEnv()
	s := shimFixture(t)
	raw := s.encode(t)
	input := shimInput(s.inner, raw)
	r := e.shim(s.account, input)
	assert.NoError(r.err, "isValidSignature")
	assert.Equal(hex.EncodeToString(r.out), "1626ba7e"+hex.EncodeToString(make([]byte, 28)), "magic value")
	cfgItem := rlp.NewStream(raw[1:])
	body, err := cfgItem.List()
	assert.NoError(err, "list")
	_, err = body.Raw()
	assert.NoError(err, "account")
	cfgRaw, err := body.Raw()
	assert.NoError(err, "config")
	w := append(rlp.EncodeBytes(s.account[:]), cfgRaw...)
	want := uint64(30*((len(input)+31)/32)) + 30 + 2_600 + tokensOf(w)*4 + 2*3_000 +
		keccakCost(33+32+8+2+42) + keccakCost(96) + keccakCost(34+32+2+42)
	assert.Equal(r.gas, want, "gas")
}

func TestConfigurableShimRegistered(t *testing.T) {
	e := newCAEnv()
	s := shimFixture(t)
	e.seed(s.account, refCommitment(s.cfg))
	r := e.shim(s.account, shimInput(s.inner, s.encode(t)))
	assert.NoError(r.err, "registered account")
	assert.Equal(r.out[0], byte(0x16), "valid")
	e.st.commits[s.account] = primitives.Hash{31: 1}
	r = e.shim(s.account, shimInput(s.inner, s.encode(t)))
	assert.NoError(r.err, "commitment mismatch")
	assert.Equal(r.out[0], byte(0xff), "invalid")
}

func TestConfigurableShimRejections(t *testing.T) {
	e := newCAEnv()
	s := shimFixture(t)
	signers := s.signers
	r := e.shim(caEOA, shimInput(s.inner, s.encode(t)))
	assert.NoError(r.err, "other account")
	assert.Equal(r.out[0], byte(0xff), "account mismatch is invalid")
	bad := s
	bad.inner = primitives.Hash{0: 0xcd}
	r = e.shim(s.account, shimInput(s.inner, bad.encode(t)))
	expectCARevert(t, r, "InvalidSignature()")
	one := s
	one.signers = []shimSigner{signers[0]}
	expectCARevert(t, e.shim(s.account, shimInput(s.inner, one.encode(t))), "InvalidSignature()")
	excess := s
	excess.cfg.threshold = 1
	excess.account = refDerive(excess.cfg)
	expectCARevert(t, e.shim(excess.account, shimInput(s.inner, excess.encode(t))), "InvalidSignature()")
	reversed := s
	reversed.signers = []shimSigner{signers[1], signers[0]}
	expectCARevert(t, e.shim(s.account, shimInput(s.inner, reversed.encode(t))), "InvalidSignature()")
	expectCARevert(t, e.shim(s.account, shimInput(s.inner, []byte{0x05, 0xc0})), "InvalidFormat()")
	v1 := s
	v1.cfg.version = 1
	r = e.shim(s.account, shimInput(s.inner, v1.encode(t)))
	assert.NoError(r.err, "unregistered nonzero version")
	assert.Equal(r.out[0], byte(0xff), "invalid")
}

func TestConfigurableShimOtherCalls(t *testing.T) {
	e := newCAEnv()
	r := e.shim(caEOA, nil)
	assert.NoError(r.err, "empty call")
	assert.Equal(len(r.out), 0, "empty output")
	in := calldata("onERC721Received(address,address,uint256,bytes)", word(caEOA), word(caOwnerAddr), amount(1), amount(128), amount(0))
	r = e.shim(caEOA, in)
	assert.NoError(r.err, "onERC721Received")
	assert.Equal(hex.EncodeToString(r.out[:4]), "150b7a02", "returns selector")
	r = e.shim(caEOA, calldata("transfer(address,uint256)", word(caEOA), amount(1)))
	assert.Ok(r.err != nil, "unknown selector reverts")
	assert.Equal(len(revertSelector(r.err)), 0, "empty revert")
}
