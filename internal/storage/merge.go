package storage

import (
	"bytes"
	"errors"
)

var errStop = errors.New("storage: stop iterating")

type source interface {
	valid() bool
	record() record
	next() error
}

type mergeInput struct {
	segs   []*segment
	mem    *memtable
	prefix []byte
}

type merger struct {
	sources []source
	total   int
}

func newMerger(in mergeInput) (*merger, error) {
	m := &merger{}
	if in.mem != nil {
		m.sources = append(m.sources, in.mem.iterator(in.prefix))
		m.total += min(len(in.mem.entries), recordsMax)
	}
	for i := len(in.segs) - 1; i >= 0; i-- {
		it, err := in.segs[i].iterator(in.prefix)
		if err != nil {
			return nil, err
		}
		m.sources = append(m.sources, it)
		m.total += in.segs[i].records
	}
	return m, nil
}

func (m *merger) smallest() int {
	best := -1
	for i, s := range m.sources {
		if !s.valid() {
			continue
		}
		if best < 0 || bytes.Compare(s.record().key, m.sources[best].record().key) < 0 {
			best = i
		}
	}
	return best
}

func (m *merger) each(visit func(record) error) error {
	limit := min(m.total+1, (segmentsMax+1)*recordsMax)
	steps := 0
	for ; steps < limit; steps++ {
		best := m.smallest()
		if best < 0 {
			return nil
		}
		r := m.sources[best].record()
		err := visit(r)
		if errors.Is(err, errStop) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := m.skip(r.key); err != nil {
			return err
		}
	}
	return ErrCorrupt
}

func (m *merger) skip(key []byte) error {
	k := append([]byte(nil), key...)
	for _, s := range m.sources {
		if s.valid() && bytes.Equal(s.record().key, k) {
			if err := s.next(); err != nil {
				return err
			}
		}
	}
	return nil
}
