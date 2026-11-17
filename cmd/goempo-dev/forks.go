package main

import (
	"encoding/json"
	"errors"
	"sort"
)

const searchStepsMax = 65

const _ = uint(searchStepsMax - uint64Bits - 1)

var errSearch = errors.New("binary search did not converge")

type fork struct {
	Name string `json:"name"`
	Time uint64 `json:"time"`
}

var mainnetForks = []fork{
	{Name: "T0", Time: 0},
	{Name: "T1", Time: 1770908400},
	{Name: "T1B", Time: 1771858800},
	{Name: "T1C", Time: 1773327600},
	{Name: "T2", Time: 1774965600},
	{Name: "T3", Time: 1777298400},
	{Name: "T4", Time: 1779112800},
	{Name: "T5", Time: 1781013600},
	{Name: "T6", Time: 1782223200},
	{Name: "T7", Time: 1783605600},
	{Name: "T8", Time: 1785420000},
	{Name: "T9", Time: 1786024800},
	{Name: "T10", Time: 1787320800},
	{Name: "T11", Time: 1789048800},
	{Name: "T12", Time: 1791900000},
	{Name: "T13", Time: 1793718000},
	{Name: "T14", Time: 1796742000},
}

func forkAt(timestamp uint64) string {
	name := mainnetForks[0].Name
	for i := range mainnetForks {
		if mainnetForks[i].Time <= timestamp {
			name = mainnetForks[i].Name
		}
	}
	return name
}

type forkWindow struct {
	Name  string `json:"name"`
	First uint64 `json:"first"`
	Last  uint64 `json:"last"`
}

type timeSearch struct {
	timestamp uint64
	head      uint64
}

func (c *rpcClient) firstBlockAtOrAfter(q timeSearch) (uint64, error) {
	lo, hi := uint64(0), q.head+1
	step := 0
	for ; step < searchStepsMax; step++ {
		if lo >= hi {
			break
		}
		mid := lo + (hi-lo)/2
		ts, err := c.blockTimestamp(mid)
		if err != nil {
			return 0, err
		}
		if ts >= q.timestamp {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if step == searchStepsMax {
		return 0, errSearch
	}
	return lo, nil
}

func (c *rpcClient) forkWindows() ([]forkWindow, error) {
	head, err := c.headNumber()
	if err != nil {
		return nil, err
	}
	starts := make([]uint64, len(mainnetForks))
	for i := range mainnetForks {
		starts[i], err = c.firstBlockAtOrAfter(timeSearch{timestamp: mainnetForks[i].Time, head: head})
		if err != nil {
			return nil, err
		}
	}
	var out []forkWindow
	for i := range mainnetForks {
		if starts[i] > head {
			break
		}
		last := head
		if i+1 < len(mainnetForks) && starts[i+1] <= head {
			last = starts[i+1] - 1
		}
		if last < starts[i] {
			continue
		}
		out = append(out, forkWindow{Name: mainnetForks[i].Name, First: starts[i], Last: last})
	}
	return out, nil
}

type busyOptions struct {
	busy  int
	probe int
}

type blockCount struct {
	number uint64
	txs    uint64
}

func (c *rpcClient) busiestBlocks(w forkWindow, opts busyOptions) ([]uint64, error) {
	if opts.busy <= 0 {
		return nil, nil
	}
	probes := sampleWindow(w, opts.probe)
	counts := make([]blockCount, 0, len(probes))
	for start := 0; start < len(probes); start += rpcBatchMax {
		end := min(start+rpcBatchMax, len(probes))
		got, err := c.txCounts(probes[start:end])
		if err != nil {
			return nil, err
		}
		counts = append(counts, got...)
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i].txs > counts[j].txs })
	counts = counts[:min(len(counts), opts.busy)]
	out := make([]uint64, len(counts))
	for i := range counts {
		out[i] = counts[i].number
	}
	return out, nil
}

func (c *rpcClient) txCounts(numbers []uint64) ([]blockCount, error) {
	reqs := make([]rpcRequest, len(numbers))
	for i, n := range numbers {
		reqs[i] = rpcRequest{JSONRPC: jsonRPCVersion, ID: i, Method: "eth_getBlockTransactionCountByNumber", Params: []any{blockTag(n)}}
	}
	results, err := c.batch(reqs)
	if err != nil {
		return nil, err
	}
	out := make([]blockCount, len(numbers))
	for i, raw := range results {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		txs, err := parseQuantity(s)
		if err != nil {
			return nil, err
		}
		out[i] = blockCount{number: numbers[i], txs: txs}
	}
	return out, nil
}

func sampleWindow(w forkWindow, count int) []uint64 {
	count = min(count, sampleMax)
	if count < 2 {
		return []uint64{w.First}
	}
	span := w.Last - w.First
	seen := make(map[uint64]bool, count)
	var out []uint64
	for i := 0; i < count; i++ {
		n := w.First + span/uint64(count-1)*uint64(i)
		if i == count-1 {
			n = w.Last
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}
