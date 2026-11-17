package tracing

import (
	"bytes"
	"slices"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/kapetan-io/tiger/assert"
)

type AccessTuple struct {
	Address evm.Address
	Keys    []evm.Hash
}

type AccessSpec struct {
	Initial  []AccessTuple
	Sender   evm.Address
	Excluded func(evm.Address) bool
}

type accessSet struct {
	slots    map[evm.Address]map[evm.Hash]bool
	excluded map[evm.Address]bool
	spec     AccessSpec
}

func (a *accessSet) skip(addr evm.Address) bool {
	return a.excluded[addr] || a.spec.Excluded(addr)
}

func (a *accessSet) add(addr evm.Address) map[evm.Hash]bool {
	m := a.slots[addr]
	if m == nil {
		m = make(map[evm.Hash]bool)
		a.slots[addr] = m
	}
	return m
}

func AccessListOf(rec *Recorder, spec AccessSpec) []AccessTuple {
	a := &accessSet{slots: make(map[evm.Address]map[evm.Hash]bool), excluded: map[evm.Address]bool{spec.Sender: true}, spec: spec}
	for _, t := range spec.Initial {
		m := a.add(t.Address)
		for _, k := range t.Keys {
			m[k] = true
		}
	}
	pruned := false
	for _, root := range rec.Roots {
		a.excluded[rec.Nodes[root].To] = true
		if !pruned {
			pruned = true
			for _, addr := range sortedAddrs(a.slots) {
				if a.skip(addr) {
					delete(a.slots, addr)
				}
			}
		}
		a.collect(rec, root)
	}
	return a.list()
}

func (a *accessSet) collect(rec *Recorder, root int) {
	stack := []stepCursor{{idx: root}}
	for iter := 0; iter < stepsMax && len(stack) > 0; iter++ {
		top := &stack[len(stack)-1]
		n := &rec.Nodes[top.idx]
		if top.next >= len(n.Steps) {
			stack = stack[:len(stack)-1]
			continue
		}
		s := &n.Steps[top.next]
		top.next++
		a.step(n, s)
		if s.Child != noChild {
			stack = append(stack, stepCursor{idx: s.Child})
		}
	}
	assert.Ok(len(stack) == 0, "access list steps within bound")
}

func (a *accessSet) step(n *Node, s *Step) {
	depth := len(s.Stack)
	if touchesSlot(s.Op) && depth >= 1 {
		a.add(execAddress(n))[s.Stack[depth-1].Bytes32()] = true
	}
	if readsAccount(s.Op) && depth >= 1 {
		a.touchAddr(s.Stack[depth-1].Bytes32())
	}
	if callsAccount(s.Op) && depth >= 5 {
		a.touchAddr(s.Stack[depth-2].Bytes32())
	}
}

func readsAccount(op evm.OpCode) bool {
	return op == evm.EXTCODECOPY || op == evm.EXTCODEHASH || op == evm.EXTCODESIZE || op == evm.BALANCE || op == evm.SELFDESTRUCT
}

func callsAccount(op evm.OpCode) bool {
	return op == evm.DELEGATECALL || op == evm.CALL || op == evm.STATICCALL || op == evm.CALLCODE
}

func (a *accessSet) touchAddr(w evm.Hash) {
	addr := evm.Address(w[12:])
	if !a.skip(addr) {
		a.add(addr)
	}
}

func sortedAddrs(m map[evm.Address]map[evm.Hash]bool) []evm.Address {
	out := make([]evm.Address, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.SortFunc(out, func(x, y evm.Address) int { return bytes.Compare(x[:], y[:]) })
	return out
}

func (a *accessSet) list() []AccessTuple {
	out := make([]AccessTuple, 0, len(a.slots))
	for _, addr := range sortedAddrs(a.slots) {
		keys := make([]evm.Hash, 0, len(a.slots[addr]))
		for k := range a.slots[addr] {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(x, y evm.Hash) int { return bytes.Compare(x[:], y[:]) })
		out = append(out, AccessTuple{Address: addr, Keys: keys})
	}
	return out
}
