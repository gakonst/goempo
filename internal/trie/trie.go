package trie

import (
	"bytes"
	"math"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/kapetan-io/tiger/assert"
)

var EmptyRoot = primitives.Hash{
	0x56, 0xe8, 0x1f, 0x17, 0x1b, 0xcc, 0x55, 0xa6, 0xff, 0x83, 0x45, 0xe6, 0x92, 0xc0, 0xf8, 0x6e,
	0x5b, 0x48, 0xe0, 0x1b, 0x99, 0x6c, 0xad, 0xc0, 0x01, 0x62, 0x2f, 0xb5, 0xe3, 0x63, 0xb4, 0x21,
}

const nodesMax = 1 << 36

const _ = uint(math.MaxInt/4 - nodesMax)

type nodeKind uint8

const (
	kindLeaf      nodeKind = 1
	kindExtension nodeKind = 2
	kindBranch    nodeKind = 3
	kindSealed    nodeKind = 4
)

type node struct {
	kind     nodeKind
	path     []byte
	value    []byte
	children [16]*node
	ref      []byte
}

type Value []byte

type Trie struct {
	root    *node
	nodes   int
	last    []byte
	sorted  int
	enc     rlp.Encoder
	hasher  *keccak.Hasher
	scratch []nodeVisit
}

type nodeVisit struct {
	n    *node
	exit bool
}

func New() *Trie {
	return &Trie{hasher: keccak.New()}
}

func (t *Trie) Nodes() int {
	return t.nodes
}

func (t *Trie) newLeaf(path []byte, value Value) *node {
	assert.Ok(t.nodes < nodesMax, "trie holds nodesMax nodes")
	t.nodes++
	return &node{kind: kindLeaf, path: path, value: bytes.Clone(value)}
}

func (t *Trie) newExtension(path []byte, child *node) *node {
	assert.Ok(t.nodes < nodesMax, "trie holds nodesMax nodes")
	assert.Ok(len(path) > 0, "extension with an empty path")
	t.nodes++
	n := &node{kind: kindExtension, path: path}
	n.children[0] = child
	return n
}

func (t *Trie) newBranch() *node {
	assert.Ok(t.nodes < nodesMax, "trie holds nodesMax nodes")
	t.nodes++
	return &node{kind: kindBranch}
}

func (t *Trie) drop() {
	t.nodes--
	assert.Ok(t.nodes >= 0, "trie node count went negative")
}

func (t *Trie) Get(key []byte) ([]byte, bool) {
	kn := keyNibbles(key)
	n := t.root
	pos := 0
	for steps := 0; steps <= len(kn)+1; steps++ {
		if n == nil {
			return nil, false
		}
		switch n.kind {
		case kindLeaf:
			if bytes.Equal(n.path, kn[pos:]) {
				return n.value, true
			}
			return nil, false
		case kindExtension:
			if !bytes.HasPrefix(kn[pos:], n.path) {
				return nil, false
			}
			pos += len(n.path)
			n = n.children[0]
		case kindBranch:
			if pos == len(kn) {
				return n.value, n.value != nil
			}
			n = n.children[kn[pos]]
			pos++
		case kindSealed:
			assert.Fail("trie Get reached a sealed subtree")
		default:
			assert.Unreachable("trie node kind")
		}
	}
	assert.Fail("trie Get walked past the key")
	return nil, false
}

func (t *Trie) Update(key []byte, value Value) {
	if len(value) == 0 {
		t.Delete(key)
		return
	}
	t.insert(keyNibbles(key), value, nil)
}

func (t *Trie) UpdateSorted(key []byte, value Value) {
	assert.Ok(len(value) > 0, "trie UpdateSorted with an empty value")
	if t.sorted > 0 {
		assert.Ok(bytes.Compare(t.last, key) < 0, "trie UpdateSorted keys must strictly increase")
	}
	t.sorted++
	t.last = append(t.last[:0], key...)
	kn := keyNibbles(key)
	t.insert(kn, value, kn)
}

