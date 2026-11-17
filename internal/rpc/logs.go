package rpc

import (
	"bytes"
	"encoding/json"
	"strconv"
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/kapetan-io/tiger/assert"
)

const (
	logsRangeMax   = 100_000
	logsResultMax  = 20_000
	filtersMax     = 1 << 12
	topicsMax      = 4
	filterIDLength = 16
)

const (
	_ = uint(logsRangeMax - logsResultMax)
	_ = uint(logsResultMax - filtersMax)
	_ = uint(filtersMax - topicsMax)
	_ = uint(filterIDLength - topicsMax)
)

type logFilter struct {
	from      blockRef
	to        blockRef
	hasHash   bool
	hash      primitives.Hash
	addresses []primitives.Address
	topics    [][]primitives.Hash
}

type filterObject struct {
	FromBlock json.RawMessage   `json:"fromBlock"`
	ToBlock   json.RawMessage   `json:"toBlock"`
	BlockHash json.RawMessage   `json:"blockHash"`
	Address   json.RawMessage   `json:"address"`
	Topics    []json.RawMessage `json:"topics"`
}

func filterParam(raw json.RawMessage) (logFilter, error) {
	f := logFilter{from: blockRef{kind: tagLatest}, to: blockRef{kind: tagLatest}}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return f, typeError(raw, "struct Filter")
	}
	var obj filterObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return f, invalidParams("invalid filter" + column(raw))
	}
	if len(obj.Topics) > topicsMax {
		return f, invalidParams("too many topics" + column(raw))
	}
	if err := f.setRange(&obj); err != nil {
		return f, err
	}
	addrs, err := addressSet(obj.Address)
	if err != nil {
		return f, err
	}
	f.addresses = addrs
	for _, t := range obj.Topics {
		set, err := topicSet(t)
		if err != nil {
			return f, err
		}
		f.topics = append(f.topics, set)
	}
	return f, nil
}

func (f *logFilter) setRange(obj *filterObject) error {
	if !isNull(obj.BlockHash) {
		h, err := hashParam(obj.BlockHash)
		if err != nil {
			return err
		}
		f.hasHash, f.hash = true, h
	}
	if !isNull(obj.FromBlock) {
		ref, err := blockNumberParam(obj.FromBlock)
		if err != nil {
			return err
		}
		f.from = ref
	}
	if !isNull(obj.ToBlock) {
		ref, err := blockNumberParam(obj.ToBlock)
		if err != nil {
			return err
		}
		f.to = ref
	}
	return nil
}

func rawList(raw json.RawMessage) ([]json.RawMessage, error) {
	if isNull(raw) {
		return nil, nil
	}
	raw = bytes.TrimSpace(raw)
	if raw[0] != '[' {
		return []json.RawMessage{raw}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, invalidParams("invalid list" + column(raw))
	}
	return items, nil
}

