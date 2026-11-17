package rpc

import (
	"slices"
	"strings"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const (
	consensusEpochLength = 21600
)

type namedAddress struct {
	name string
	addr string
}

var ethPrecompileNames = []namedAddress{
	{"BLAKE2F", "0x0000000000000000000000000000000000000009"},
	{"BLS12_G1ADD", "0x000000000000000000000000000000000000000b"},
	{"BLS12_G1MSM", "0x000000000000000000000000000000000000000c"},
	{"BLS12_G2ADD", "0x000000000000000000000000000000000000000d"},
	{"BLS12_G2MSM", "0x000000000000000000000000000000000000000e"},
	{"BLS12_MAP_FP2_TO_G2", "0x0000000000000000000000000000000000000011"},
	{"BLS12_MAP_FP_TO_G1", "0x0000000000000000000000000000000000000010"},
	{"BLS12_PAIRING_CHECK", "0x000000000000000000000000000000000000000f"},
	{"BN254_ADD", "0x0000000000000000000000000000000000000006"},
	{"BN254_MUL", "0x0000000000000000000000000000000000000007"},
	{"BN254_PAIRING", "0x0000000000000000000000000000000000000008"},
	{"ECREC", "0x0000000000000000000000000000000000000001"},
	{"ID", "0x0000000000000000000000000000000000000004"},
	{"KZG_POINT_EVALUATION", "0x000000000000000000000000000000000000000a"},
	{"MODEXP", "0x0000000000000000000000000000000000000005"},
	{"P256VERIFY", "0x0000000000000000000000000000000000000100"},
	{"RIPEMD160", "0x0000000000000000000000000000000000000003"},
	{"SHA256", "0x0000000000000000000000000000000000000002"},
}

var systemContractNames = []namedAddress{
	{"BEACON_ROOTS_ADDRESS", "0x000f3df6d732807ef1319fb7b8bb8522d0beac02"},
	{"CONSOLIDATION_REQUEST_PREDEPLOY_ADDRESS", "0x0000bbddc7ce488642fb579f8b00f3a590007251"},
	{"DEPOSIT_CONTRACT_ADDRESS", "0x0000000000000000000000000000000000000000"},
	{"HISTORY_STORAGE_ADDRESS", "0x0000f90827f1c53a10cb7a02335b175320002935"},
	{"WITHDRAWAL_REQUEST_PREDEPLOY_ADDRESS", "0x00000961ef480eb55e80d19ad83579a64c007002"},
}

var zeroBlockForks = []string{
	"homesteadBlock", "daoForkSupport", "eip150Block", "eip155Block", "eip158Block", "byzantiumBlock",
	"constantinopleBlock", "petersburgBlock", "istanbulBlock", "berlinBlock", "londonBlock",
	"mergeNetsplitBlock", "shanghaiTime", "cancunTime", "pragueTime", "osakaTime", "terminalTotalDifficulty",
}

var blobScheduleJSON = []byte(`{"baseFeeUpdateFraction":5007716,"max":9,"target":6}`)

func writeBlobSchedule(w *writer) {
	w.raw(blobScheduleJSON)
}

type forkTime struct {
	key string
	at  uint64
}

func (s *Server) forkTimes() []forkTime {
	chainID := hardfork.ChainID(s.cfg.ChainID)
	var out []forkTime
	for i := range int(hardfork.Latest()) {
		f := hardfork.Fork(i + 1)
		at, ok := hardfork.Activation(chainID, f)
		if !ok {
			continue
		}
		out = append(out, forkTime{key: "t" + strings.ToLower(strings.TrimPrefix(f.String(), "T")) + "Time", at: at})
	}
	slices.SortFunc(out, func(a, b forkTime) int { return strings.Compare(a.key, b.key) })
	return out
}

func debugChainConfig(s *Server, _ *params) ([]byte, error) {
	return encode(func(w *writer) {
		w.objOpen()
		w.key("chainId")
		w.integer(int64(s.cfg.ChainID))
		for _, k := range zeroBlockForks {
			w.key(field(k))
			if k == "daoForkSupport" {
				w.raw([]byte("false"))
				continue
			}
			w.integer(0)
		}
		w.kBool("terminalTotalDifficultyPassed", true)
		w.key("epochLength")
		w.integer(consensusEpochLength)
		for _, t := range s.forkTimes() {
			w.key(field(t.key))
			w.integer(int64(t.at))
		}
		w.kStr("depositContractAddress", "0x0000000000000000000000000000000000000000")
		w.key("blobSchedule")
		w.objOpen()
		for _, name := range []string{"cancun", "osaka", "prague"} {
			w.key(field(name))
			writeBlobSchedule(w)
		}
		w.objClose()
		w.objClose()
	}), nil
}

func (s *Server) writeForkConfig(w *writer, e forkEntry) {
	w.objOpen()
	w.key("activationTime")
	w.integer(int64(e.at))
	w.key("blobSchedule")
	writeBlobSchedule(w)
	w.kQuantity("chainId", s.cfg.ChainID)
	w.kData("forkId", e.id[:])
	w.key("precompiles")
	w.objOpen()
	for _, p := range ethPrecompileNames {
		w.kStr(field(p.name), p.addr)
	}
	w.objClose()
	w.key("systemContracts")
	w.objOpen()
	for _, c := range systemContractNames {
		w.kStr(field(c.name), c.addr)
	}
	w.objClose()
	w.objClose()
}

func distinctForks(entries []forkEntry) []forkEntry {
	var out []forkEntry
	for _, e := range entries {
		if len(out) > 0 && out[len(out)-1].at == e.at {
			out[len(out)-1] = e
			continue
		}
		out = append(out, e)
	}
	return out
}

func ethConfig(s *Server, _ *params) ([]byte, error) {
	forks := distinctForks(s.forkEntries())
	if len(forks) == 0 {
		return nil, internalError("no fork schedule for this chain")
	}
	cur := 0
	for i, e := range forks {
		if e.active {
			cur = i
		}
	}
	return encode(func(w *writer) {
		w.objOpen()
		w.key("current")
		s.writeForkConfig(w, forks[cur])
		w.key("next")
		if cur+1 < len(forks) {
			s.writeForkConfig(w, forks[cur+1])
		} else {
			w.raw(resultNull)
		}
		w.key("last")
		if cur+1 < len(forks) {
			s.writeForkConfig(w, forks[len(forks)-1])
		} else {
			w.raw(resultNull)
		}
		w.objClose()
	}), nil
}
