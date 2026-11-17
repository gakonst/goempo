package hardfork_test

import (
	"testing"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/kapetan-io/tiger/assert"
)

func TestMainnetSchedule(t *testing.T) {
	cases := []struct {
		name string
		time uint64
		want hardfork.Fork
	}{
		{"block 1", 0x696aa4c7, hardfork.T0},
		{"before T1", 1_770_908_399, hardfork.T0},
		{"T1 and T1A", 1_770_908_400, hardfork.T1A},
		{"T1C", 1_773_327_600, hardfork.T1C},
		{"block 30000000", 0x6a56b9ca, hardfork.T7},
		{"block 48557098", 0x6af4c9f6, hardfork.T13},
		{"T14", 1_796_742_000, hardfork.T14},
	}
	for _, c := range cases {
		got := hardfork.At(hardfork.MainnetChainID, c.time)
		assert.Equal(got, c.want, c.name)
	}
}

func TestModeratoAndUnknownChain(t *testing.T) {
	assert.Equal(hardfork.At(hardfork.ModeratoChainID, 1_770_303_599), hardfork.Genesis, "moderato before T0")
	assert.Equal(hardfork.At(hardfork.ModeratoChainID, 1_771_858_800), hardfork.T1B, "moderato T1A and T1B together")
	assert.Equal(hardfork.At(1337, 0), hardfork.Latest(), "dev chain runs the latest fork")
	_, ok := hardfork.Activation(hardfork.MainnetChainID, hardfork.T15)
	assert.Ok(!ok, "T15 unscheduled")
	at, ok := hardfork.Activation(hardfork.MainnetChainID, hardfork.T7)
	assert.Ok(ok, "T7 scheduled")
	assert.Equal(at, uint64(1_783_605_600), "T7 time")
	assert.Equal(hardfork.T1C.String(), "T1C", "name")
	assert.Ok(hardfork.T12.AtLeast(hardfork.T4), "ordering")
}
