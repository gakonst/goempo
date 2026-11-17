package tracing

import (
	"strconv"

	"github.com/gakonst/goempo/internal/evm"
)

type opcodeTally struct {
	count uint64
	gas   uint64
}

func OpcodeGasJSON(r *Recorder) []byte {
	var tally [256]opcodeTally
	order := make([]evm.OpCode, 0, len(tally))
	for i := range r.Nodes {
		n := &r.Nodes[i]
		for j := range n.Steps {
			s := &n.Steps[j]
			t := &tally[s.Op]
			if t.count == 0 {
				order = append(order, s.Op)
			}
			t.count++
			t.gas += stepGasUsed(r, s)
		}
	}
	out := []byte{'['}
	for i, op := range order {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"opcode":`...)
		out = strconv.AppendQuote(out, op.String())
		out = append(out, `,"count":`...)
		out = strconv.AppendUint(out, tally[op].count, 10)
		out = append(out, `,"gasUsed":`...)
		out = strconv.AppendUint(out, tally[op].gas, 10)
		out = append(out, '}')
	}
	return append(out, ']')
}

func stepGasUsed(r *Recorder, s *Step) uint64 {
	if s.Status == evm.StatusCall && createsFrame(s.Op) && s.Child != noChild {
		return s.Cost + r.Nodes[s.Child].Gas
	}
	return s.Cost
}
