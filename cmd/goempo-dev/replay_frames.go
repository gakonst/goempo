package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/rpcexec"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

const (
	framesPrintMax = 16
	framesMax      = 1 << 16
	framesTxsMax   = 4
)

const _ = uint(1<<20 - framesMax)

const _ = uint(framesMax - framesPrintMax - framesTxsMax)

var errFramesTooMany = errors.New("diagnose: call tree has more than framesMax frames")

type callFrame struct {
	Type    string      `json:"type"`
	To      string      `json:"to"`
	Input   string      `json:"input"`
	Gas     string      `json:"gas"`
	GasUsed string      `json:"gasUsed"`
	Error   string      `json:"error"`
	Calls   []callFrame `json:"calls"`
}

type txCallTrace struct {
	TxHash string    `json:"txHash"`
	Result callFrame `json:"result"`
}

func (r *replayer) diagnoseFrames(c *rpcClient, blk *types.Block, txs []int) {
	if len(txs) == 0 {
		return
	}
	ours, err := r.traceOwn(blk)
	if err != nil {
		fmt.Fprintln(os.Stderr, "diagnose: frames: our trace:", err)
		return
	}
	var want []txCallTrace
	q := rpcQuery{method: "debug_traceBlockByNumber", params: []any{blockTag(blk.Header.Inner.Number), map[string]any{"tracer": "callTracer"}}}
	if err := callInto(c, q, &want); err != nil {
		fmt.Fprintln(os.Stderr, "diagnose: frames: node trace:", err)
		return
	}
	for _, i := range txs[:min(len(txs), framesTxsMax)] {
		if i >= len(ours) || i >= len(want) || ours[i] == nil {
			continue
		}
		var got callFrame
		if err := json.Unmarshal(ours[i], &got); err != nil {
			fmt.Fprintln(os.Stderr, "diagnose: frames:", err)
			continue
		}
		compareFrames(frameTrees{got: &got, want: &want[i].Result, root: fmt.Sprintf("tx%d", i)})
	}
}

func (r *replayer) traceOwn(blk *types.Block) ([]json.RawMessage, error) {
	r.db.State().Discard()
	chain := rpc.NewMemChain()
	n := blk.Header.Inner.Number
	data, err := rpc.NewBlockData(blk, nil, nil)
	if err != nil {
		return nil, err
	}
	chain.Add(data)
	view := &hashChain{MemChain: chain, r: r, number: n}
	x := &rpcexec.Executor{ChainID: hardfork.MainnetChainID, Chain: view, States: &replayState{r: r}, Blocks: r.exec}
	out, err := x.TraceBlock(n, &rpc.TraceOptions{Tracer: "callTracer"})
	r.db.State().Discard()
	return out, err
}

type ourFrame = callFrame

type framePair struct {
	got    *ourFrame
	want   *callFrame
	path   string
	parent int
}

type frameTrees struct {
	got  *ourFrame
	want *callFrame
	root string
}

func framePairs(t frameTrees) ([]framePair, error) {
	pairs := []framePair{{got: t.got, want: t.want, path: t.root, parent: -1}}
	i := 0
	for ; i < len(pairs) && i < framesMax; i++ {
		p := pairs[i]
		kids := min(len(p.got.Calls), len(p.want.Calls))
		if len(pairs)+kids > framesMax {
			return nil, errFramesTooMany
		}
		for k := 0; k < kids; k++ {
			pairs = append(pairs, framePair{got: &p.got.Calls[k], want: &p.want.Calls[k], path: p.path + "." + strconv.Itoa(k), parent: i})
		}
	}
	if i < len(pairs) {
		return nil, errFramesTooMany
	}
	return pairs, nil
}

func frameSame(p *framePair) bool {
	g, w := p.got, p.want
	return g.Gas == w.Gas && g.GasUsed == w.GasUsed && g.To == w.To && g.Error == w.Error && len(g.Calls) == len(w.Calls)
}

func compareFrames(t frameTrees) {
	pairs, err := framePairs(t)
	if err != nil {
		fmt.Fprintln(os.Stderr, "diagnose: frames:", err)
		return
	}
	subtreeSame := make([]bool, len(pairs))
	kidsSame := make([]bool, len(pairs))
	for i := range pairs {
		kidsSame[i] = true
	}
	for i := len(pairs) - 1; i >= 0; i-- {
		subtreeSame[i] = kidsSame[i] && frameSame(&pairs[i])
		if pa := pairs[i].parent; pa >= 0 && !subtreeSame[i] {
			kidsSame[pa] = false
		}
	}
	printed := 0
	for i := 0; i < len(pairs) && printed < framesPrintMax; i++ {
		if subtreeSame[i] {
			continue
		}
		printed++
		mark := ""
		if kidsSame[i] {
			mark = " <- innermost"
		}
		printFrame(&pairs[i], mark)
	}
}

func printFrame(p *framePair, mark string) {
	g, w := p.got, p.want
	fmt.Fprintf(os.Stderr, "diagnose: frame %s %s to %s sel %s: gas %s want %s, gasUsed %s want %s, calls %d want %d, err %q want %q%s\n",
		p.path, w.Type, w.To, selector(w.Input), hexInt(g.Gas), hexInt(w.Gas), hexInt(g.GasUsed), hexInt(w.GasUsed),
		len(g.Calls), len(w.Calls), g.Error, w.Error, mark)
}

func selector(input string) string {
	return input[:min(len(input), 10)]
}

func hexInt(s string) string {
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return s
	}
	return strconv.FormatUint(v, 10)
}

type hashChain struct {
	*rpc.MemChain
	r      *replayer
	number uint64
}

func (h *hashChain) Block(n uint64) (*rpc.BlockData, bool) {
	if n >= h.number {
		return h.MemChain.Block(n)
	}
	if h.number-n > hashWindow {
		return nil, false
	}
	return &rpc.BlockData{Hash: h.r.getHash(n)}, true
}
