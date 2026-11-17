package trie

import (
	"bytes"
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/kapetan-io/tiger/assert"
)

var ErrProof = errors.New("trie: proof does not verify")

func (t *Trie) Prove(key []byte) ([][]byte, error) {
	if t.root == nil {
		return nil, nil
	}
	t.computeRefs(t.root)
	kn := keyNibbles(key)
	slot := &t.root
	pos := 0
	var proof [][]byte
	for steps := 0; steps <= 2*len(kn)+2; steps++ {
		if err := t.resolve(slot, kn[:pos]); err != nil {
			return nil, err
		}
		n := *slot
		if n == nil {
			return proof, nil
		}
		t.computeRefs(n)
		enc := bytes.Clone(t.encodeNode(n))
		if steps == 0 || len(n.ref) == primitives.HashLength {
			proof = append(proof, enc)
		}
		next, done := step(n, kn, &pos)
		if done {
			return proof, nil
		}
		slot = next
	}
	assert.Fail("trie Prove walked past the key")
	return nil, nil
}

func step(n *node, kn []byte, pos *int) (**node, bool) {
	switch n.kind {
	case kindLeaf:
		return nil, true
	case kindExtension:
		if !bytes.HasPrefix(kn[*pos:], n.path) {
			return nil, true
		}
		*pos += len(n.path)
		return &n.children[0], false
	case kindBranch:
		if *pos == len(kn) {
			return nil, true
		}
		next := &n.children[kn[*pos]]
		*pos++
		return next, false
	case kindSealed:
		assert.Fail("trie node still sealed after resolve")
	default:
		assert.Unreachable("trie node kind")
	}
	return nil, true
}

type proofWalk struct {
	kn  []byte
	pos int
}

type walkStep struct {
	value []byte
	ref   []byte
	done  bool
}

func VerifyProof(root primitives.Hash, key []byte, proof [][]byte) ([]byte, error) {
	walk := &proofWalk{kn: keyNibbles(key)}
	want := root[:]
	var embedded []byte
	limit := len(proof) + len(walk.kn) + 1
	for i := 0; i < limit; i++ {
		var enc []byte
		if embedded != nil {
			enc, embedded = embedded, nil
		} else {
			if len(proof) == 0 {
				return nil, ErrProof
			}
			enc, proof = proof[0], proof[1:]
			h := keccak.Sum256(enc)
			if !bytes.Equal(h[:], want) {
				return nil, ErrProof
			}
		}
		st, err := walk.next(enc)
		if err != nil {
			return nil, err
		}
		if st.done {
			return st.value, nil
		}
		if len(st.ref) < primitives.HashLength {
			embedded = st.ref
		} else {
			want = st.ref
		}
	}
	return nil, ErrProof
}

func (w *proofWalk) next(enc []byte) (walkStep, error) {
	items, err := listItems(enc)
	if err != nil {
		return walkStep{}, err
	}
	switch len(items) {
	case 2:
		return w.short(items)
	case 17:
		return w.branch(items)
	}
	return walkStep{}, ErrProof
}

func (w *proofWalk) short(items [][]byte) (walkStep, error) {
	compact, err := bytesOf(items[0])
	if err != nil {
		return walkStep{}, err
	}
	cp, ok := decodeHexPrefix(compact)
	if !ok {
		return walkStep{}, ErrProof
	}
	rest := w.kn[w.pos:]
	if cp.kind == pathLeaf {
		if !bytes.Equal(cp.path, rest) {
			return walkStep{done: true}, nil
		}
		v, err := bytesOf(items[1])
		return walkStep{value: v, done: true}, err
	}
	if !bytes.HasPrefix(rest, cp.path) {
		return walkStep{done: true}, nil
	}
	w.pos += len(cp.path)
	ref, err := refOf(items[1])
	return walkStep{ref: ref}, err
}

func (w *proofWalk) branch(items [][]byte) (walkStep, error) {
	if w.pos == len(w.kn) {
		v, err := bytesOf(items[16])
		return walkStep{value: v, done: true}, err
	}
	ref, err := refOf(items[w.kn[w.pos]])
	w.pos++
	if err != nil {
		return walkStep{}, err
	}
	if len(ref) == 0 {
		return walkStep{done: true}, nil
	}
	return walkStep{ref: ref}, nil
}

func refOf(item []byte) ([]byte, error) {
	if len(item) > 0 && item[0] >= 0xc0 {
		return item, nil
	}
	return bytesOf(item)
}

func bytesOf(item []byte) ([]byte, error) {
	s := rlp.NewStream(item)
	return s.Bytes()
}

func listItems(enc []byte) ([][]byte, error) {
	s := rlp.NewStream(enc)
	list, err := s.List()
	if err != nil {
		return nil, ErrProof
	}
	n, err := list.Count()
	if err != nil || n > 17 {
		return nil, ErrProof
	}
	items := make([][]byte, n)
	for i := range items {
		if items[i], err = list.Raw(); err != nil {
			return nil, ErrProof
		}
	}
	return items, nil
}
