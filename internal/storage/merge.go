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

type merger struct {
	sources []source
	total   int
	last    []byte
	hi      []byte
}

func (m *merger) smallest() int {
	best := -1
	var bestKey []byte
	for i, s := range m.sources {
		if !s.valid() {
			continue
		}
		k := s.record().key
		if best < 0 || bytes.Compare(k, bestKey) < 0 {
			best, bestKey = i, k
		}
	}
	return best
}

func (m *merger) each(visit func(record) error) error {
	limit := min(m.total+1, (segmentsMax+2)*recordsMax)
	for steps := 0; steps < limit; steps++ {
		best := m.smallest()
		if best < 0 {
			return nil
		}
		r := m.sources[best].record()
		if m.hi != nil && bytes.Compare(r.key, m.hi) >= 0 {
			return nil
		}
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
	m.last = append(m.last[:0], key...)
	for _, s := range m.sources {
		if s.valid() && bytes.Equal(s.record().key, m.last) {
			if err := s.next(); err != nil {
				return err
			}
		}
	}
	return nil
}
