package rpc

import (
	"encoding/binary"
	"hash/crc32"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const forkIDLength = 4

func (s *Server) genesisHash() primitives.Hash {
	if !s.cfg.GenesisHash.IsZero() {
		return s.cfg.GenesisHash
	}
	if b, ok := s.chain.Block(0); ok {
		return b.Hash
	}
	return primitives.Hash{}
}

func (s *Server) headTime() uint64 {
	if b, ok := s.chain.Block(s.chain.Head()); ok {
		return b.Block.Header.Inner.Timestamp
	}
	return 0
}

type forkEntry struct {
	name   string
	at     uint64
	active bool
	id     [forkIDLength]byte
}

func (s *Server) forkEntries() []forkEntry {
	chainID := hardfork.ChainID(s.cfg.ChainID)
	g := s.genesisHash()
	sum := crc32.ChecksumIEEE(g[:])
	last := uint64(0)
	now := s.headTime()
	out := make([]forkEntry, 0, int(hardfork.Latest()))
	for i := range int(hardfork.Latest()) {
		f := hardfork.Fork(i + 1)
		at, ok := hardfork.Activation(chainID, f)
		if !ok {
			break
		}
		if at > last {
			var be [8]byte
			binary.BigEndian.PutUint64(be[:], at)
			sum = crc32.Update(sum, crc32.IEEETable, be[:])
			last = at
		}
		e := forkEntry{name: f.String(), at: at, active: at <= now}
		binary.BigEndian.PutUint32(e.id[:], sum)
		out = append(out, e)
	}
	return out
}

func tempoForkSchedule(s *Server, _ *params) ([]byte, error) {
	entries := s.forkEntries()
	activeName := ""
	return encode(func(w *writer) {
		w.objOpen()
		w.key("schedule")
		w.arrOpen()
		for _, e := range entries {
			w.objOpen()
			w.kStr("name", e.name)
			w.key("activationTime")
			w.integer(int64(e.at))
			w.kBool("active", jbool(e.active))
			if e.active {
				w.kData("forkId", e.id[:])
				activeName = e.name
			}
			w.objClose()
		}
		w.arrClose()
		w.key("active")
		if activeName == "" {
			w.null()
		} else {
			w.str(activeName)
		}
		w.objClose()
	}), nil
}
