package sync

import (
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/p2p"
	"github.com/gakonst/goempo/internal/primitives"
)

const peersMax = 64

var (
	ErrNoPeers = errors.New("sync: no peer holds the blocks")
	ErrTooMany = errors.New("sync: too many peers")
)

type Peer interface {
	Blocks() p2p.BlockRange
	Headers(r p2p.HeadersRequest) ([][]byte, error)
	Bodies(hashes []primitives.Hash) ([][]byte, error)
	Close() error
}

type Peers struct {
	peers []Peer
	next  int
	Drop  func(p Peer, err error)
}

func NewPeers(peers []Peer) (*Peers, error) {
	if len(peers) > peersMax {
		return nil, ErrTooMany
	}
	return &Peers{peers: append([]Peer(nil), peers...)}, nil
}

func (s *Peers) Add(p Peer) error {
	if len(s.peers) >= peersMax {
		return ErrTooMany
	}
	s.peers = append(s.peers, p)
	return nil
}

func (s *Peers) Len() int { return len(s.peers) }

func (s *Peers) remove(i int, err error) {
	p := s.peers[i]
	s.peers = append(s.peers[:i], s.peers[i+1:]...)
	closeErr := p.Close()
	if s.Drop != nil {
		s.Drop(p, errors.Join(err, closeErr))
	}
}

func (s *Peers) Tip() (Link, error) {
	var best p2p.BlockRange
	for _, p := range s.peers {
		if r := p.Blocks(); r.Latest > best.Latest {
			best = r
		}
	}
	if best.Latest == 0 {
		return Link{}, ErrNoPeers
	}
	return Link{Number: best.Latest, Hash: best.LatestHash}, nil
}

func (s *Peers) pick(last uint64) int {
	for i := range len(s.peers) {
		j := (s.next + i) % len(s.peers)
		if s.peers[j].Blocks().Latest >= last {
			s.next = j + 1
			return j
		}
	}
	return -1
}

func (s *Peers) Blocks(from uint64, count int) ([]Raw, error) {
	count = min(count, p2p.ItemsMax)
	for range sourceTryMax {
		i := s.pick(from + uint64(count) - 1)
		if i < 0 {
			return nil, ErrNoPeers
		}
		raws, err := fetch(s.peers[i], from, count)
		if err == nil {
			return raws, nil
		}
		s.remove(i, err)
	}
	return nil, ErrNoPeers
}

func headerHash(h []byte) primitives.Hash { return keccak.Sum256(h) }

func fetch(p Peer, from uint64, count int) ([]Raw, error) {
	headers, err := p.Headers(p2p.HeadersRequest{Number: from, Amount: uint64(count)})
	if err != nil {
		return nil, err
	}
	if len(headers) == 0 {
		return nil, ErrShortReply
	}
	bodies, err := p.Bodies(hashesOf(headers))
	if err != nil {
		return nil, err
	}
	if len(bodies) == 0 {
		return nil, ErrShortReply
	}
	n := min(len(headers), len(bodies))
	raws := make([]Raw, n)
	for i := range raws {
		raws[i] = Raw{Header: headers[i], Body: bodies[i]}
	}
	return raws, nil
}
