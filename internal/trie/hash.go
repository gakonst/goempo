package trie

import (
	"bytes"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/kapetan-io/tiger/assert"
)

const (
	parallelMin     = 256
	parallelWideMin = 4096
	workersMax      = 32
	tasksMax        = 256
	hashersCached   = 2 * workersMax
	queueCount      = 4
	heightsMax      = 2*nibblesMax + 2
)

const _ = uint(parallelWideMin - parallelMin)

const _ = uint(tasksMax - 16*16)

const _ = uint(tasksMax - workersMax)

const _ = uint(heightsMax - nibblesMax - 1)

type encodingRetention bool

const (
	keepEncodings encodingRetention = true
	dropEncodings encodingRetention = false
)

var refHashers = make(chan *refHasher, hashersCached)

func takeRefHasher() *refHasher {
	select {
	case h := <-refHashers:
		return h
	default:
		return newRefHasher()
	}
}

func returnRefHasher(h *refHasher) {
	select {
	case refHashers <- h:
	default:
	}
}

func (t *Trie) Hash() primitives.Hash {
	if t.root == nil {
		return EmptyRoot
	}
	t.computeRootRefs()
	return t.rootHash()
}

func (t *Trie) rootHash() primitives.Hash {
	if t.root.hashRef() {
		return primitives.Hash(t.root.ref())
	}
	return t.keccak().Hash(t.root.ref())
}

func (t *Trie) keccak() *keccak.Hasher {
	if t.hasher == nil {
		t.hasher = keccak.New()
	}
	return t.hasher
}

func (t *Trie) retention() encodingRetention {
	if t.reader != nil {
		return keepEncodings
	}
	return dropEncodings
}

func (t *Trie) Commit(w NodeWriter) primitives.Hash {
	if t.root == nil {
		return EmptyRoot
	}
	t.computeRootRefs()
	if !t.root.saved {
		t.save(w)
	}
	return t.rootHash()
}

