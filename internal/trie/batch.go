package trie

import (
	"sync"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/kapetan-io/tiger/assert"
)

const BatchMin = 128

const batchWorkersMax = 4

const _ = uint(nodesMax - BatchMin)

const _ = uint(16 - batchWorkersMax)

type Mutation struct {
	Key   primitives.Hash
	Value Value
}

type NodeReaderForker interface{ ForkReader() NodeReader }

type batchReader struct {
	base   NodeReader
	prefix byte
}

func (r batchReader) ReadNode(path []byte) ([]byte, error) {
	absolute := make([]byte, 1, len(path)+1)
	absolute[0] = r.prefix
	return r.base.ReadNode(append(absolute, path...))
}

type batchResult struct {
	root  *node
	delta int
	err   error
}

type batchJobs struct {
	mutations [16][]Mutation
	results   [16]batchResult
	readers   [batchWorkersMax]NodeReader
	root      *node
	nodes     int
}

func (t *Trie) ApplyBatch(mutations []Mutation) error {
	assert.Ok(len(mutations) <= nodesMax, "trie mutation batch bound")
	if len(mutations) < BatchMin {
		return t.applySerial(mutations)
	}
	if err := t.resolve(&t.root, nil); err != nil {
		return err
	}
	if t.root == nil || t.root.kind != kindBranch {
		return t.applySerial(mutations)
	}
	jobs := batchJobs{root: t.root, nodes: t.nodes}
	buckets := 0
	for _, mutation := range mutations {
		nibble := mutation.Key[0] >> 4
		if len(jobs.mutations[nibble]) == 0 {
			buckets++
		}
		jobs.mutations[nibble] = append(jobs.mutations[nibble], mutation)
	}
	if buckets < 2 {
		return t.applySerial(mutations)
	}
	if t.reader != nil {
		forker, ok := t.reader.(NodeReaderForker)
		if !ok {
			return t.applySerial(mutations)
		}
		for i := range jobs.readers {
			jobs.readers[i] = forker.ForkReader()
		}
	}
	var group sync.WaitGroup
	group.Go(func() { jobs.runLane(1) })
	group.Go(func() { jobs.runLane(2) })
	group.Go(func() { jobs.runLane(3) })
	jobs.runLane(0)
	group.Wait()
	for _, result := range jobs.results {
		if result.err != nil {
			return result.err
		}
	}
	for i, result := range jobs.results {
		t.root.children[i] = result.root
		t.nodes += result.delta
	}
	assert.Ok(t.nodes >= 0, "trie mutation node reduction nonnegative")
	assert.Ok(t.nodes < nodesMax, "trie mutation node reduction bound")
	dirty(t.root)
	return t.normalize(parent{slot: &t.root}, nil)
}

func (t *Trie) applySerial(mutations []Mutation) error {
	for _, mutation := range mutations {
		if err := t.Set(mutation.Key[:], mutation.Value); err != nil {
			return err
		}
	}
	return nil
}

func (jobs *batchJobs) runLane(lane int) {
	worker := New()
	for nibble, mutations := range jobs.mutations {
		if nibble%batchWorkersMax != lane {
			continue
		}
		worker.root = jobs.root.children[nibble]
		worker.nodes = jobs.nodes
		if jobs.readers[lane] != nil {
			worker.reader = batchReader{base: jobs.readers[lane], prefix: byte(nibble)}
		}
		var err error
		for _, mutation := range mutations {
			kn := keyNibbles(mutation.Key[:])[1:]
			if len(mutation.Value) == 0 {
				err = worker.unsetNibbles(kn)
			} else {
				err = worker.insert(kn, mutation.Value, nil)
			}
			if err != nil {
				break
			}
		}
		jobs.results[nibble] = batchResult{root: worker.root, delta: worker.nodes - jobs.nodes, err: err}
	}
}
