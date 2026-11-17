package tracing

import (
	"bytes"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
)

type Locator struct {
	BlockHash   evm.Hash
	BlockNumber uint64
	TxHash      evm.Hash
	TxIndex     int
}

func callTypeName(k evm.CallKind) string {
	switch k {
	case evm.KindCall:
		return "call"
	case evm.KindCallCode:
		return "callcode"
	case evm.KindDelegateCall:
		return "delegatecall"
	case evm.KindStaticCall:
		return "staticcall"
	case evm.KindCreate, evm.KindCreate2:
		return "call"
	default:
		assert.Unreachable("CallKind: unhandled value")
	}
	return "call"
}

func excludedNodes(rec *Recorder) []bool {
	ex := make([]bool, len(rec.Nodes))
	for i := range rec.Nodes {
		n := &rec.Nodes[i]
		if n.Parent >= 0 && ex[n.Parent] {
			ex[i] = true
			continue
		}
		ex[i] = !n.Kind.IsCreate() && isEthPrecompile(n.To) && n.Value.IsZero()
	}
	return ex
}

func ParityJSON(rec *Recorder, info TxInfo, loc Locator) []byte {
	w := &writer{}
	w.arrOpen()
	ex := excludedNodes(rec)
	addrs := make([][]int, len(rec.Nodes))
	var base []int
	if info.AA {
		roots := 0
		for _, r := range rec.Roots {
			if !ex[r] {
				roots++
			}
		}
		writeParitySynthetic(w, roots, loc)
	}
	pos := 0
	for _, r := range rec.Roots {
		if ex[r] {
			continue
		}
		if info.AA {
			addrs[r] = append(append([]int(nil), base...), pos)
			pos++
		} else {
			addrs[r] = []int{}
		}
	}
	for i := range rec.Nodes {
		if ex[i] {
			continue
		}
		n := &rec.Nodes[i]
		kept := 0
		for _, c := range n.Children {
			if ex[c] {
				continue
			}
			addrs[c] = append(append([]int(nil), addrs[i]...), kept)
			kept++
		}
		writeParityNode(w, parityRow{node: n, subtraces: kept, addr: addrs[i], loc: loc})
	}
	w.arrClose()
	return w.buf
}

func writeLocator(w *writer, loc Locator) {
	w.key("blockHash")
	w.hash(loc.BlockHash)
	w.key("blockNumber")
	w.integer(loc.BlockNumber)
}

func writeTxLocator(w *writer, loc Locator) {
	w.key("transactionHash")
	w.hash(loc.TxHash)
	w.key("transactionPosition")
	w.integer(uint64(loc.TxIndex))
}

func writeTraceAddress(w *writer, addr []int) {
	w.key("traceAddress")
	w.arrOpen()
	for _, a := range addr {
		w.integer(uint64(a))
	}
	w.arrClose()
}

func writeParitySynthetic(w *writer, subtraces int, loc Locator) {
	w.objOpen()
	w.key("action")
	w.objOpen()
	w.kAddress("from", evm.Address{})
	w.kStr("callType", "call")
	w.kQuantity("gas", 0)
	w.kData("input", nil)
	w.kAddress("to", evm.Address{})
	w.kQuantity256("value", u256.Int{})
	w.objClose()
	writeLocator(w, loc)
	w.key("result")
	w.objOpen()
	w.kQuantity("gasUsed", 0)
	w.kData("output", nil)
	w.objClose()
	w.key("subtraces")
	w.integer(uint64(subtraces))
	writeTraceAddress(w, nil)
	writeTxLocator(w, loc)
	w.kStr("type", "call")
	w.objClose()
}

type parityRow struct {
	node      *Node
	subtraces int
	addr      []int
	loc       Locator
}

func writeParityNode(w *writer, row parityRow) {
	n, subtraces, addr, loc := row.node, row.subtraces, row.addr, row.loc
	create := n.Kind.IsCreate()
	revert := n.Status == evm.StatusRevert
	w.objOpen()
	w.key("action")
	w.objOpen()
	w.kAddress("from", n.From)
	if create {
		w.kStr("creationMethod", map[bool]string{true: "create2", false: "create"}[n.Kind == evm.KindCreate2])
		w.kQuantity("gas", n.Gas)
		w.kData("init", n.Input)
	} else {
		w.kStr("callType", callTypeName(n.Kind))
		w.kQuantity("gas", n.Gas)
		w.kData("input", n.Input)
		w.kAddress("to", n.To)
	}
	w.kQuantity256("value", n.Value)
	w.objClose()
	writeLocator(w, loc)
	if t, bad := errorFor(n.Status); bad {
		w.kStr("error", t.parity)
	}
	w.key("result")
	switch {
	case !n.Success() && !revert:
		w.raw([]byte("null"))
	case create && !revert:
		w.objOpen()
		w.kAddress("address", n.To)
		w.kData("code", n.Output)
		w.kQuantity("gasUsed", n.GasUsed)
		w.objClose()
	default:
		w.objOpen()
		w.kQuantity("gasUsed", n.GasUsed)
		w.kData("output", n.Output)
		w.objClose()
	}
	w.key("subtraces")
	w.integer(uint64(subtraces))
	writeTraceAddress(w, addr)
	writeTxLocator(w, loc)
	w.kStr("type", map[bool]string{true: "create", false: "call"}[create])
	w.objClose()
}

