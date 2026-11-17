package dkg_test

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

func readHex(name string) []byte {
	raw, err := os.ReadFile("testdata/" + name)
	assert.NoError(err, "read fixture")
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(string(raw)), "0x"))
	assert.NoError(err, "decode fixture")
	return b
}

func TestMainnetOutcomesRoundTrip(_ *testing.T) {
	for _, name := range []string{"mainnet_block_44085599_extradata.hex", "mainnet_block_44107199_extradata.hex"} {
		raw := readHex(name)
		o, err := dkg.DecodeOutcome(raw)
		assert.NoError(err, "decode mainnet outcome")
		assert.Equal(hex.EncodeToString(o.Encode()), hex.EncodeToString(raw), "re-encodes byte for byte")
		assert.Equal(int(o.Output.Public.Total), len(o.Output.Players), "one share per player")
		assert.Equal(len(o.Output.Public.Poly), o.Output.Players.Quorum(), "degree is quorum minus one")
	}
}

func TestMainnetFullDKGChangesIdentity(_ *testing.T) {
	a, err := dkg.DecodeOutcome(readHex("mainnet_block_44085599_extradata.hex"))
	assert.NoError(err, "decode 2041")
	b, err := dkg.DecodeOutcome(readHex("mainnet_block_44107199_extradata.hex"))
	assert.NoError(err, "decode 2042")
	assert.Equal(b.Epoch, a.Epoch+1, "consecutive epochs")
	assert.Ok(a.NextFullDKG, "2041 announces a full dkg for 2042")
	assert.Ok(!a.Output.Public.Poly.Constant().Equal(b.Output.Public.Poly.Constant()), "the full dkg makes a new group key")
}
