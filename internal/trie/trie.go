package trie

import (
	"bytes"
	"errors"
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

var (
	ErrNodeHash = errors.New("trie: stored node does not match its hash")
	ErrNodeRLP  = errors.New("trie: stored node is not a valid node encoding")
)

const nodesMax = 1 << 36

const recycledNodesMax = 256

const _ = uint(nodesMax - recycledNodesMax)

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
	saved    bool
	path     []byte
	value    []byte
	children [16]*node
	ref      []byte
	enc      []byte
}

type Value []byte

type NodeReader interface {
	ReadNode(path []byte) ([]byte, error)
}

type StoredNode struct {
	Path    []byte
	Encoded []byte
}

type NodeWriter interface {
	WriteNode(n StoredNode)
}

type Trie struct {
	root    *node
	nodes   int
	last    []byte
	sorted  int
	reader  NodeReader
	enc     rlp.Encoder
	hasher  *keccak.Hasher
	scratch []nodeVisit
	free    []*node
	commit  *commitWorkers
}

type nodeVisit struct {
	n    *node
	exit bool
	path []byte
}

func New() *Trie {
	return &Trie{hasher: keccak.New()}
}

func Open(root primitives.Hash, reader NodeReader) *Trie {
	t := &Trie{hasher: keccak.New(), reader: reader}
	if root != EmptyRoot {
		t.root = &node{kind: kindSealed, saved: true, ref: bytes.Clone(root[:])}
		t.nodes = 1
	}
	return t
}

func (t *Trie) Nodes() int {
	return t.nodes
}

func (t *Trie) newLeaf(path []byte, value Value) *node {
	assert.Ok(t.nodes < nodesMax, "trie holds nodesMax nodes")
	t.nodes++
	n := t.allocNode()
	*n = node{kind: kindLeaf, path: path, value: bytes.Clone(value)}
	return n
}

func (t *Trie) newExtension(path []byte, child *node) *node {
	assert.Ok(t.nodes < nodesMax, "trie holds nodesMax nodes")
	assert.Ok(len(path) > 0, "extension with an empty path")
	t.nodes++
	n := t.allocNode()
	*n = node{kind: kindExtension, path: path}
	n.children[0] = child
	return n
}

func (t *Trie) newBranch() *node {
	assert.Ok(t.nodes < nodesMax, "trie holds nodesMax nodes")
	t.nodes++
	n := t.allocNode()
	n.kind = kindBranch
	return n
}

func (t *Trie) allocNode() *node {
	if len(t.free) == 0 {
		return new(node)
	}
	i := len(t.free) - 1
	n := t.free[i]
	t.free[i] = nil
	t.free = t.free[:i]
	return n
}

func (t *Trie) recycle(n *node) {
	*n = node{}
	if len(t.free) == recycledNodesMax {
		return
	}
	if len(t.free) == cap(t.free) {
		capacity := min(recycledNodesMax, max(1, 2*cap(t.free)))
		free := make([]*node, len(t.free), capacity)
		copy(free, t.free)
		t.free = free
	}
	t.free = append(t.free, n)
}

func (t *Trie) drop() {
	t.nodes--
	assert.Ok(t.nodes >= 0, "trie node count went negative")
}

func dirty(n *node) {
	n.ref = nil
	n.enc = nil
	n.saved = false
}

func (t *Trie) Get(key []byte) ([]byte, bool) {
	v, err := t.Find(key)
	assert.NoError(err, "in-memory trie lookup")
	return v, v != nil
}

func (t *Trie) Find(key []byte) ([]byte, error) {
	kn := keyNibbles(key)
	slot := &t.root
	pos := 0
	for steps := 0; steps <= 2*len(kn)+2; steps++ {
		if err := t.resolve(slot, kn[:pos]); err != nil {
			return nil, err
		}
		n := *slot
		if n == nil {
			return nil, nil
		}
		switch n.kind {
		case kindLeaf:
			if bytes.Equal(n.path, kn[pos:]) {
				return n.value, nil
			}
			return nil, nil
		case kindExtension:
			if !bytes.HasPrefix(kn[pos:], n.path) {
				return nil, nil
			}
			pos += len(n.path)
			slot = &n.children[0]
		case kindBranch:
			if pos == len(kn) {
				return n.value, nil
			}
			slot = &n.children[kn[pos]]
			pos++
		case kindSealed:
			assert.Fail("trie node still sealed after resolve")
		default:
			assert.Unreachable("trie node kind")
		}
	}
	assert.Fail("trie Find walked past the key")
	return nil, nil
}

func (t *Trie) Update(key []byte, value Value) {
	assert.NoError(t.Set(key, value), "in-memory trie update")
}

func (t *Trie) Delete(key []byte) {
	assert.NoError(t.Unset(key), "in-memory trie delete")
}

func (t *Trie) Set(key []byte, value Value) error {
	if len(value) == 0 {
		return t.Unset(key)
	}
	return t.insert(keyNibbles(key), value, nil)
}

func (t *Trie) UpdateSorted(key []byte, value Value) {
	assert.Ok(len(value) > 0, "trie UpdateSorted with an empty value")
	if t.sorted > 0 {
		assert.Ok(bytes.Compare(t.last, key) < 0, "trie UpdateSorted keys must strictly increase")
	}
	t.sorted++
	t.last = append(t.last[:0], key...)
	kn := keyNibbles(key)
	assert.NoError(t.insert(kn, value, kn), "sorted trie insert")
}