func (t *Trie) save(w NodeWriter) {
	stack := append(t.scratch[:0], nodeVisit{n: t.root})
	limit := min(t.nodes+1, nodesMax+1)
	steps := 0
	for ; steps < limit; steps++ {
		if len(stack) == 0 {
			break
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := top.n
		if n.hashRef() || n == t.root {
			encoded := n.enc
			if encoded == nil {
				encoded = t.encode(n)
			}
			w.WriteNode(StoredNode{Path: top.path, Encoded: encoded})
		}
		n.enc = nil
		n.saved = true
		children := 0
		for _, child := range n.children {
			if child != nil && !child.saved {
				children++
			}
		}
		if children == 0 {
			continue
		}
		width := len(top.path) + 1
		if n.kind == kindExtension {
			width = len(top.path) + len(n.path)
		}
		paths := make([]byte, 0, children*width)
		for i, child := range n.children {
			if child == nil || child.saved {
				continue
			}
			start := len(paths)
			paths = append(paths, top.path...)
			if n.kind == kindExtension {
				paths = append(paths, n.path...)
			} else {
				paths = append(paths, byte(i))
			}
			stack = append(stack, nodeVisit{n: child, path: paths[start:len(paths):len(paths)]})
		}
	}
	assert.Ok(len(stack) == 0, "trie save visited more nodes than the trie holds")
	clear(stack[:cap(stack)])
	t.scratch = stack[:0]
}

func (t *Trie) encode(n *node) []byte {
	size := nodeSize(n)
	e := nodeEncoder{buf: make([]byte, 0, size)}
	e.node(n)
	assert.Equal(len(e.buf), size, "trie node encoding size")
	return e.buf
}

func (t *Trie) Evict(keepDepth int) {
	if t.root == nil {
		return
	}
	assert.Ok(t.reader != nil, "trie Evict needs a reader to load evicted nodes back")
	type visit struct {
		n     *node
		depth int
	}
	stack := []visit{{n: t.root}}
	limit := min(t.nodes+1, nodesMax+1)
	steps := 0
	for ; steps < limit; steps++ {
		if len(stack) == 0 {
			break
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for i, child := range top.n.children {
			if child == nil || child.kind == kindSealed {
				continue
			}
			if top.depth+1 >= keepDepth && child.saved && child.hashRef() {
				t.dropSubtree(child)
				t.nodes++
				top.n.children[i] = sealedNode(child.ref(), nodeStored)
				continue
			}
			stack = append(stack, visit{n: child, depth: top.depth + 1})
		}
	}
	assert.Ok(len(stack) == 0, "trie Evict visited more nodes than the trie holds")
}

func (t *Trie) sealBelow(n *node, index byte) {
	limit := min(int(index), len(n.children))
	for i := 0; i < limit; i++ {
		child := n.children[i]
		if child == nil {
			continue
		}
		if child.kind == kindSealed {
			continue
		}
		t.computeRefs(child)
		t.dropSubtree(child)
		t.nodes++
		n.children[i] = sealedNode(child.ref(), nodeUnstored)
	}
}

func (t *Trie) dropSubtree(root *node) {
	stack := append(t.scratch[:0], nodeVisit{n: root})
	limit := min(t.nodes+1, nodesMax+1)
	steps := 0
	for ; steps < limit; steps++ {
		if len(stack) == 0 {
			break
		}
		n := stack[len(stack)-1].n
		stack = stack[:len(stack)-1]
		t.drop()
		if n.kind == kindSealed {
			continue
		}
		for _, child := range n.children {
			if child != nil {
				stack = append(stack, nodeVisit{n: child})
			}
		}
	}
	assert.Ok(len(stack) == 0, "trie subtree has more nodes than the trie")
	clear(stack[:cap(stack)])
	t.scratch = stack[:0]
}

func (t *Trie) computeRefs(root *node) {
	if root.hasRef() {
		return
	}
	h := takeRefHasher()
	h.computeRefs(root, refsInput{nodes: t.nodes, retention: t.retention()})
	returnRefHasher(h)
}

func (t *Trie) computeRootRefs() {
	if t.root.hasRef() {
		return
	}
	if t.pending >= parallelMin && runtime.GOMAXPROCS(0) > 1 {
		depth := 1
		if t.pending >= parallelWideMin {
			depth = 2
		}
		t.parallelRefs(depth)
	}
	t.computeRefs(t.root)
	t.pending = 0
}

func (t *Trie) parallelRefs(depth int) {
	assert.Ok(depth >= 1, "trie parallel split depth at least one")
	assert.Ok(depth <= 2, "trie parallel split depth at most two")
	tasks := []*node{t.root}
	for d := 0; d < depth; d++ {
		next := splitDirty(tasks)
		if len(next) == 0 {
			return
		}
		tasks = next
	}
	if len(tasks) < 2 {
		return
	}
	assert.Ok(len(tasks) <= tasksMax, "trie parallel tasks over tasksMax")
	in := refsInput{nodes: t.nodes, retention: t.retention()}
	workers := min(len(tasks), runtime.GOMAXPROCS(0), workersMax)
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			h := takeRefHasher()
			for range len(tasks) {
				i := int(next.Add(1)) - 1
				if i >= len(tasks) {
					break
				}
				h.computeRefs(tasks[i], in)
			}
			returnRefHasher(h)
		})
	}
	wg.Wait()
}

func splitDirty(nodes []*node) []*node {
	assert.Ok(len(nodes) <= 16, "trie split of more than one level")
	out := make([]*node, 0, 16*len(nodes))
	for _, n := range nodes {
		b := n
		if b.kind == kindExtension {
			b = b.children[0]
		}
		if b == nil || b.hasRef() || b.kind != kindBranch {
			continue
		}
		for _, child := range b.children {
			if child != nil && !child.hasRef() && child.kind != kindLeaf {
				out = append(out, child)
			}
		}
	}
	return out
}

type refsInput struct {
	nodes     int
	retention encodingRetention
}

type hashQueue struct {
	nodes []*node
	msgs  [][]byte
	out   [keccak.BatchLanes]primitives.Hash
}

