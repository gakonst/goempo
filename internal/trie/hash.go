package trie

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

func (t *Trie) Hash() primitives.Hash {
	if t.root == nil {
		return EmptyRoot
	}
	t.computeRefs(t.root)
	if len(t.root.ref) == primitives.HashLength {
		return primitives.Hash(t.root.ref)
	}
	return t.hasher.Hash(t.root.ref)
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

func (t *Trie) encodeNode(n *node) {
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
		for i := 0; i < len(n.children); i++ {
			t.writeRef(n.children[i])
		}
		t.enc.WriteBytes(n.value)
	case kindSealed:
		assert.Fail("trie encodeNode of a sealed node")
	default:
		assert.Unreachable("trie node kind")
	}
	t.enc.ListEnd()
	encoded := t.enc.Encoded()
	if len(encoded) < primitives.HashLength {
		n.ref = append([]byte(nil), encoded...)
		return
	}
	h := t.hasher.Hash(encoded)
	n.ref = h[:]
}
