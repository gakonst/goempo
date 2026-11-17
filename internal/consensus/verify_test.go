package consensus_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/tempo/dkg"
	"github.com/kapetan-io/tiger/assert"
)

const mainnetIdentity2042 = "8627d33753b0d90a8eae75363d6b393ab9204076545b3af6caf831903e4bedde4374f1106551b481fb5ea35e59d51c92165c963881c718216e3e4570a307c08885af55c274f19488f709afe021ca3d9e567138ab097860d9146e895407bea182"

type fixture struct {
	Height      uint64
	Epoch       uint64
	View        uint64
	Digest      string
	Certificate string
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	assert.NoError(err, "hex")
	return b
}

func loadFixtures() []fixture {
	raw, err := os.ReadFile("testdata/mainnet_finalizations.jsonl")
	assert.NoError(err, "read fixtures")
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	out := make([]fixture, 0, len(lines))
	for _, line := range lines {
		var fx fixture
		assert.NoError(json.Unmarshal([]byte(line), &fx), "parse fixture")
		out = append(out, fx)
	}
	return out
}

func identityFor(epoch uint64) consensus.Identity {
	if epoch >= 2042 {
		id, err := consensus.DecodeIdentity(unhex(mainnetIdentity2042))
		assert.NoError(err, "decode 2042 identity")
		return id
	}
	boundary := map[uint64]string{1949: "42098399", 2041: "44085599"}[epoch]
	raw, err := os.ReadFile("testdata/mainnet_block_" + boundary + "_extradata.hex")
	assert.NoError(err, "read boundary extra data")
	outcome, err := dkg.Decode(unhex(string(raw)))
	assert.NoError(err, "decode outcome")
	assert.Equal(outcome.Epoch, epoch, "outcome epoch")
	id, err := consensus.DecodeIdentity(outcome.Identity[:])
	assert.NoError(err, "decode outcome identity")
	return id
}

func TestMainnetFinalizationsVerify(t *testing.T) {
	ns := consensus.NewNamespace(consensus.TempoNamespace)
	fixtures := loadFixtures()
	assert.Equal(len(fixtures), 4, "fixture count")
	for _, fx := range fixtures {
		raw := unhex(fx.Certificate)
		f, err := consensus.DecodeFinalization(raw)
		assert.NoError(err, "decode finalization")
		assert.Equal(f.Proposal.Round.Epoch, fx.Epoch, "epoch")
		assert.Equal(f.Proposal.Round.View, fx.View, "view")
		assert.Equal(hex.EncodeToString(f.Proposal.Payload[:]), strings.TrimPrefix(fx.Digest, "0x"), "digest")
		assert.Equal(hex.EncodeToString(f.Encode()), hex.EncodeToString(raw), "re-encode")
		id := identityFor(fx.Epoch)
		assert.NoError(consensus.VerifyFinalization(id, ns, f), "verify finalization")
		bad := f
		bad.Proposal.Payload[0] ^= 1
		assert.Ok(consensus.VerifyFinalization(id, ns, bad) != nil, "tampered digest fails")
		swapped := f
		swapped.Certificate.Vote, swapped.Certificate.Seed = f.Certificate.Seed, f.Certificate.Vote
		assert.Ok(consensus.VerifyFinalization(id, ns, swapped) != nil, "swapped signatures fail")
		assert.Ok(consensus.VerifyFinalization(id, consensus.NewNamespace("OTHER"), f) != nil, "namespace binds")
	}
}

func TestDecodeFinalizationRejects(t *testing.T) {
	raw := unhex(loadFixtures()[0].Certificate)
	_, err := consensus.DecodeFinalization(raw[:len(raw)-1])
	assert.Equal(err, consensus.ErrShort, "truncated")
	_, err = consensus.DecodeFinalization(append(append([]byte(nil), raw...), 0))
	assert.Equal(err, consensus.ErrLeftover, "left over")
	_, err = consensus.DecodeFinalization([]byte{0x80, 0x00})
	assert.Equal(err, consensus.ErrVarint, "non-canonical varint")
}

func BenchmarkVerifyFinalization(b *testing.B) {
	ns := consensus.NewNamespace(consensus.TempoNamespace)
	fx := loadFixtures()[2]
	f, err := consensus.DecodeFinalization(unhex(fx.Certificate))
	assert.NoError(err, "decode")
	id := identityFor(fx.Epoch)
	for i := 0; i < b.N; i++ {
		assert.NoError(consensus.VerifyFinalization(id, ns, f), "verify")
	}
}
