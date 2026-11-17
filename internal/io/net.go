package io

import (
	"errors"
	"sync"
)

var (
	ErrConnClosed   = errors.New("io: connection closed")
	ErrRefused      = errors.New("io: connection refused")
	ErrAddressInUse = errors.New("io: address already in use")
)

type Conn interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
	SetReadTimeout(nanos uint64) error
}

type Dialer interface {
	Dial(address string) (Conn, error)
}

type Listener interface {
	Accept() (Conn, error)
	Close() error
}

type MemNetwork struct {
	mu        sync.Mutex
	listeners map[string]*memListener
}

func NewMemNetwork() *MemNetwork {
	return &MemNetwork{listeners: map[string]*memListener{}}
}

func (n *MemNetwork) Listen(address string) (Listener, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.listeners[address]; ok {
		return nil, ErrAddressInUse
	}
	l := &memListener{network: n, address: address, pending: make(chan Conn, memBacklog), done: make(chan struct{})}
	n.listeners[address] = l
	return l, nil
}

func (n *MemNetwork) Dial(address string) (Conn, error) {
	n.mu.Lock()
	l, ok := n.listeners[address]
	n.mu.Unlock()
	if !ok {
		return nil, ErrRefused
	}
	pair := Pipe()
	select {
	case l.pending <- pair.B:
		return pair.A, nil
	case <-l.done:
		return nil, ErrRefused
	default:
		return nil, ErrRefused
	}
}

const memBacklog = 16

type memListener struct {
	network *MemNetwork
	address string
	pending chan Conn
	done    chan struct{}
	once    sync.Once
}

func (l *memListener) Accept() (Conn, error) {
	select {
	case c := <-l.pending:
		return c, nil
	case <-l.done:
		return nil, ErrConnClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() {
		l.network.mu.Lock()
		delete(l.network.listeners, l.address)
		l.network.mu.Unlock()
		close(l.done)
	})
	return nil
}

const pipeChunksMax = 4096

const _ = uint(pipeChunksMax - memBacklog)

type pipeHalf struct {
	chunks chan []byte
	done   chan struct{}
	once   sync.Once
}

func newPipeHalf() *pipeHalf {
	return &pipeHalf{chunks: make(chan []byte, pipeChunksMax), done: make(chan struct{})}
}

func (h *pipeHalf) Close() {
	h.once.Do(func() { close(h.done) })
}

type pipeEnd struct {
	in      *pipeHalf
	out     *pipeHalf
	pending []byte
}

type PipePair struct {
	A Conn
	B Conn
}

func Pipe() PipePair {
	a := newPipeHalf()
	b := newPipeHalf()
	return PipePair{A: &pipeEnd{in: a, out: b}, B: &pipeEnd{in: b, out: a}}
}

func (p *pipeEnd) Read(buf []byte) (int, error) {
	in := p.in
	if len(p.pending) == 0 {
		select {
		case chunk := <-in.chunks:
			p.pending = chunk
		case <-in.done:
			select {
			case chunk := <-in.chunks:
				p.pending = chunk
			default:
				return 0, ErrConnClosed
			}
		}
	}
	n := copy(buf, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}

func (p *pipeEnd) Write(buf []byte) (int, error) {
	out := p.out
	chunk := append([]byte(nil), buf...)
	select {
	case <-out.done:
		return 0, ErrConnClosed
	default:
	}
	select {
	case out.chunks <- chunk:
		return len(buf), nil
	case <-out.done:
		return 0, ErrConnClosed
	}
}

func (p *pipeEnd) Close() error {
	p.in.Close()
	p.out.Close()
	return nil
}

func (p *pipeEnd) SetReadTimeout(uint64) error { return nil }
