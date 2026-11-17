package dkg_test

import (
	"encoding/hex"
	"testing"

	"github.com/gakonst/goempo/internal/dkg"
	"github.com/kapetan-io/tiger/assert"
)

func pattern(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i % 251)
	}
	return out
}

func TestBlake3MatchesReferenceVectors(_ *testing.T) {
	empty := dkg.Blake3(nil)
	assert.Equal(hex.EncodeToString(empty[:]), "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9adc112b7cc9a93cae41f3262", "empty input")
	two := dkg.Blake3(pattern(1025))
	assert.Equal(hex.EncodeToString(two[:]), "d00278ae47eb27b34faecf67b4fe263f82d5412916c1ffd97c8cb7fb814b8444", "two chunks")
	long := dkg.Blake3XOF(pattern(5000), 20)
	assert.Equal(hex.EncodeToString(long), "ee78d92070de3df1c57c37002abf0a6b1a6589ac", "five chunks")
}

func TestBlake3KeyedAndExtended(_ *testing.T) {
	var key [32]byte
	copy(key[:], "whats the Elvish word for friend")
	got := dkg.Blake3KeyedXOF(key, pattern(1025), 80)
	want := "357dc55de0c7e382c900fd6e320acc04146be01db6a8ce7210b7189bd664ea69" +
		"362396b77fdc0d2634a552970843722066c3c15902ae5097e00ff53f1e116f1cd5352720113a837ab2452cafbde4d540"
	assert.Equal(hex.EncodeToString(got), want, "keyed extended output")
}

func TestNoiseReadsAreSequential(_ *testing.T) {
	tr := dkg.NewTranscript([]byte("ns"))
	tr.Commit([]byte("x"))
	a := tr.Noise([]byte("n"))
	b := tr.Noise([]byte("n"))
	x := make([]byte, 80)
	y := make([]byte, 80)
	_, err := a.Read(x[:30])
	assert.NoError(err, "read")
	_, err = a.Read(x[30:])
	assert.NoError(err, "read")
	_, err = b.Read(y)
	assert.NoError(err, "read")
	assert.Equal(hex.EncodeToString(x), hex.EncodeToString(y), "noise is one stream")
	ack := tr.Fork([]byte("ack"))
	log := tr.Fork([]byte("log"))
	assert.Ok(ack.Summarize() != log.Summarize(), "forks bind their label")
}
