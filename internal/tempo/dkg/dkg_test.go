package dkg_test

import (
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/tempo/dkg"
	"github.com/kapetan-io/tiger/assert"
)

func TestDecodeMainnetBoundary(t *testing.T) {
	data, err := os.ReadFile("testdata/extra-32443199.bin")
	assert.NoError(err, "read extra data")
	o, err := dkg.Decode(data)
	assert.NoError(err, "decode")
	assert.Equal(o.Epoch, uint64(1502), "the committee contract holds epoch 1502 after this block")
	assert.Ok(len(o.Players) > 0, "players")
	assert.Equal(o.TrailingBytes, 0, "no trailing bytes")
	_, err = dkg.Decode(data[:len(data)-1])
	assert.Ok(err != nil, "a truncated outcome fails")
}
