package storage

import (
	"bytes"
	"sort"
)

const runSegmentsMax = 64

type run struct {
	segs    []*segment
	size    int64
	records int
}

func newRun(segs []*segment) *run {
	r := &run{segs: segs}
	for _, s := range segs {
		r.size += s.size
		r.records += s.records
	}
	return r
}

func (r *run) segmentFor(key []byte) int {
	return sort.Search(len(r.segs), func(i int) bool {
		return bytes.Compare(r.segs[i].firstKey(), key) > 0
	}) - 1
}

func (r *run) get(p *probeKey, cache *blockCache) (lookup, error) {
	i := r.segmentFor(p.key)
	if i < 0 {
		return lookup{}, nil
	}
	return r.segs[i].get(p, cache)
}

type runIterator struct {
	r     *run
	at    int
	it    *segmentIterator
	ahead bool
}

func (r *run) iterator(seek []byte, ahead bool) (*runIterator, error) {
	ri := &runIterator{r: r, at: max(r.segmentFor(seek), 0), ahead: ahead}
	it, err := r.segs[ri.at].iterator(seek, ahead)
	if err != nil {
		return nil, err
	}
	ri.it = it
	return ri, ri.skipDone()
}

func (ri *runIterator) skipDone() error {
	for steps := 0; steps < len(ri.r.segs) && !ri.it.valid() && ri.at+1 < len(ri.r.segs); steps++ {
		ri.at++
		it, err := ri.r.segs[ri.at].iterator(nil, ri.ahead)
		if err != nil {
			return err
		}
		ri.it = it
	}
	return nil
}

func (ri *runIterator) valid() bool { return ri.it.valid() }

func (ri *runIterator) record() record { return ri.it.record() }

func (ri *runIterator) next() error {
	if err := ri.it.next(); err != nil {
		return err
	}
	return ri.skipDone()
}
