package trie

import (
	"bytes"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/kapetan-io/tiger/assert"
)

func (t *Trie) Hash() primitives.Hash {
	if t.root == nil {
		return EmptyRoot
	}
	t.computeRefs(t.root)
	return t.rootHash()
}

func (t *Trie) rootHash() primitives.Hash {
	if len(t.root.ref) == primitives.HashLength {
		return primitives.Hash(t.root.ref)
	}
	return t.hasher.Hash(t.root.ref)
}

func (t *Trie) Commit(w NodeWriter) primitives.Hash {
	if t.root == nil {
		return EmptyRoot
	}
	t.computeRefs(t.root)
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
		if len(n.ref) == primitives.HashLength || n == t.root {
			encoded := n.enc
			if encoded == nil {
				encoded = bytes.Clone(t.encodeNode(n))
			}
			w.WriteNode(StoredNode{Path: top.path, Encoded: encoded})
		}
		n.enc = nil
		n.saved = true
		for i, child := range n.children {
			if child == nil || child.saved {
				continue
			}
			childPath := append([]byte(nil), top.path...)
			if n.kind == kindExtension {
				childPath = append(childPath, n.path...)
			} else {
				childPath = append(childPath, byte(i))
			}
			stack = append(stack, nodeVisit{n: child, path: childPath})
		}
	}
	assert.Ok(len(stack) == 0, "trie save visited more nodes than the trie holds")
	t.scratch = stack[:0]
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
			if top.depth+1 >= keepDepth && child.saved && len(child.ref) == primitives.HashLength {
				t.dropSubtree(child)
				t.nodes++
				top.n.children[i] = &node{kind: kindSealed, saved: true, ref: child.ref}
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
		n.children[i] = &node{kind: kindSealed, ref: child.ref}
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
	t.scratch = stack[:0]
}

func (t *Trie) computeRefs(root *node) {
	if root.ref != nil {
		return
	}
	stack := append(t.scratch[:0], nodeVisit{n: root})
	limit := min(2*t.nodes+2, 2*nodesMax+2)
	steps := 0
	for ; steps < limit; steps++ {
		if len(stack) == 0 {
			break
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.exit {
			t.encodeNode(top.n)
			continue
		}
		stack = append(stack, nodeVisit{n: top.n, exit: true})
		for _, child := range top.n.children {
			if child == nil {
				continue
			}
			if child.ref != nil {
				continue
			}
			stack = append(stack, nodeVisit{n: child})
		}
	}
	assert.Ok(len(stack) == 0, "trie hash visited more nodes than the trie holds")
	t.scratch = stack[:0]
}

func (t *Trie) writeRef(child *node) {
	if child == nil {
		t.enc.WriteBytes(nil)
		return
	}
	assert.Ok(child.ref != nil, "trie child hashed after its parent")
	if len(child.ref) < primitives.HashLength {
		t.enc.WriteRaw(child.ref)
		return
	}
	t.enc.WriteBytes(child.ref)
}

func (t *Trie) encodeNode(n *node) []byte {
	t.enc.Reset()
	t.enc.ListStart()
	switch n.kind {
	case kindLeaf:
		t.enc.WriteBytes(hexPrefix(n.path, pathLeaf))
		t.enc.WriteBytes(n.value)
	case kindExtension:
		t.enc.WriteBytes(hexPrefix(n.path, pathExtension))
		t.writeRef(n.children[0])
	case kindBranch:
		for _, child := range n.children {
			t.writeRef(child)
		}
		t.enc.WriteBytes(n.value)
	case kindSealed:
		assert.Fail("trie encodeNode of a sealed node")
	default:
		assert.Unreachable("trie node kind")
	}
	t.enc.ListEnd()
	encoded := t.enc.Encoded()
	if n.ref != nil {
		return encoded
	}
	if len(encoded) < primitives.HashLength {
		n.ref = bytes.Clone(encoded)
		return encoded
	}
	h := t.hasher.Hash(encoded)
	n.ref = h[:]
	if t.reader != nil {
		n.enc = bytes.Clone(encoded)
	}
	return encoded
}

func (t *Trie) resolve(slot **node, path []byte) error {
	n := *slot
	if n == nil || n.kind != kindSealed {
		return nil
	}
	assert.Ok(t.reader != nil, "trie reached a sealed node and has no reader")
	assert.Equal(len(n.ref), primitives.HashLength, "trie sealed node without a hash")
	encoded, err := t.reader.ReadNode(path)
	if err != nil {
		return err
	}
	if t.hasher.Hash(encoded) != primitives.Hash(n.ref) {
		return ErrNodeHash
	}
	tree, err := decodeNode(encoded)
	if err != nil {
		return err
	}
	tree.root.ref = n.ref
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
			n.ref = bytes.Clone(item.raw)
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
	*slot = &node{kind: kindSealed, saved: true, ref: bytes.Clone(ref)}
	return embedded{}, nil
}
