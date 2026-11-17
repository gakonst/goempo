package validator_test

import (
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	electorLines    = 10
	electorLinesMax = 32
	electorFields   = 5
	electorPairs    = 8
)

const _ = uint(electorLinesMax - electorLines)

var outcomeFiles = map[uint64]string{
	1949: "../consensus/testdata/mainnet_block_42098399_extradata.hex",
	2042: "../consensus/testdata/mainnet_block_44107199_extradata.hex",
}

type electorRow struct {
	version consensus.ElectorVersion
	epoch   uint64
	height  uint64
	final   consensus.Finalization
	header  types.Header
}

func hexBytes(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(s), "0x"))
	assert.NoError(err, "hex")
	return b
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	assert.NoError(err, "read "+path)
	return string(b)
}

func parseUint(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	assert.NoError(err, "uint")
	return v
}

func readElectorRows() []electorRow {
	lines := strings.Split(strings.TrimSpace(readFile("testdata/mainnet_elector.txt")), "\n")
	assert.Between(len(lines), electorLines, electorLinesMax, "fixture rows")
	rows := make([]electorRow, 0, len(lines))
	for _, line := range lines {
		f := strings.Fields(line)
		assert.Equal(len(f), electorFields, "fixture fields")
		fin, err := consensus.DecodeFinalization(hexBytes(f[3]))
		assert.NoError(err, "finalization")
		hdr, err := types.DecodeHeader(hexBytes(f[4]))
		assert.NoError(err, "header")
		version := consensus.ElectorV0
		if f[0] == "v1" {
			version = consensus.ElectorV1
		}
		rows = append(rows, electorRow{version: version, epoch: parseUint(f[1]), height: parseUint(f[2]), final: fin, header: hdr})
	}
	return rows
}

func playersOf(epoch uint64) dkg.KeySet {
	outcome, err := dkg.DecodeOutcome(hexBytes(readFile(outcomeFiles[epoch])))
	assert.NoError(err, "outcome")
	return outcome.Output.Players
}

func electedFor(prev *electorRow, cur electorRow, n int) int {
	e := consensus.Election{Version: cur.version, Round: cur.final.Proposal.Round, N: n}
	if prev != nil {
		seed := prev.final.Certificate.Seed
		e.Seed = &seed
	}
	return e.Leader()
}

func TestMainnetLeaderElection(_ *testing.T) {
	rows := readElectorRows()
	checked := 0
	for i, cur := range rows {
		ctx := cur.header.Consensus
		assert.Ok(ctx != nil, "header has a consensus context")
		assert.Equal(ctx.View, cur.final.Proposal.Round.View, "header view matches the finalization")
		var prev *electorRow
		if i > 0 && rows[i-1].epoch == cur.epoch {
			prev = &rows[i-1]
			assert.Equal(cur.height, prev.height+1, "consecutive heights")
			assert.Equal(ctx.ParentView, prev.final.Proposal.Round.View, "no nullified views between")
		}
		if prev == nil && ctx.View != 1 {
			continue
		}
		players := playersOf(cur.epoch)
		assert.Equal([32]byte(players[electedFor(prev, cur, len(players))]), ctx.Proposer, "elects the mainnet proposer")
		checked++
	}
	assert.Equal(checked, electorPairs+1, "every adjacent pair and the view-1 block")
}

func TestElectionVersionsDiffer(_ *testing.T) {
	rows := readElectorRows()
	n := len(playersOf(rows[2].epoch))
	v1 := rows[2]
	v1.version = consensus.ElectorV1
	assert.Ok(electedFor(&rows[1], rows[2], n) != electedFor(&rows[1], v1, n), "V1 at an epoch 1949 view picks someone other than the proposer")
}

func TestElectionViewOne(_ *testing.T) {
	e := consensus.Election{Round: consensus.Round{Epoch: 7, View: 1}, N: 5}
	assert.Equal(e.Leader(), 3, "view 1 is round robin from epoch+view")
	assert.Equal(consensus.Modulo([]byte{1, 0}, 7), uint64(256%7), "big-endian fold")
	big := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	assert.Equal(consensus.Modulo(big, 1<<63+5), uint64((1<<72-1)%(1<<63+5)), "fold past 64 bits")
}