func (t *Trie) insert(kn []byte, value Value, seal []byte) error {
	slot := &t.root
	pos := 0
	for steps := 0; steps <= len(kn)+1; steps++ {
		if err := t.resolve(slot, kn[:pos]); err != nil {
			return err
		}
		n := *slot
		if n == nil {
			*slot = t.newLeaf(kn[pos:], value)
			return nil
		}
		dirty(n)
		switch n.kind {
		case kindLeaf:
			common := prefixLen(n.path, kn[pos:])
			if common == len(n.path) && common == len(kn)-pos {
				n.value = bytes.Clone(value)
				return nil
			}
			*slot = t.split(splitInput{old: n, oldPath: n.path, key: kn[pos:], common: common, value: value})
			t.drop()
			return nil
		case kindExtension:
			common := prefixLen(n.path, kn[pos:])
			if common == len(n.path) {
				pos += common
				slot = &n.children[0]
				continue
			}
			*slot = t.split(splitInput{old: n, oldPath: n.path, key: kn[pos:], common: common, value: value})
			t.drop()
			return nil
		case kindBranch:
			if pos == len(kn) {
				n.value = bytes.Clone(value)
				return nil
			}
			if seal != nil {
				t.sealBelow(n, kn[pos])
			}
			slot = &n.children[kn[pos]]
			pos++
		case kindSealed:
			assert.Fail("trie node still sealed after resolve")
		default:
			assert.Unreachable("trie node kind")
		}
	}
	assert.Fail("trie insert walked past the key")
	return nil
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

type parent struct {
	slot **node
	pos  int
}

func (t *Trie) Unset(key []byte) error {
	kn := keyNibbles(key)
	parents := make([]parent, 0, len(kn)+2)
	found, err := t.findForDelete(kn, &parents)
	if err != nil || !found {
		return err
	}
	for i := range parents {
		dirty(*parents[i].slot)
	}
	for i := len(parents) - 1; i >= 0; i-- {
		if err := t.normalize(parents[i], kn); err != nil {
			return err
		}
	}
	return nil
}

func (t *Trie) findForDelete(kn []byte, parents *[]parent) (bool, error) {
	slot := &t.root
	pos := 0
	for steps := 0; steps <= len(kn)+1; steps++ {
		if err := t.resolve(slot, kn[:pos]); err != nil {
			return false, err
		}
		n := *slot
		if n == nil {
			return false, nil
		}
		switch n.kind {
		case kindLeaf:
			if !bytes.Equal(n.path, kn[pos:]) {
				return false, nil
			}
			*slot = nil
			t.drop()
			return true, nil
		case kindExtension:
			if !bytes.HasPrefix(kn[pos:], n.path) {
				return false, nil
			}
			*parents = append(*parents, parent{slot: slot, pos: pos})
			pos += len(n.path)
			slot = &n.children[0]
		case kindBranch:
			if pos == len(kn) {
				if n.value == nil {
					return false, nil
				}
				*parents = append(*parents, parent{slot: slot, pos: pos})
				n.value = nil
				return true, nil
			}
			*parents = append(*parents, parent{slot: slot, pos: pos})
			slot = &n.children[kn[pos]]
			pos++
		case kindSealed:
			assert.Fail("trie node still sealed after resolve")
		default:
			assert.Unreachable("trie node kind")
		}
	}
	assert.Fail("trie delete walked past the key")
	return false, nil
}

func (t *Trie) normalize(p parent, kn []byte) error {
	slot := p.slot
	n := *slot
	if n.kind == kindExtension {
		if n.children[0] == nil {
			*slot = nil
			t.drop()
			return nil
		}
		t.mergeExtension(slot, n)
		return nil
	}
	assert.Equal(n.kind, kindBranch, "trie normalize of a leaf")
	count := 0
	only := -1
	for i, c := range n.children {
		if c != nil {
			count++
			only = i
		}
	}
	if n.value != nil {
		count++
	}
	if count >= 2 {
		return nil
	}
	if count == 0 {
		*slot = nil
		t.drop()
		return nil
	}
	if only < 0 {
		*slot = t.newLeaf(nil, n.value)
		t.drop()
		return nil
	}
	childPath := append(append([]byte(nil), kn[:p.pos]...), byte(only))
	if err := t.resolve(&n.children[only], childPath); err != nil {
		return err
	}
	ext := t.newExtension(nibblePath{byte(only)}, n.children[only])
	t.drop()
	*slot = ext
	t.mergeExtension(slot, ext)
	return nil
}

func (t *Trie) mergeExtension(slot **node, n *node) {
	child := n.children[0]
	switch child.kind {
	case kindLeaf:
		*slot = t.newLeaf(joinPath(n.path, child.path), child.value)
		t.drop()
		t.drop()
	case kindExtension:
		*slot = t.newExtension(joinPath(n.path, child.path), child.children[0])
		t.drop()
		t.drop()
	case kindBranch:
		return
	case kindSealed:
		assert.Fail("trie extension child left sealed")
	default:
		assert.Unreachable("trie node kind")
	}
}
