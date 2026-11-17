// Package p2p speaks devp2p to Tempo's execution nodes: the p2p base
// protocol (Hello, Ping, Disconnect) over an RLPx connection, the eth/68 and
// eth/69 Status with Tempo's fork id, and the block header and body requests
// a follower downloads blocks with.
package p2p

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const forksMax = 32

const timestampThreshold = 1_300_000_000

const _ = uint(timestampThreshold - forksMax)

var ErrForkOrder = errors.New("p2p: fork times are not in order")

type ForkID struct {
	Hash [4]byte
	Next uint64
}

type Schedule struct {
	activations []uint64
	hashes      []uint32
}

type Chain struct {
	Genesis   primitives.Hash
	Timestamp uint64
	Forks     []uint64
}

func NewSchedule(c Chain) (Schedule, error) {
	var s Schedule
	if len(c.Forks) > forksMax {
		return s, ErrForkOrder
	}
	sum := crc32.ChecksumIEEE(c.Genesis[:])
	s.hashes = append(s.hashes, sum)
	previous := uint64(0)
	for _, t := range c.Forks {
		if t <= c.Timestamp || t == previous {
			continue
		}
		if t < previous {
			return s, ErrForkOrder
		}
		var be [8]byte
		binary.BigEndian.PutUint64(be[:], t)
		sum = crc32.Update(sum, crc32.IEEETable, be[:])
		s.activations = append(s.activations, t)
		s.hashes = append(s.hashes, sum)
		previous = t
	}
	return s, nil
}

func TempoForks(chainID hardfork.ChainID) []uint64 {
	forks := []uint64{0, 0, 0, 0}
	for i := range int(hardfork.Latest()) + 1 {
		if t, ok := hardfork.Activation(chainID, hardfork.Fork(i)); ok {
			forks = append(forks, t)
		}
	}
	return forks
}

func (s Schedule) passed(headTime uint64) int {
	n := 0
	for _, t := range s.activations {
		if t > headTime {
			break
		}
		n++
	}
	return n
}

func encodeHash(h uint32) [4]byte {
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], h)
	return out
}

func (s Schedule) ID(headTime uint64) ForkID {
	n := s.passed(headTime)
	id := ForkID{Hash: encodeHash(s.hashes[n])}
	if n < len(s.activations) {
		id.Next = s.activations[n]
	}
	return id
}

type Head struct {
	Number    uint64
	Timestamp uint64
}

func (s Schedule) Compatible(head Head, remote ForkID) bool {
	n := s.passed(head.Timestamp)
	if encodeHash(s.hashes[n]) == remote.Hash {
		if remote.Next == 0 {
			return true
		}
		if remote.Next > timestampThreshold {
			return head.Timestamp < remote.Next
		}
		return head.Number < remote.Next
	}
	for i := 0; i < n; i++ {
		if encodeHash(s.hashes[i]) == remote.Hash {
			return s.activations[i] == remote.Next
		}
	}
	for i := n + 1; i < len(s.hashes); i++ {
		if encodeHash(s.hashes[i]) == remote.Hash {
			return true
		}
	}
	return false
}