type accountState struct {
	balance    u256.Int
	hasBalance bool
	nonce      uint64
	code       []byte
	hasCode    bool
	storage    map[evm.Hash]u256.Int
}

func newAccountState(balance u256.Int, nonce uint64, code []byte) accountState {
	s := accountState{balance: balance, hasBalance: true, nonce: nonce, storage: map[evm.Hash]u256.Int{}}
	if len(code) > 0 {
		s.code, s.hasCode = code, true
	}
	return s
}

func (s *accountState) equal(o *accountState) bool {
	if s.hasBalance != o.hasBalance {
		return false
	}
	if s.hasBalance && s.balance != o.balance {
		return false
	}
	if s.nonce != o.nonce || s.hasCode != o.hasCode || !bytes.Equal(s.code, o.code) {
		return false
	}
	if len(s.storage) != len(o.storage) {
		return false
	}
	for _, k := range sortedHashes(s.storage) {
		ov, ok := o.storage[k]
		if !ok {
			return false
		}
		if ov != s.storage[k] {
			return false
		}
	}
	return true
}

func (s *accountState) write(w *writer, addr evm.Address) {
	w.key(field(addr.Hex()))
	w.objOpen()
	if s.hasBalance {
		w.kQuantity256("balance", s.balance)
	}
	if s.hasCode {
		w.kData("code", s.code)
	}
	if s.nonce != 0 {
		w.key("nonce")
		w.integer(s.nonce)
	}
	if len(s.storage) > 0 {
		w.key("storage")
		w.objOpen()
		for _, k := range sortedHashes(s.storage) {
			w.key(field(k.Hex()))
			w.word(s.storage[k])
		}
		w.objClose()
	}
	w.objClose()
}

func PrestateJSON(t *TxTrace) []byte {
	w := &writer{}
	w.objOpen()
	for _, a := range sortedAccounts(t.Accounts) {
		if a.EmptyBefore {
			continue
		}
		s := newAccountState(a.Balance, a.Nonce, a.Code)
		for _, k := range a.Slots {
			s.storage[k] = a.Pre[k]
		}
		s.write(w, a.Address)
	}
	w.objClose()
	return w.buf
}

func PrestateDiffJSON(t *TxTrace) []byte {
	type pair struct {
		addr     evm.Address
		pre      accountState
		post     accountState
		hasPost  bool
		keepPre  bool
		emptyPre bool
	}
	var pairs []pair
	for _, a := range sortedAccounts(t.Accounts) {
		pre := newAccountState(a.Balance, a.Nonce, a.Code)
		post := newAccountState(a.PostBalance, a.PostNonce, a.PostCode)
		if pre.hasCode && !post.hasCode {
			post.code, post.hasCode = []byte{}, true
		}
		if a.Destroyed {
			for _, k := range a.Slots {
				pre.storage[k] = a.Pre[k]
			}
		} else {
			for _, k := range a.Slots {
				o, c := a.Pre[k], a.Post[k]
				if o == c {
					continue
				}
				if !o.IsZero() {
					pre.storage[k] = o
				}
				if !c.IsZero() {
					post.storage[k] = c
				}
			}
		}
		p := pair{addr: a.Address, pre: pre, post: post, hasPost: !a.Destroyed, keepPre: true, emptyPre: a.EmptyBefore}
		if p.hasPost && pre.equal(&post) {
			continue
		}
		if p.hasPost {
			if p.post.hasBalance && p.post.balance == pre.balance {
				p.post.hasBalance = false
			}
			if p.post.nonce == pre.nonce {
				p.post.nonce = 0
			}
			if p.post.hasCode == pre.hasCode && bytes.Equal(p.post.code, pre.code) {
				p.post.hasCode, p.post.code = false, nil
			}
		}
		pairs = append(pairs, p)
	}
	w := &writer{}
	w.objOpen()
	w.key("post")
	w.objOpen()
	for i := range pairs {
		if pairs[i].hasPost {
			pairs[i].post.write(w, pairs[i].addr)
		}
	}
	w.objClose()
	w.key("pre")
	w.objOpen()
	for i := range pairs {
		if !pairs[i].emptyPre {
			pairs[i].pre.write(w, pairs[i].addr)
		}
	}
	w.objClose()
	w.objClose()
	return w.buf
}
