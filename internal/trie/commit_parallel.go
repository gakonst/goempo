package trie

import (
	"sync"

	"github.com/kapetan-io/tiger/assert"
)

const (
	commitWorkersMax  = 4
	commitChildrenMin = 4
	commitDirtyMin    = 128
)

const _ = uint(16 - commitChildrenMin)

const _ = uint(commitChildrenMin - commitWorkersMax)

const _ = uint(nodesMax - commitDirtyMin)

type commitWorkers struct{ lanes [commitWorkersMax - 1]*Trie }

type commitJobs struct {
	roots [16]*node
	count int
}

func (t *Trie) computeCommitRefs() {
	root := t.root
	if root.ref != nil {
		return
	}
	if root.kind != kindBranch {
		t.computeRefs(root)
		return
	}
	jobs := commitJobs{}
	for _, child := range root.children {
		if child != nil && child.ref == nil {
			jobs.roots[jobs.count] = child
			jobs.count++
		}
	}
	if jobs.count < commitChildrenMin || !t.hasCommitWork(root) {
		t.computeRefs(root)
		return
	}
	if t.commit == nil {
		t.commit = &commitWorkers{}
	}
	for i := 0; i < commitWorkersMax-1; i++ {
		if t.commit.lanes[i] == nil {
			t.commit.lanes[i] = New()
		}
		t.commit.lanes[i].nodes = t.nodes
		t.commit.lanes[i].reader = t.reader
	}
	t.runCommitWorkers(&jobs)
	t.computeRefs(root)
}

func (t *Trie) hasCommitWork(root *node) bool {
	stack := append(t.scratch[:0], nodeVisit{n: root})
	for range [commitDirtyMin]struct{}{} {
		if len(stack) == 0 {
			t.scratch = stack
			return false
		}
		last := len(stack) - 1
		n := stack[last].n
		stack[last] = nodeVisit{}
		stack = stack[:last]
		for _, child := range n.children {
			if child != nil && child.ref == nil {
				stack = append(stack, nodeVisit{n: child})
			}
		}
	}
	clear(stack)
	t.scratch = stack[:0]
	return true
}

func (t *Trie) runCommitWorkers(jobs *commitJobs) {
	assert.Ok(jobs.count <= len(jobs.roots), "trie commit exceeds child bound")
	var group sync.WaitGroup
	group.Go(func() { t.commit.lanes[0].computeCommitLane(jobs, 1) })
	group.Go(func() { t.commit.lanes[1].computeCommitLane(jobs, 2) })
	group.Go(func() { t.commit.lanes[2].computeCommitLane(jobs, 3) })
	t.computeCommitLane(jobs, 0)
	group.Wait()
}

func (t *Trie) computeCommitLane(jobs *commitJobs, lane int) {
	for i, root := range jobs.roots {
		if i < jobs.count && i%commitWorkersMax == lane {
			t.computeRefs(root)
		}
	}
}
