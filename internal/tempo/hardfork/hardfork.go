package hardfork

import (
	"errors"
	"strings"
	"sync"

	"github.com/kapetan-io/tiger/assert"
)

type Fork uint8

const (
	Genesis Fork = iota
	T0
	T1
	T1A
	T1B
	T1C
	T2
	T3
	T4
	T5
	T6
	T7
	T8
	T9
	T10
	T11
	T12
	T13
	T14
	T15
	forkCount
)

type ChainID uint64

const (
	MainnetChainID  ChainID = 4217
	ModeratoChainID ChainID = 42431
)

const unscheduled uint64 = 1<<64 - 1

var names = [forkCount]string{
	"Genesis", "T0", "T1", "T1A", "T1B", "T1C", "T2", "T3", "T4", "T5",
	"T6", "T7", "T8", "T9", "T10", "T11", "T12", "T13", "T14", "T15",
}

var mainnet = [forkCount]uint64{
	Genesis: 0,
	T0:      0,
	T1:      1_770_908_400,
	T1A:     1_770_908_400,
	T1B:     1_771_858_800,
	T1C:     1_773_327_600,
	T2:      1_774_965_600,
	T3:      1_777_298_400,
	T4:      1_779_112_800,
	T5:      1_781_013_600,
	T6:      1_782_223_200,
	T7:      1_783_605_600,
	T8:      1_785_420_000,
	T9:      1_786_024_800,
	T10:     1_787_320_800,
	T11:     1_789_048_800,
	T12:     1_791_900_000,
	T13:     1_793_718_000,
	T14:     1_796_742_000,
	T15:     unscheduled,
}

var moderato = [forkCount]uint64{
	Genesis: 0,
	T0:      1_770_303_600,
	T1:      1_770_303_600,
	T1A:     1_771_858_800,
	T1B:     1_771_858_800,
	T1C:     1_773_068_400,
	T2:      1_774_537_200,
	T3:      1_776_780_000,
	T4:      1_778_767_200,
	T5:      1_780_495_200,
	T6:      1_781_791_200,
	T7:      1_783_000_800,
	T8:      1_785_160_800,
	T9:      1_785_938_400,
	T10:     1_787_234_400,
	T11:     1_788_962_400,
	T12:     1_791_468_000,
	T13:     1_793_286_000,
	T14:     1_795_532_400,
	T15:     unscheduled,
}

func (f Fork) String() string {
	assert.Ok(f < forkCount, "fork out of range")
	return names[f]
}

func (f Fork) AtLeast(g Fork) bool { return f >= g }

func Latest() Fork { return forkCount - 1 }

type Schedule [forkCount]uint64

var ErrBuiltinChain = errors.New("hardfork: mainnet and moderato schedules are built in")

var custom struct {
	mu     sync.Mutex
	chains map[ChainID]*[forkCount]uint64
}

func NewSchedule() Schedule {
	var s Schedule
	for f := range s {
		s[f] = unscheduled
	}
	s[Genesis] = 0
	return s
}

func GenesisKey(f Fork) string { return strings.ToLower(names[f]) + "Time" }

func Register(chainID ChainID, s Schedule) error {
	if chainID == MainnetChainID || chainID == ModeratoChainID {
		return ErrBuiltinChain
	}
	t := [forkCount]uint64(s)
	custom.mu.Lock()
	defer custom.mu.Unlock()
	if custom.chains == nil {
		custom.chains = map[ChainID]*[forkCount]uint64{}
	}
	custom.chains[chainID] = &t
	return nil
}

func schedule(chainID ChainID) (*[forkCount]uint64, bool) {
	if chainID == MainnetChainID {
		return &mainnet, true
	}
	if chainID == ModeratoChainID {
		return &moderato, true
	}
	custom.mu.Lock()
	defer custom.mu.Unlock()
	t, ok := custom.chains[chainID]
	return t, ok
}

func Activation(chainID ChainID, f Fork) (uint64, bool) {
	assert.Ok(f < forkCount, "fork out of range")
	times, ok := schedule(chainID)
	if !ok || times[f] == unscheduled {
		return 0, false
	}
	return times[f], true
}

func At(chainID ChainID, timestamp uint64) Fork {
	times, ok := schedule(chainID)
	if !ok {
		return Latest()
	}
	active := Genesis
	for f := Genesis; f < forkCount; f++ {
		if times[f] <= timestamp {
			active = f
		}
	}
	return active
}