type refHasher struct {
	single *keccak.Hasher
	batch  *keccak.Batch
	stack  []nodeVisit
	levels [][]*node
	temp   nodeEncoder
	queues [queueCount]hashQueue
}

func newRefHasher() *refHasher {
	h := &refHasher{single: keccak.New(), batch: keccak.NewBatch()}
	for i := range h.queues {
		h.queues[i].nodes = make([]*node, 0, keccak.BatchLanes)
		h.queues[i].msgs = make([][]byte, 0, keccak.BatchLanes)
	}
	return h
}

func (h *refHasher) computeRefs(root *node, in refsInput) {
	if root.hasRef() {
		return
	}
	top := min(h.sortByHeight(root, in.nodes), heightsMax-1)
	for height := 0; height <= top; height++ {
		level := h.levels[height]
		for _, n := range level {
			h.encodeLevelNode(n, in.retention)
		}
		h.flush(in.retention)
		h.temp.buf = h.temp.buf[:0]
		clear(level)
		h.levels[height] = level[:0]
	}
	assert.Ok(root.hasRef(), "trie root left without a reference")
}

func (h *refHasher) sortByHeight(root *node, nodes int) int {
	stack := append(h.stack[:0], nodeVisit{n: root})
	limit := min(2*nodes+2, 2*nodesMax+2)
	steps := 0
	for ; steps < limit; steps++ {
		if len(stack) == 0 {
			break
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := top.n
		if top.exit {
			height := 0
			for _, child := range n.children {
				if child != nil && !child.hasRef() {
					height = max(height, int(child.height)+1)
				}
			}
			assert.Ok(height < heightsMax, "trie deeper than heightsMax")
			n.height = uint16(height)
			if height >= len(h.levels) {
				h.levels = append(h.levels, make([][]*node, height+1-len(h.levels))...)
			}
			h.levels[height] = append(h.levels[height], n)
			continue
		}
		assert.Ok(n.kind != kindSealed, "trie sealed node without a reference")
		stack = append(stack, nodeVisit{n: n, exit: true})
		for _, child := range n.children {
			if child != nil && !child.hasRef() {
				stack = append(stack, nodeVisit{n: child})
			}
		}
	}
	assert.Ok(len(stack) == 0, "trie hash visited more nodes than the trie holds")
	clear(stack[:cap(stack)])
	h.stack = stack[:0]
	return int(root.height)
}

func (h *refHasher) encodeLevelNode(n *node, retention encodingRetention) {
	size := nodeSize(n)
	var msg []byte
	if retention == keepEncodings && size >= primitives.HashLength {
		e := nodeEncoder{buf: make([]byte, 0, size)}
		e.node(n)
		msg = e.buf
	} else {
		start := len(h.temp.buf)
		h.temp.node(n)
		msg = h.temp.buf[start:len(h.temp.buf):len(h.temp.buf)]
	}
	assert.Equal(len(msg), size, "trie node encoding size")
	if size < primitives.HashLength {
		n.setRef(msg)
		h.temp.buf = h.temp.buf[:len(h.temp.buf)-size]
		return
	}
	q := &h.queues[min(size/keccak.BlockSize, queueCount-1)]
	q.nodes = append(q.nodes, n)
	q.msgs = append(q.msgs, msg)
	if len(q.msgs) == keccak.BatchLanes {
		h.flushQueue(q, retention)
	}
}

func (h *refHasher) flush(retention encodingRetention) {
	for i := range h.queues {
		h.flushQueue(&h.queues[i], retention)
	}
}

func (h *refHasher) flushQueue(q *hashQueue, retention encodingRetention) {
	count := len(q.msgs)
	if count == 0 {
		return
	}
	if count == 1 {
		h.single.Reset()
		h.single.Write(q.msgs[0])
		h.single.SumInto(&q.out[0])
	} else {
		h.batch.Hash(q.msgs, q.out[:count])
	}
	for i := 0; i < count; i++ {
		n := q.nodes[i]
		n.setRef(q.out[i][:])
		if retention == keepEncodings {
			n.enc = q.msgs[i]
		}
	}
	clear(q.nodes[:count])
	clear(q.msgs[:count])
	q.nodes = q.nodes[:0]
	q.msgs = q.msgs[:0]
}

func nodeSize(n *node) int {
	payload := nodePayload(n)
	return headerSize(payload) + payload
}

func nodePayload(n *node) int {
	payload := 0
	switch n.kind {
	case kindLeaf:
		payload = compactSize(len(n.path)) + stringSize(n.value)
	case kindExtension:
		payload = compactSize(len(n.path)) + refSize(n.children[0])
	case kindBranch:
		for _, child := range n.children {
			payload += refSize(child)
		}
		payload += stringSize(n.value)
	case kindSealed:
		assert.Fail("trie encode of a sealed node")
	default:
		assert.Unreachable("trie node kind")
	}
	return payload
}

type nodeEncoder struct {
	buf []byte
}

func (e *nodeEncoder) node(n *node) {
	e.header(0xc0, nodePayload(n))
	switch n.kind {
	case kindLeaf:
		e.compact(n.path, pathLeaf)
		e.str(n.value)
	case kindExtension:
		e.compact(n.path, pathExtension)
		e.ref(n.children[0])
	case kindBranch:
		for _, child := range n.children {
			e.ref(child)
		}
		e.str(n.value)
	case kindSealed:
		assert.Fail("trie encode of a sealed node")
	default:
		assert.Unreachable("trie node kind")
	}
}

func refSize(child *node) int {
	if child == nil {
		return 1
	}
	assert.Ok(child.hasRef(), "trie child hashed after its parent")
	if child.hashRef() {
		return 1 + primitives.HashLength
	}
	return int(child.refLen)
}

func (e *nodeEncoder) ref(child *node) {
	if child == nil {
		e.buf = append(e.buf, 0x80)
		return
	}
	if child.hashRef() {
		e.buf = append(e.buf, 0x80+primitives.HashLength)
	}
	e.buf = append(e.buf, child.ref()...)
}

func stringSize(b []byte) int {
	if len(b) == 1 && b[0] < 0x80 {
		return 1
	}
	return headerSize(len(b)) + len(b)
}

func (e *nodeEncoder) str(b []byte) {
	if len(b) == 1 && b[0] < 0x80 {
		e.buf = append(e.buf, b[0])
		return
	}
	e.header(0x80, len(b))
	e.buf = append(e.buf, b...)
}

func compactSize(nibbles int) int {
	size := 1 + nibbles/2
	if size == 1 {
		return 1
	}
	return headerSize(size) + size
}

func (e *nodeEncoder) compact(path []byte, kind pathKind) {
	size := 1 + len(path)/2
	if size > 1 {
		e.header(0x80, size)
	}
	e.buf = appendHexPrefix(e.buf, kind, path)
}

func headerSize(size int) int {
	if size <= 55 {
		return 1
	}
	return 1 + uintBytes(size)
}

func (e *nodeEncoder) header(offset byte, size int) {
	if size <= 55 {
		e.buf = append(e.buf, offset+byte(size))
		return
	}
	n := uintBytes(size)
	e.buf = append(e.buf, offset+55+byte(n))
	for k := n - 1; k >= 0; k-- {
		e.buf = append(e.buf, byte(size>>(8*k)))
	}
}

func uintBytes(v int) int {
	n := 0
	for rest := v; rest > 0 && n < 8; rest >>= 8 {
		n++
	}
	return n
}

func (t *Trie) resolve(slot **node, path []byte) error {
	n := *slot
	if n == nil || n.kind != kindSealed {
		return nil
	}
	assert.Ok(t.reader != nil, "trie reached a sealed node and has no reader")
	assert.Ok(n.hashRef(), "trie sealed node without a hash")
	encoded, err := t.reader.ReadNode(path)
	if err != nil {
		return err
	}
	if t.keccak().Hash(encoded) != primitives.Hash(n.ref()) {
		return ErrNodeHash
	}
	tree, err := decodeNode(encoded)
	if err != nil {
		return err
	}
	tree.root.setRef(n.ref())
	t.nodes += tree.count - 1
	*slot = tree.root
	return nil
}

type embedded struct {
	raw  []byte
	slot **node
}

type decodedTree struct {
	root  *node
	count int
}

type decoded struct {
	n     *node
	inner []embedded
}

func decodeNode(encoded []byte) (decodedTree, error) {
	var top *node
	work := []embedded{{raw: encoded, slot: &top}}
	count := 0
	limit := len(encoded) + 1
	steps := 0
	for ; steps < limit; steps++ {
		if len(work) == 0 {
			break
		}
		item := work[len(work)-1]
		work = work[:len(work)-1]
		d, err := decodeOne(item.raw)
		if err != nil {
			return decodedTree{}, err
		}
		n := d.n
		n.saved = true
		if item.slot != &top {
			n.setRef(item.raw)
		}
		*item.slot = n
		count++
		for _, e := range d.inner {
			if e.raw == nil {
				count++
				continue
			}
			work = append(work, e)
		}
	}
	if len(work) != 0 {
		return decodedTree{}, ErrNodeRLP
	}
	return decodedTree{root: top, count: count}, nil
}

func decodeOne(raw []byte) (decoded, error) {
	s := rlp.NewStream(raw)
	list, err := s.List()
	if err != nil {
		return decoded{}, ErrNodeRLP
	}
	if s.Finish() != nil {
		return decoded{}, ErrNodeRLP
	}
	items, err := list.Count()
	if err != nil {
		return decoded{}, ErrNodeRLP
	}
	if items == 17 {
		return decodeBranch(&list)
	}
	if items != 2 {
		return decoded{}, ErrNodeRLP
	}
	compact, err := list.Bytes()
	if err != nil {
		return decoded{}, ErrNodeRLP
	}
	cp, ok := decodeHexPrefix(compact)
	if !ok {
		return decoded{}, ErrNodeRLP
	}
	if cp.kind == pathLeaf {
		value, err := list.Bytes()
		if err != nil || len(value) == 0 {
			return decoded{}, ErrNodeRLP
		}
		return decoded{n: &node{kind: kindLeaf, path: cp.path, value: bytes.Clone(value)}}, nil
	}
	if len(cp.path) == 0 {
		return decoded{}, ErrNodeRLP
	}
	n := &node{kind: kindExtension, path: cp.path}
	e, err := decodeRef(&list, &n.children[0])
	if err != nil {
		return decoded{}, ErrNodeRLP
	}
	if n.children[0] == nil && e.raw == nil {
		return decoded{}, ErrNodeRLP
	}
	return decoded{n: n, inner: []embedded{e}}, nil
}

func decodeBranch(list *rlp.Stream) (decoded, error) {
	n := &node{kind: kindBranch}
	var inner []embedded
	for i := range n.children {
		e, err := decodeRef(list, &n.children[i])
		if err != nil {
			return decoded{}, err
		}
		if e.raw != nil || n.children[i] != nil {
			inner = append(inner, e)
		}
	}
	value, err := list.Bytes()
	if err != nil {
		return decoded{}, ErrNodeRLP
	}
	if len(value) > 0 {
		n.value = bytes.Clone(value)
	}
	return decoded{n: n, inner: inner}, nil
}

func decodeRef(list *rlp.Stream, slot **node) (embedded, error) {
	kind, err := list.Kind()
	if err != nil {
		return embedded{}, ErrNodeRLP
	}
	if kind == rlp.KindList {
		raw, err := list.Raw()
		if err != nil || len(raw) >= primitives.HashLength {
			return embedded{}, ErrNodeRLP
		}
		return embedded{raw: raw, slot: slot}, nil
	}
	ref, err := list.Bytes()
	if err != nil {
		return embedded{}, ErrNodeRLP
	}
	if len(ref) == 0 {
		return embedded{}, nil
	}
	if len(ref) != primitives.HashLength {
		return embedded{}, ErrNodeRLP
	}
	*slot = sealedNode(ref, nodeStored)
	return embedded{}, nil
}
