package trie

import (
	"github.com/kapetan-io/tiger/assert"
)

const KeyBytesMax = 1024

const nibblesMax = 2 * KeyBytesMax

const _ = uint(nibblesMax - 2*KeyBytesMax)

func keyNibbles(key []byte) []byte {
	assert.Ok(len(key) <= KeyBytesMax, "trie key longer than KeyBytesMax")
	out := make([]byte, 2*len(key))
	for i := 0; i < len(key); i++ {
		out[2*i] = key[i] >> 4
		out[2*i+1] = key[i] & 0x0f
	}
	return out
}

type nibblePath []byte

func prefixLen(a nibblePath, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func joinPath(head nibblePath, tail []byte) []byte {
	out := make([]byte, 0, len(head)+len(tail))
	out = append(out, head...)
	return append(out, tail...)
}

type pathKind bool

const (
	pathLeaf      pathKind = true
	pathExtension pathKind = false
)

func hexPrefix(path []byte, kind pathKind) []byte {
	flag := byte(0)
	if kind == pathLeaf {
		flag = 2
	}
	out := make([]byte, 1+len(path)/2)
	rest := path
	if len(path)%2 == 1 {
		out[0] = (flag+1)<<4 | path[0]
		rest = path[1:]
	} else {
		out[0] = flag << 4
	}
	pairs := len(rest) / 2
	for k := 0; k < pairs; k++ {
		out[1+k] = rest[2*k]<<4 | rest[2*k+1]
	}
	return out
}

type compactPath struct {
	path []byte
	kind pathKind
}

func decodeHexPrefix(compact []byte) (compactPath, bool) {
	if len(compact) == 0 {
		return compactPath{}, false
	}
	flag := compact[0] >> 4
	if flag > 3 {
		return compactPath{}, false
	}
	kind := pathExtension
	if flag >= 2 {
		kind = pathLeaf
	}
	odd := flag%2 == 1
	if !odd && compact[0]&0x0f != 0 {
		return compactPath{}, false
	}
	path := make([]byte, 0, 2*len(compact))
	if odd {
		path = append(path, compact[0]&0x0f)
	}
	for _, b := range compact[1:] {
		path = append(path, b>>4, b&0x0f)
	}
	return compactPath{path: path, kind: kind}, true
}
