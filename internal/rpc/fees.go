package rpc

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	feeHistoryMax  = 1024
	percentilesMax = 100
	sciLow         = 1e-5
	sciHigh        = 1e16
)

const _ = uint(feeHistoryMax - percentilesMax)

func appendJSONFloat(dst []byte, v float64) []byte {
	if v == 0 {
		return append(dst, '0', '.', '0')
	}
	a := math.Abs(v)
	if a >= sciLow && a < sciHigh {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return append(dst, s...)
	}
	s := strconv.FormatFloat(v, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	sign := ""
	if exp[0] == '-' {
		sign = "-"
	}
	exp = strings.TrimLeft(exp[1:], "0")
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	return append(dst, (mant + "e" + sign + exp)...)
}

type rewardQuery struct {
	percentiles []float64
	want        bool
}

func percentilesParam(raw json.RawMessage) (rewardQuery, error) {
	if raw == nil {
		return rewardQuery{}, nil
	}
	raw = bytes.TrimSpace(raw)
	var ps []float64
	if err := json.Unmarshal(raw, &ps); err != nil {
		return rewardQuery{}, typeError(raw, "a sequence")
	}
	bad := newError(CodeInvalidParams, "invalid reward percentiles")
	if len(ps) > percentilesMax {
		return rewardQuery{}, bad
	}
	for i, pct := range ps {
		if pct < 0 {
			return rewardQuery{}, bad
		}
		if pct > 100 {
			return rewardQuery{}, bad
		}
		if i > 0 && pct < ps[i-1] {
			return rewardQuery{}, bad
		}
	}
	return rewardQuery{percentiles: ps, want: true}, nil
}

func ethFeeHistory(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("block_count")
	if err != nil {
		return nil, err
	}
	count, err := quantityParam(raw)
	if err != nil {
		return nil, err
	}
	raw, err = p.required("newest_block")
	if err != nil {
		return nil, err
	}
	ref, err := blockNumberParam(raw)
	if err != nil {
		return nil, err
	}
	q, err := percentilesParam(p.optional("reward_percentiles"))
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return []byte("{\"gasUsedRatio\":[],\"oldestBlock\":\"0x0\"}"), nil
	}
	if ref.kind == tagPending {
		ref = blockRef{kind: tagLatest}
	}
	newest, err := s.stateBlock(ref)
	if err != nil {
		return nil, err
	}
	count = min(count, feeHistoryMax, newest+1)
	oldest := newest + 1 - count
	blocks := make([]*BlockData, 0, count)
	for k := range count {
		b, ok := s.chain.Block(oldest + k)
		if !ok {
			return nil, blockNotFound("0x" + strconv.FormatUint(oldest+k, 16))
		}
		blocks = append(blocks, b)
	}
	return encode(func(w *writer) { writeFeeHistory(w, blocks, q) }), nil
}

func writeFeeHistory(w *writer, blocks []*BlockData, q rewardQuery) {
	w.objOpen()
	w.key("baseFeePerGas")
	w.arrOpen()
	for _, b := range blocks {
		w.quantity(b.Block.Header.Inner.BaseFeePerGas)
	}
	w.quantity(blocks[len(blocks)-1].Block.Header.Inner.BaseFeePerGas)
	w.arrClose()
	w.key("gasUsedRatio")
	w.arrOpen()
	for _, b := range blocks {
		h := &b.Block.Header.Inner
		ratio := 0.0
		if h.GasLimit > 0 {
			ratio = float64(h.GasUsed) / float64(h.GasLimit)
		}
		w.raw(appendJSONFloat(nil, ratio))
	}
	w.arrClose()
	w.key("baseFeePerBlobGas")
	w.arrOpen()
	for range len(blocks) + 1 {
		w.quantity(blobBaseFee)
	}
	w.arrClose()
	w.key("blobGasUsedRatio")
	w.arrOpen()
	for range blocks {
		w.raw([]byte("0.0"))
	}
	w.arrClose()
	w.kQuantity("oldestBlock", blocks[0].Number())
	if q.want {
		w.key("reward")
		w.arrOpen()
		for _, b := range blocks {
			if len(q.percentiles) > 0 {
				writeRewards(w, b, q.percentiles)
			}
		}
		w.arrClose()
	}
	w.objClose()
}

type txReward struct {
	gas uint64
	tip u256.Int
}

func effectiveTip(tx types.Transaction, baseFee uint64) u256.Int {
	price := effectiveGasPrice(tx, baseFee)
	fee := u256.FromUint64(baseFee)
	if price.Lt(fee) {
		return u256.Zero()
	}
	return price.Sub(fee)
}

func writeRewards(w *writer, b *BlockData, ps []float64) {
	h := &b.Block.Header.Inner
	n := min(len(b.Block.Transactions), len(b.Receipts))
	rewards := make([]txReward, 0, n)
	for i := range n {
		gas := b.Receipts[i].CumulativeGasUsed
		if i > 0 {
			gas -= b.Receipts[i-1].CumulativeGasUsed
		}
		rewards = append(rewards, txReward{gas: gas, tip: effectiveTip(b.Block.Transactions[i], h.BaseFeePerGas)})
	}
	slices.SortStableFunc(rewards, func(x, y txReward) int { return x.tip.Cmp(y.tip) })
	w.arrOpen()
	defer w.arrClose()
	if len(rewards) == 0 {
		for range ps {
			w.quantity(0)
		}
		return
	}
	cumulative := make([]uint64, len(rewards))
	total := uint64(0)
	for i, r := range rewards {
		total += r.gas
		cumulative[i] = total
	}
	for _, pct := range ps {
		threshold := uint64(float64(h.GasUsed) * pct / 100)
		idx := sort.Search(len(cumulative), func(j int) bool { return cumulative[j] >= threshold })
		w.quantity256(rewards[min(idx, len(rewards)-1)].tip)
	}
}
