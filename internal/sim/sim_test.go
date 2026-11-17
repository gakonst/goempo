package sim_test

import (
	"fmt"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/sim"
	"github.com/kapetan-io/tiger/assert"
)

func timeouts() consensus.Timeouts {
	return consensus.Timeouts{Leader: 1000, Notarization: 2000, Retry: 1000}
}

func TestFourValidatorsFinalizeAcrossEpochs(_ *testing.T) {
	cfg := sim.Config{Validators: 4, EpochLength: 8, Epochs: 3, Seed: 1, LatencyMin: 10, LatencyMax: 50, Timeouts: timeouts(), TimeMax: 600_000}
	r, err := sim.Run(cfg)
	assert.NoError(err, "run")
	rep, err := sim.Verify(r, cfg.EpochLength)
	assert.NoError(err, "verify")
	fmt.Printf("heads %v time %dms events %d messages %d: %+v\n", r.Heads, r.Time, r.Events, r.Messages, rep)
	assert.Ok(rep.Height >= 24, "three epochs finalized")
	assert.Ok(rep.Transitions >= 3, "three epoch transitions")
	assert.Equal(rep.Identities[1][2:], rep.Identities[0][2:], "reshare keeps the group key")
}

func TestFaultyNetworkStaysSafeAndLive(_ *testing.T) {
	cfg := sim.Config{
		Validators: 5, EpochLength: 10, Epochs: 2, Seed: 7, DropPercent: 10, LatencyMin: 5, LatencyMax: 200,
		Timeouts: timeouts(), TimeMax: 1_200_000,
		Outages: []sim.Outage{{Node: 2, From: 3000, To: 15000}},
	}
	r, err := sim.Run(cfg)
	assert.NoError(err, "run")
	rep, err := sim.Verify(r, cfg.EpochLength)
	assert.NoError(err, "verify")
	fmt.Printf("heads %v failures %v time %dms events %d messages %d dropped %d: %+v\n", r.Heads, r.Failures, r.Time, r.Events, r.Messages, r.Dropped, rep)
	assert.Ok(rep.Transitions >= 2, "two epoch transitions")
	assert.Equal(r.Failures[0], 0, "every reshare succeeds with one node down")
}

func TestSevenValidatorsOutageAcrossBoundary(_ *testing.T) {
	cfg := sim.Config{
		Validators: 7, EpochLength: 12, Epochs: 3, Seed: 11, DropPercent: 5, LatencyMin: 5, LatencyMax: 150,
		Timeouts: timeouts(), TimeMax: 1_200_000,
		Outages: []sim.Outage{{Node: 1, From: 1000, To: 9000}, {Node: 5, From: 2000, To: 12000}},
	}
	r, err := sim.Run(cfg)
	assert.NoError(err, "run")
	rep, err := sim.Verify(r, cfg.EpochLength)
	assert.NoError(err, "verify")
	fmt.Printf("heads %v failures %v time %dms events %d messages %d dropped %d: %+v\n", r.Heads, r.Failures, r.Time, r.Events, r.Messages, r.Dropped, rep)
	assert.Ok(rep.Transitions >= 3, "three epoch transitions")
	assert.Ok(r.Failures[0] < rep.Transitions, "at least one reshare succeeds despite the outages")
}