func (t *Trie) insert(kn []byte, value Value, seal []byte) {
	slot := &t.root
	pos := 0
	for steps := 0; steps <= len(kn)+1; steps++ {
		n := *slot
		if n == nil {
			*slot = t.newLeaf(kn[pos:], value)
			return
		}
		n.ref = nil
		switch n.kind {
		case kindLeaf:
			common := prefixLen(n.path, kn[pos:])
			if common == len(n.path) {
				if common == len(kn)-pos {
					n.value = bytes.Clone(value)
					return
				}
			}
			*slot = t.split(splitInput{old: n, oldPath: n.path, key: kn[pos:], common: common, value: value})
			t.drop()
			return
		case kindExtension:
			common := prefixLen(n.path, kn[pos:])
			if common == len(n.path) {
				pos += common
				slot = &n.children[0]
				continue
			}
			*slot = t.split(splitInput{old: n, oldPath: n.path, key: kn[pos:], common: common, value: value})
			t.drop()
			return
		case kindBranch:
			if pos == len(kn) {
				n.value = bytes.Clone(value)
				return
			}
			if seal != nil {
				t.sealBelow(n, kn[pos])
			}
			slot = &n.children[kn[pos]]
			pos++
		case kindSealed:
			assert.Fail("trie insert reached a sealed subtree")
		default:
			assert.Unreachable("trie node kind")
		}
	}
	assert.Fail("trie insert walked past the key")
}

type splitInput struct {
	old     *node
	oldPath nibblePath
	key     []byte
	common  int
	value   Value
}

func (t *Trie) split(in splitInput) *node {
	branch := t.newBranch()
	c := in.common
	if in.old.kind == kindLeaf {
		if c == len(in.oldPath) {
			branch.value = in.old.value
		} else {
			branch.children[in.oldPath[c]] = t.newLeaf(in.oldPath[c+1:], in.old.value)
		}
	} else {
		assert.Ok(c < len(in.oldPath), "extension split inside its path")
		if c+1 == len(in.oldPath) {
			branch.children[in.oldPath[c]] = in.old.children[0]
		} else {
			branch.children[in.oldPath[c]] = t.newExtension(in.oldPath[c+1:], in.old.children[0])
		}
	}
	if c == len(in.key) {
		branch.value = bytes.Clone(in.value)
	} else {
		branch.children[in.key[c]] = t.newLeaf(in.key[c+1:], in.value)
	}
	if c == 0 {
		return branch
	}
	return t.newExtension(in.key[:c], branch)
}

func (t *Trie) Delete(key []byte) {
	kn := keyNibbles(key)
	parents := make([]**node, 0, len(kn)+2)
	slot := &t.root
	pos := 0
	steps := 0
	for ; steps <= len(kn)+1; steps++ {
		n := *slot
		if n == nil {
			return
		}
		if n.kind == kindLeaf {
			if !bytes.Equal(n.path, kn[pos:]) {
				return
			}
			*slot = nil
			t.drop()
			break
		}
		if n.kind == kindExtension {
			if !bytes.HasPrefix(kn[pos:], n.path) {
				return
			}
			parents = append(parents, slot)
			pos += len(n.path)
			slot = &n.children[0]
			continue
		}
		assert.Equal(n.kind, kindBranch, "trie Delete reached a sealed subtree")
		parents = append(parents, slot)
		if pos == len(kn) {
			if n.value == nil {
				return
			}
			n.value = nil
			break
		}
		slot = &n.children[kn[pos]]
		pos++
	}
	assert.Ok(steps <= len(kn)+1, "trie Delete walked past the key")
	for i := 0; i < len(parents); i++ {
		(*parents[i]).ref = nil
	}
	for i := len(parents) - 1; i >= 0; i-- {
		t.normalize(parents[i])
	}
}

func (t *Trie) normalize(slot **node) {
	n := *slot
	if n.kind == kindExtension {
		child := n.children[0]
		assert.Ok(child != nil, "extension lost its child")
		if child.kind == kindLeaf {
			*slot = t.newLeaf(joinPath(n.path, child.path), child.value)
			t.drop()
			t.drop()
			return
		}
		if child.kind == kindExtension {
			*slot = t.newExtension(joinPath(n.path, child.path), child.children[0])
			t.drop()
			t.drop()
		}
		return
	}
	assert.Equal(n.kind, kindBranch, "trie normalize of a leaf")
	count := 0
	only := -1
	for i := 0; i < len(n.children); i++ {
		if n.children[i] != nil {
			count++
			only = i
		}
	}
	if n.value != nil {
		count++
	}
	assert.Ok(count >= 1, "branch emptied by one delete")
	if count >= 2 {
		return
	}
	if only < 0 {
		*slot = t.newLeaf(nil, n.value)
		t.drop()
		return
	}
	child := n.children[only]
	prefix := nibblePath{byte(only)}
	switch child.kind {
	case kindLeaf:
		*slot = t.newLeaf(joinPath(prefix, child.path), child.value)
		t.drop()
		t.drop()
	case kindExtension:
		*slot = t.newExtension(joinPath(prefix, child.path), child.children[0])
		t.drop()
		t.drop()
	case kindBranch:
		*slot = t.newExtension(prefix, child)
		t.drop()
	case kindSealed:
		assert.Fail("trie Delete needs a sealed subtree")
	default:
		assert.Unreachable("trie node kind")
	}
}