func addressSet(raw json.RawMessage) ([]primitives.Address, error) {
	items, err := rawList(raw)
	if err != nil {
		return nil, err
	}
	out := make([]primitives.Address, 0, len(items))
	for _, item := range items {
		a, err := addressParam(item)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func topicSet(raw json.RawMessage) ([]primitives.Hash, error) {
	items, err := rawList(raw)
	if err != nil {
		return nil, err
	}
	out := make([]primitives.Hash, 0, len(items))
	for _, item := range items {
		if isNull(item) {
			continue
		}
		h, err := hashParam(item)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func (f *logFilter) matches(l *types.Log) bool {
	if len(f.addresses) > 0 && !containsAddress(f.addresses, l.Address) {
		return false
	}
	for i, set := range f.topics {
		if len(set) == 0 {
			continue
		}
		if i >= len(l.Topics) {
			return false
		}
		if !containsHash(set, l.Topics[i]) {
			return false
		}
	}
	return true
}

func containsAddress(set []primitives.Address, a primitives.Address) bool {
	for _, x := range set {
		if x == a {
			return true
		}
	}
	return false
}

func containsHash(set []primitives.Hash, h primitives.Hash) bool {
	for _, x := range set {
		if x == h {
			return true
		}
	}
	return false
}

func (f *logFilter) bloomMatches(bloom *types.Bloom) bool {
	if len(f.addresses) > 0 && !anyAddressInBloom(bloom, f.addresses) {
		return false
	}
	for _, set := range f.topics {
		if len(set) > 0 && !anyTopicInBloom(bloom, set) {
			return false
		}
	}
	return true
}

func anyAddressInBloom(bloom *types.Bloom, set []primitives.Address) bool {
	for _, a := range set {
		if bloomHas(bloom, a[:]) {
			return true
		}
	}
	return false
}

func anyTopicInBloom(bloom *types.Bloom, set []primitives.Hash) bool {
	for _, h := range set {
		if bloomHas(bloom, h[:]) {
			return true
		}
	}
	return false
}

func bloomHas(bloom *types.Bloom, data []byte) bool {
	var probe types.Bloom
	probe.Add(data)
	for i := range probe {
		if probe[i]&bloom[i] != probe[i] {
			return false
		}
	}
	return true
}

type logSink struct {
	w     *writer
	count int
}

func (k *logSink) block(f *logFilter, b *BlockData) error {
	if b.Receipts == nil {
		return nil
	}
	if !f.bloomMatches(&b.Block.Header.Inner.LogsBloom) {
		return nil
	}
	logIndex := uint64(0)
	for i := range b.Receipts {
		r := &b.Receipts[i]
		for j := range r.Logs {
			if !f.matches(&r.Logs[j]) {
				continue
			}
			if k.count >= logsResultMax {
				return newError(CodeServer, "query exceeds max results "+strconv.Itoa(logsResultMax))
			}
			writeLog(k.w, logPos{b: b, txIndex: i, logIndex: logIndex + uint64(j)}, &r.Logs[j])
			k.count++
		}
		logIndex += uint64(len(r.Logs))
	}
	return nil
}

type blockSpan struct {
	from  uint64
	count uint64
}

func (s *Server) logRange(f *logFilter) (blockSpan, error) {
	head := s.chain.Head()
	from, ok := s.resolve(f.from)
	if !ok {
		from = head
	}
	to, ok := s.resolve(f.to)
	if !ok {
		to = head
	}
	if from > to {
		return blockSpan{}, newError(CodeInvalidParams, "invalid block range params")
	}
	to = min(to, head)
	if to < from {
		return blockSpan{from: from}, nil
	}
	if to-from >= logsRangeMax {
		return blockSpan{}, newError(CodeInvalidParams, "query exceeds max block range "+strconv.Itoa(logsRangeMax))
	}
	return blockSpan{from: from, count: min(to-from+1, logsRangeMax)}, nil
}

func (s *Server) collectLogs(f *logFilter, w *writer) error {
	sink := &logSink{w: w}
	w.arrOpen()
	defer w.arrClose()
	if f.hasHash {
		n, ok := s.chain.BlockNumber(f.hash)
		if !ok {
			return blockNotFound("hash " + f.hash.Hex())
		}
		b, ok := s.chain.Block(n)
		if !ok {
			return blockNotFound("hash " + f.hash.Hex())
		}
		return sink.block(f, b)
	}
	span, err := s.logRange(f)
	if err != nil {
		return err
	}
	for k := range span.count {
		b, ok := s.chain.Block(span.from + k)
		if !ok {
			continue
		}
		if err := sink.block(f, b); err != nil {
			return err
		}
	}
	return nil
}

func ethGetLogs(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("filter")
	if err != nil {
		return nil, err
	}
	f, err := filterParam(raw)
	if err != nil {
		return nil, err
	}
	w := newWriter(responseHint)
	if err := s.collectLogs(&f, w); err != nil {
		return nil, err
	}
	return w.bytes(), nil
}

type filterKind int

const (
	filterLogs filterKind = iota
	filterBlocks
	filterPending
)

type installed struct {
	kind filterKind
	logs logFilter
	next uint64
}

type filterTable struct {
	mu      sync.Mutex
	counter uint64
	byID    map[string]*installed
}

func newFilterTable() *filterTable {
	return &filterTable{byID: map[string]*installed{}}
}

func (t *filterTable) add(f *installed) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.byID) >= filtersMax {
		return nil, newError(CodeServer, "too many installed filters")
	}
	t.counter++
	key := "0x" + strconv.FormatUint(t.counter, 16)
	t.byID[key] = f
	return stringResult(key), nil
}

type filterCursor struct {
	f    installed
	from uint64
}

func (t *filterTable) take(id string, head uint64) (filterCursor, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.byID[id]
	if !ok {
		return filterCursor{}, false
	}
	c := filterCursor{f: *f, from: f.next}
	f.next = head + 1
	return c, true
}

func (t *filterTable) get(id string) (installed, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.byID[id]
	if !ok {
		return installed{}, false
	}
	return *f, true
}

func (t *filterTable) remove(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.byID[id]
	delete(t.byID, id)
	return ok
}

func ethNewFilter(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("filter")
	if err != nil {
		return nil, err
	}
	f, err := filterParam(raw)
	if err != nil {
		return nil, err
	}
	return s.filters.add(&installed{kind: filterLogs, logs: f, next: s.chain.Head() + 1})
}

func ethNewBlockFilter(s *Server, _ *params) ([]byte, error) {
	return s.filters.add(&installed{kind: filterBlocks, next: s.chain.Head() + 1})
}

func ethNewPendingFilter(s *Server, _ *params) ([]byte, error) {
	return s.filters.add(&installed{kind: filterPending})
}

func filterIDArg(p *params) (string, error) {
	raw, err := p.required("id")
	if err != nil {
		return "", err
	}
	return stringParam(raw, "a string")
}

func filterNotFound() *Error { return newError(CodeServer, "filter not found") }

func ethGetFilterChanges(s *Server, p *params) ([]byte, error) {
	id, err := filterIDArg(p)
	if err != nil {
		return nil, err
	}
	head := s.chain.Head()
	cur, ok := s.filters.take(id, head)
	if !ok {
		return nil, filterNotFound()
	}
	f, from := cur.f, cur.from
	if from > head {
		return resultEmpty, nil
	}
	count := min(head+1-from, logsRangeMax)
	switch f.kind {
	case filterPending:
		return resultEmpty, nil
	case filterBlocks:
		return encode(func(w *writer) {
			w.arrOpen()
			for k := range count {
				if b, ok := s.chain.Block(from + k); ok {
					w.hash(b.Hash)
				}
			}
			w.arrClose()
		}), nil
	case filterLogs:
		lf := f.logs
		lf.from = blockRef{kind: tagNumber, number: from}
		lf.to = blockRef{kind: tagNumber, number: from + count - 1}
		w := newWriter(responseHint)
		if err := s.collectLogs(&lf, w); err != nil {
			return nil, err
		}
		return w.bytes(), nil
	default:
		assert.Unreachable("filterKind: unhandled value")
	}
	return nil, filterNotFound()
}

func ethGetFilterLogs(s *Server, p *params) ([]byte, error) {
	id, err := filterIDArg(p)
	if err != nil {
		return nil, err
	}
	f, ok := s.filters.get(id)
	if !ok {
		return nil, filterNotFound()
	}
	if f.kind != filterLogs {
		return nil, filterNotFound()
	}
	w := newWriter(responseHint)
	if err := s.collectLogs(&f.logs, w); err != nil {
		return nil, err
	}
	return w.bytes(), nil
}

func ethUninstallFilter(s *Server, p *params) ([]byte, error) {
	id, err := filterIDArg(p)
	if err != nil {
		return nil, err
	}
	if s.filters.remove(id) {
		return resultTrue, nil
	}
	return resultFalse, nil
}
