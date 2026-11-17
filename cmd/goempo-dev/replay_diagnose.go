package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	diagLogsMax     = 16
	diagAccountsMax = 64
	diagSlotsMax    = 64
	diagReceiptsMax = 1 << 16
)

const _ = uint(diagReceiptsMax - diagAccountsMax - diagSlotsMax - diagLogsMax)

type rpcReceipt struct {
	Status            string            `json:"status"`
	GasUsed           string            `json:"gasUsed"`
	CumulativeGasUsed string            `json:"cumulativeGasUsed"`
	TransactionHash   string            `json:"transactionHash"`
	Logs              []json.RawMessage `json:"logs"`
}

type prestateAccount struct {
	Balance string            `json:"balance"`
	Nonce   uint64            `json:"nonce"`
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

type prestateDiff struct {
	Pre  map[string]prestateAccount `json:"pre"`
	Post map[string]prestateAccount `json:"post"`
}

type traceResult struct {
	TxHash string       `json:"txHash"`
	Result prestateDiff `json:"result"`
}

func (r *replayer) diagnose(c *rpcClient) {
	m := r.failed
	fmt.Fprintf(os.Stderr, "diagnose: first mismatch at block %d (%s)\n", m.number, m.field)
	r.diagnoseState(c)
	if r.result != nil {
		r.diagnoseReceipts(c)
	}
}

func (r *replayer) diagnoseReceipts(c *rpcClient) {
	var want []rpcReceipt
	if err := callInto(c, rpcQuery{method: "eth_getBlockReceipts", params: []any{blockTag(r.failed.number)}}, &want); err != nil {
		fmt.Fprintln(os.Stderr, "diagnose: receipts:", err)
		return
	}
	got := r.result.Receipts
	if len(got) != len(want) {
		fmt.Fprintf(os.Stderr, "diagnose: %d receipts, want %d\n", len(got), len(want))
	}
	var prev uint64
	var bad []int
	defer func() { r.diagnoseFrames(c, r.failedBlock, bad) }()
	count := min(len(got), len(want), diagReceiptsMax)
	for i := 0; i < count; i++ {
		g := &got[i]
		w := &want[i]
		gasUsed := g.CumulativeGasUsed - prev
		prev = g.CumulativeGasUsed
		wantGas, err := strconv.ParseUint(w.GasUsed, 0, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "diagnose: receipt:", err)
			return
		}
		status := "0x0"
		if g.Success {
			status = "0x1"
		}
		if gasUsed == wantGas && status == w.Status && len(g.Logs) == len(w.Logs) {
			continue
		}
		if gasUsed != wantGas {
			bad = append(bad, i)
		}
		fmt.Fprintf(os.Stderr, "diagnose: tx %d %s: gasUsed %d want %d (diff %d), status %s want %s, logs %d want %d\n",
			i, w.TransactionHash, gasUsed, wantGas, int64(gasUsed)-int64(wantGas), status, w.Status, len(g.Logs), len(w.Logs))
		printLogs(g.Logs, w.Logs)
	}
}

func printLogs(got []types.Log, want []json.RawMessage) {
	for i := 0; i < min(len(got), diagLogsMax); i++ {
		fmt.Fprintf(os.Stderr, "  got log %d: %s topics=%d data=%x\n", i, got[i].Address, len(got[i].Topics), got[i].Data)
	}
	for i := 0; i < min(len(want), diagLogsMax); i++ {
		fmt.Fprintf(os.Stderr, "  want log %d: %s\n", i, string(want[i]))
	}
}

func (r *replayer) diagnoseState(c *rpcClient) {
	var traces []traceResult
	opts := map[string]any{"tracer": "prestateTracer", "tracerConfig": map[string]any{"diffMode": true}}
	if err := callInto(c, rpcQuery{method: "debug_traceBlockByNumber", params: []any{blockTag(r.failed.number), opts}}, &traces); err != nil {
		fmt.Fprintln(os.Stderr, "diagnose: prestate trace:", err)
		return
	}
	final := map[string]prestateAccount{}
	count := min(len(traces), diagReceiptsMax)
	for i := 0; i < count; i++ {
		mergePost(postMerge{into: final, from: traces[i].Result.Post})
	}
	st := r.db.State()
	shown := 0
	for _, addrHex := range slices.Sorted(maps.Keys(final)) {
		if shown >= diagAccountsMax {
			break
		}
		addr, err := parseAddr(addrHex)
		if err != nil {
			continue
		}
		acct := final[addrHex]
		shown += compareAccount(st, addr, &acct)
	}
	if shown == 0 {
		fmt.Fprintln(os.Stderr, "diagnose: every account and slot the trace wrote matches; look for writes the trace does not have")
	}
}

type postMerge struct {
	into map[string]prestateAccount
	from map[string]prestateAccount
}

func mergePost(m postMerge) {
	final := m.into
	for _, addr := range slices.Sorted(maps.Keys(m.from)) {
		acct := m.from[addr]
		prev, ok := final[addr]
		if !ok {
			final[addr] = acct
			continue
		}
		if acct.Balance != "" {
			prev.Balance = acct.Balance
		}
		if acct.Nonce != 0 {
			prev.Nonce = acct.Nonce
		}
		if acct.Code != "" {
			prev.Code = acct.Code
		}
		if prev.Storage == nil {
			prev.Storage = map[string]string{}
		}
		maps.Copy(prev.Storage, acct.Storage)
		final[addr] = prev
	}
}

type stateReader interface {
	Nonce(addr primitives.Address) uint64
	Storage(addr primitives.Address, key primitives.Hash) u256.Int
}

func compareAccount(st stateReader, addr primitives.Address, want *prestateAccount) int {
	diffs := 0
	if want.Nonce != 0 && st.Nonce(addr) != want.Nonce {
		fmt.Fprintf(os.Stderr, "diagnose: %s nonce %d want %d\n", addr, st.Nonce(addr), want.Nonce)
		diffs++
	}
	n := 0
	for _, k := range slices.Sorted(maps.Keys(want.Storage)) {
		if n >= diagSlotsMax {
			break
		}
		n++
		v := want.Storage[k]
		key, err := parseHash(k)
		if err != nil {
			continue
		}
		wantV, err := parseHash(v)
		if err != nil {
			continue
		}
		got := st.Storage(addr, key)
		if got.Bytes32() != [32]byte(wantV) {
			fmt.Fprintf(os.Stderr, "diagnose: %s slot %s = %x want %s\n", addr, k, got.Bytes32(), v)
			diffs++
		}
	}
	return diffs
}

var errBadHex = errors.New("bad hex value")

type rpcQuery struct {
	method string
	params []any
}

func callInto(c *rpcClient, q rpcQuery, out any) error {
	raw, err := c.call(q.method, q.params...)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func parseAddr(s string) (primitives.Address, error) {
	b, err := decodeHex(s)
	if err != nil || len(b) != len(primitives.Address{}) {
		return primitives.Address{}, errBadHex
	}
	return primitives.Address(b), nil
}

func parseHash(s string) (primitives.Hash, error) {
	b, err := decodeHex(s)
	if err != nil || len(b) > len(primitives.Hash{}) {
		return primitives.Hash{}, errBadHex
	}
	var h primitives.Hash
	copy(h[len(h)-len(b):], b)
	return h, nil
}
