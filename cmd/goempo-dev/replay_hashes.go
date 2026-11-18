package main

import (
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

type hashRing struct {
	mu      sync.Mutex
	known   [hashWindow]primitives.Hash
	headers [hashWindow]types.Header
	pending [hashWindow]bool
}

func (b *hashRing) setHash(number uint64, h primitives.Hash) {
	b.mu.Lock()
	defer b.mu.Unlock()

	i := number % hashWindow
	b.known[i] = h
	b.pending[i] = false
}

func (b *hashRing) setHeader(number uint64, h *types.Header) {
	b.mu.Lock()
	defer b.mu.Unlock()

	i := number % hashWindow
	b.headers[i] = *h
	b.pending[i] = true
}

func (b *hashRing) at(number uint64) primitives.Hash {
	b.mu.Lock()
	defer b.mu.Unlock()

	i := number % hashWindow
	if b.pending[i] {
		b.known[i] = b.headers[i].Hash()
		b.pending[i] = false
	}
	return b.known[i]
}
