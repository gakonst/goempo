package main

import (
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

type hashRing struct {
	known   [hashWindow]primitives.Hash
	headers [hashWindow]types.Header
	pending [hashWindow]bool
}

func (b *hashRing) setHash(number uint64, h primitives.Hash) {
	i := number % hashWindow
	b.known[i] = h
	b.pending[i] = false
}

func (b *hashRing) setHeader(number uint64, h *types.Header) {
	i := number % hashWindow
	b.headers[i] = *h
	b.pending[i] = true
}

func (b *hashRing) at(number uint64) primitives.Hash {
	i := number % hashWindow
	if b.pending[i] {
		b.known[i] = b.headers[i].Hash()
		b.pending[i] = false
	}
	return b.known[i]
}
