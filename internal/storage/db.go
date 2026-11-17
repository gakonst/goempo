package storage

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const MemtableBytesDefault = 64 << 20

const MergeWorkersDefault = 4

const mergeWorkersMax = 16

const mergeJobsMax = 2

const mergePartBytesMin = 32 << 20

const immutableMax = 1

const parallelApplyMin = 1 << 11

const applyWorkersMax = memShards

const _ = uint(applyWorkersMax - mergeWorkersMax/2)

const segmentsMax = 64

const tierWidth = 4

const runsSoft = 2 * tierWidth

const abortCheckRecords = 1 << 12

const _ = uint(segmentsMax - tierWidth)

const _ = uint(runSegmentsMax - mergeWorkersMax)

var errAborted = errors.New("storage: merge aborted by close")

type Options struct {
	MemtableBytes   int
	BlockCacheBytes int
	MergeWorkers    int
}

type applyScratch struct {
	ops    []op
	hashes []uint64
}

type view struct {
	imm  []*memtable
	runs []*run
}

type DB struct {
	fs      goio.FS
	limit   int
	workers int
	mem     *memtable
	scratch applyScratch
	cache   *blockCache
	current atomic.Pointer[view]

	mu       sync.Mutex
	cond     sync.Cond
	seq      uint64
	nextID   uint64
	busy     map[*run]bool
	jobs     int
	rotated  uint64
	flushed  uint64
	retired  []*segment
	bgErr    error
	closing  bool
	closed   bool
	stats    Stats
	manifest sync.Mutex

	flushCh chan *memtable
	jobCh   chan *mergeJob
	abort   atomic.Bool
	wg      sync.WaitGroup
}

type Stats struct {
	Segments     int
	Runs         int
	SegmentBytes int64
	MemtableKeys int
	Flushes      int
	Merges       int
	BytesWritten int64
}

func Open(fs goio.FS, opts Options) (*DB, error) {
	limit := opts.MemtableBytes
	if limit <= 0 {
		limit = MemtableBytesDefault
	}
	cacheBytes := opts.BlockCacheBytes
	if cacheBytes <= 0 {
		cacheBytes = BlockCacheBytesDefault
	}
	workers := opts.MergeWorkers
	if workers <= 0 {
		workers = MergeWorkersDefault
	}
	db := &DB{
		fs: fs, limit: limit, workers: min(workers, mergeWorkersMax),
		mem: newMemtable(limit), cache: newBlockCache(cacheBytes), busy: map[*run]bool{},
		flushCh: make(chan *memtable, immutableMax), jobCh: make(chan *mergeJob, mergeJobsMax),
	}
	db.cond.L = &db.mu
	man, err := readManifest(fs)
	if err != nil {
		return nil, err
	}
	db.seq = man.seq
	runs := make([]*run, 0, len(man.runs))
	for _, names := range man.runs {
		segs := make([]*segment, 0, len(names))
		for _, name := range names {
			s, err := db.openNamed(name)
			if err != nil {
				return nil, errors.Join(err, closeAll(runs), closeAll([]*run{{segs: segs}}))
			}
			segs = append(segs, s)
		}
		runs = append(runs, newRun(segs))
	}
	db.current.Store(&view{runs: runs})
	if err := db.removeUnlisted(man); err != nil {
		return nil, errors.Join(err, closeAll(runs))
	}
	db.wg.Add(1 + mergeJobsMax)
	go db.flushLoop()
	for range mergeJobsMax {
		go db.mergeLoop()
	}
	db.mu.Lock()
	db.schedule()
	db.mu.Unlock()
	return db, nil
}

func (db *DB) openNamed(name string) (*segment, error) {
	file, err := db.fs.Open(name)
	if err != nil {
		return nil, err
	}
	db.nextID++
	s, err := openSegment(name, db.nextID, file)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return s, nil
}

func closeAll(runs []*run) error {
	var err error
	for _, r := range runs {
		for _, s := range r.segs {
			err = errors.Join(err, s.file.Close())
		}
	}
	return err
}

func (db *DB) removeUnlisted(man manifest) error {
	all, err := db.fs.List()
	if err != nil {
		return err
	}
	keep := map[string]bool{manifestName: true}
	for _, names := range man.runs {
		for _, name := range names {
			keep[name] = true
		}
	}
	for _, name := range all {
		if keep[name] {
			continue
		}
		if err := db.fs.Remove(name); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Get(key []byte) ([]byte, error) {
	p := newProbeKey(key)
	if l, ok := db.mem.get(&p); ok {
		return foundValue(l)
	}
	v := db.current.Load()
	for i := len(v.imm) - 1; i >= 0; i-- {
		if l, ok := v.imm[i].get(&p); ok {
			return foundValue(l)
		}
	}
	for i := len(v.runs) - 1; i >= 0; i-- {
		found, err := v.runs[i].get(&p, db.cache)
		if err != nil {
			return nil, err
		}
		if !found.found {
			continue
		}
		if found.deleted {
			return nil, ErrNotFound
		}
		return found.value, nil
	}
	return nil, ErrNotFound
}

func foundValue(l lookup) ([]byte, error) {
	if l.deleted {
		return nil, ErrNotFound
	}
	return l.value, nil
}

func (db *DB) failure() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return ErrClosed
	}
	return db.bgErr
}

func (db *DB) Write(b *Batch) error {
	defer b.release()
	if err := db.reclaim(); err != nil {
		return err
	}
	if err := db.failure(); err != nil {
		return err
	}
	if b.Len() >= parallelApplyMin {
		db.applyParallel(b)
	} else {
		b.each(db.mem.apply)
	}
	if db.mem.bytes() >= db.limit {
		return db.rotate()
	}
	return nil
}

func (db *DB) applyParallel(b *Batch) {
	sc := &db.scratch
	sc.ops = sc.ops[:0]
	b.each(func(o op) { sc.ops = append(sc.ops, o) })
	n := len(sc.ops)
	sc.hashes = slices.Grow(sc.hashes[:0], n)[:n]
	workers := min(db.workers, applyWorkersMax)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i := w * n / workers; i < (w+1)*n/workers; i++ {
				sc.hashes[i] = hashKey(sc.ops[i].key)
			}
		})
	}
	wg.Wait()
	for w := range workers {
		wg.Go(func() {
			for i, h := range sc.hashes {
				if s := shardOf(h); s%workers == w {
					db.mem.shards[s].apply(sc.ops[i], h)
				}
			}
		})
	}
	wg.Wait()
	clear(sc.ops)
}

func (db *DB) reclaim() error {
	db.mu.Lock()
	old := db.retired
	db.retired = nil
	db.mu.Unlock()
	var err error
	for _, s := range old {
		err = errors.Join(err, s.file.Close())
	}
	return err
}

func (db *DB) rotate() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	for waits := 0; db.bgErr == nil && len(db.current.Load().imm) >= immutableMax; waits++ {
		assert.Ok(waits < 1<<40, "storage writer waited forever for a flush")
		db.cond.Wait()
	}
	if db.bgErr != nil {
		return db.bgErr
	}
	v := db.current.Load()
	m := db.mem
	db.current.Store(&view{imm: append(slices.Clip(v.imm), m), runs: v.runs})
	db.mem = newMemtable(db.limit)
	db.rotated++
	db.flushCh <- m
	return nil
}

func (db *DB) nextName() (string, uint64) {
	db.seq++
	db.nextID++
	return strconv.FormatUint(db.seq, 16) + ".seg", db.nextID
}

func (db *DB) createSegment() (string, uint64, goio.File, error) {
	db.mu.Lock()
	name, id := db.nextName()
	db.mu.Unlock()
	file, err := db.fs.Create(name)
	return name, id, file, err
}

func (db *DB) Flush() error {
	if err := db.reclaim(); err != nil {
		return err
	}
	if err := db.failure(); err != nil {
		return err
	}
	if db.mem.len() > 0 {
		if err := db.rotate(); err != nil {
			return err
		}
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	for waits := 0; db.bgErr == nil && db.flushed < db.rotated; waits++ {
		assert.Ok(waits < 1<<40, "storage flush waited forever")
		db.cond.Wait()
	}
	return db.bgErr
}

func (db *DB) fail(err error) {
	db.mu.Lock()
	if db.bgErr == nil {
		db.bgErr = err
	}
	db.cond.Broadcast()
	db.mu.Unlock()
}

func (db *DB) flushLoop() {
	defer db.wg.Done()
	for m := range db.flushCh {
		if db.failure() != nil {
			continue
		}
		if err := db.flushOne(m); err != nil {
			db.fail(err)
		}
	}
}

func (db *DB) flushOne(m *memtable) error {
	name, id, file, err := db.createSegment()
	if err != nil {
		return err
	}
	w := newSegmentWriter(file, m.len())
	mg := &merger{}
	m.sources(nil, sortFull, mg)
	err = mg.each(func(r record) error {
		w.add(r)
		return nil
	})
	var seg *segment
	if err == nil {
		seg, err = w.finish(name, id)
	}
	if err != nil {
		return errors.Join(err, file.Close())
	}
	db.mu.Lock()
	for waits := 0; db.bgErr == nil && !db.closing && len(db.current.Load().runs) >= segmentsMax; waits++ {
		assert.Ok(waits < 1<<40, "storage flush waited forever for a merge")
		db.cond.Wait()
	}
	v := db.current.Load()
	assert.Ok(len(v.imm) > 0 && v.imm[0] == m, "storage flushes memtables in order")
	db.current.Store(&view{imm: v.imm[1:], runs: append(slices.Clip(v.runs), newRun([]*segment{seg}))})
	db.stats.Flushes++
	db.stats.BytesWritten += seg.size
	db.mu.Unlock()
	if err := db.writeManifest(); err != nil {
		return err
	}
	db.mu.Lock()
	db.flushed++
	db.schedule()
	db.cond.Broadcast()
	db.mu.Unlock()
	return nil
}

func (db *DB) windowFree(runs []*run) bool {
	for _, r := range runs {
		if db.busy[r] {
			return false
		}
	}
	return true
}

func tiered(runs []*run) bool {
	lo, hi := runs[0].size, runs[0].size
	for _, r := range runs[1:] {
		lo, hi = min(lo, r.size), max(hi, r.size)
	}
	return hi <= tierWidth*max(lo, 1)
}

func (db *DB) pickWindow(runs []*run) int {
	for i := len(runs) - tierWidth; i >= 0; i-- {
		w := runs[i : i+tierWidth]
		if db.windowFree(w) && tiered(w) {
			return i
		}
	}
	if len(runs) <= runsSoft {
		return -1
	}
	best, bestSize := -1, int64(0)
	for i := len(runs) - tierWidth; i >= 0; i-- {
		w := runs[i : i+tierWidth]
		if !db.windowFree(w) {
			continue
		}
		var size int64
		for _, r := range w {
			size += r.size
		}
		if best < 0 || size < bestSize {
			best, bestSize = i, size
		}
	}
	return best
}

type mergeJob struct {
	inputs []*run
	drop   bool
}

func (db *DB) schedule() {
	for db.jobs < mergeJobsMax && !db.closing && db.bgErr == nil {
		runs := db.current.Load().runs
		if len(runs) < tierWidth {
			return
		}
		at := db.pickWindow(runs)
		if at < 0 {
			return
		}
		window := runs[at : at+tierWidth]
		for _, r := range window {
			db.busy[r] = true
		}
		db.jobs++
		db.jobCh <- &mergeJob{inputs: slices.Clone(window), drop: at == 0}
	}
}

func (db *DB) mergeLoop() {
	defer db.wg.Done()
	for job := range db.jobCh {
		db.runJob(job)
	}
}

func (db *DB) runJob(job *mergeJob) {
	segs, err := db.mergeRuns(job)
	db.mu.Lock()
	for _, r := range job.inputs {
		delete(db.busy, r)
	}
	db.jobs--
	if err != nil {
		if !errors.Is(err, errAborted) && db.bgErr == nil {
			db.bgErr = err
		}
		db.cond.Broadcast()
		db.mu.Unlock()
		return
	}
	v := db.current.Load()
	at := slices.Index(v.runs, job.inputs[0])
	assert.Ok(at >= 0 && at+len(job.inputs) <= len(v.runs), "merged runs are still live")
	for i, r := range job.inputs {
		assert.Ok(v.runs[at+i] == r, "merged runs are contiguous")
	}
	runs := slices.Clone(v.runs[:at])
	if len(segs) > 0 {
		merged := newRun(segs)
		runs = append(runs, merged)
		db.stats.BytesWritten += merged.size
	}
	runs = append(runs, v.runs[at+len(job.inputs):]...)
	db.current.Store(&view{imm: v.imm, runs: runs})
	db.stats.Merges++
	db.mu.Unlock()
	err = db.writeManifest()
	if err == nil {
		err = db.retire(job.inputs)
	}
	db.mu.Lock()
	if err != nil && db.bgErr == nil {
		db.bgErr = err
	}
	db.schedule()
	db.cond.Broadcast()
	db.mu.Unlock()
}

func (db *DB) retire(runs []*run) error {
	var old []*segment
	var err error
	for _, r := range runs {
		for _, s := range r.segs {
			err = errors.Join(err, db.fs.Remove(s.name))
			old = append(old, s)
		}
	}
	db.mu.Lock()
	db.retired = append(db.retired, old...)
	db.mu.Unlock()
	return err
}

func (db *DB) splitKeys(inputs []*run, parts int) [][]byte {
	largest := inputs[0]
	for _, r := range inputs[1:] {
		if r.size > largest.size {
			largest = r
		}
	}
	step := largest.size / int64(parts)
	splits := make([][]byte, 0, parts-1)
	var seen int64
	next := step
	for _, s := range largest.segs {
		for _, e := range s.index {
			seen += int64(e.length)
			if seen < next || len(splits) == parts-1 {
				continue
			}
			if len(splits) == 0 || bytes.Compare(e.firstKey, splits[len(splits)-1]) > 0 {
				splits = append(splits, e.firstKey)
			}
			next += step
		}
	}
	return splits
}

func (db *DB) mergeRuns(job *mergeJob) ([]*segment, error) {
	var total int64
	for _, r := range job.inputs {
		total += r.size
	}
	partBytes := int64(min(mergePartBytesMin, 4*db.limit))
	parts := int(min(max(total/partBytes, 1), int64(db.workers)))
	splits := db.splitKeys(job.inputs, parts)
	bounds := make([][]byte, 0, len(splits)+2)
	bounds = append(bounds, nil)
	bounds = append(bounds, splits...)
	bounds = append(bounds, nil)
	out := make([]*segment, len(bounds)-1)
	errs := make([]error, len(bounds)-1)
	var wg sync.WaitGroup
	for p := range out {
		wg.Go(func() {
			out[p], errs[p] = db.mergePart(job, bounds[p], bounds[p+1])
		})
	}
	wg.Wait()
	err := errors.Join(errs...)
	segs := make([]*segment, 0, len(out))
	for _, s := range out {
		if s == nil {
			continue
		}
		if err != nil {
			err = errors.Join(err, s.file.Close(), db.fs.Remove(s.name))
			continue
		}
		segs = append(segs, s)
	}
	if err != nil {
		return nil, err
	}
	return segs, nil
}

func (db *DB) mergePart(job *mergeJob, lo, hi []byte) (*segment, error) {
	m := &merger{hi: hi}
	for i := len(job.inputs) - 1; i >= 0; i-- {
		it, err := job.inputs[i].iterator(lo, true)
		if err != nil {
			return nil, err
		}
		m.sources = append(m.sources, it)
		m.total += job.inputs[i].records
	}
	name, id, file, err := db.createSegment()
	if err != nil {
		return nil, err
	}
	w := newSegmentWriter(file, 0)
	seen := 0
	err = m.each(func(r record) error {
		seen++
		if seen%abortCheckRecords == 0 && db.abort.Load() {
			return errAborted
		}
		if r.deleted && job.drop {
			return nil
		}
		w.add(r)
		return nil
	})
	if err == nil && w.records == 0 {
		return nil, errors.Join(file.Close(), db.fs.Remove(name))
	}
	var seg *segment
	if err == nil {
		seg, err = w.finish(name, id)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close(), db.fs.Remove(name))
	}
	return seg, nil
}

func (db *DB) Iterate(prefix []byte, visit func(Entry) error) error {
	v := db.current.Load()
	m := &merger{}
	db.mem.sources(prefix, sortLazy, m)
	for i := len(v.imm) - 1; i >= 0; i-- {
		v.imm[i].sources(prefix, sortLazy, m)
	}
	for i := len(v.runs) - 1; i >= 0; i-- {
		it, err := v.runs[i].iterator(prefix, false)
		if err != nil {
			return err
		}
		m.sources = append(m.sources, it)
		m.total += v.runs[i].records
	}
	return m.each(func(r record) error {
		if !bytes.HasPrefix(r.key, prefix) {
			return errStop
		}
		if r.deleted {
			return nil
		}
		return visit(Entry{Key: r.key, Value: r.value})
	})
}

func (db *DB) Stats() Stats {
	db.mu.Lock()
	s := db.stats
	db.mu.Unlock()
	v := db.current.Load()
	s.Runs = len(v.runs)
	s.MemtableKeys = db.mem.len()
	for _, r := range v.runs {
		s.Segments += len(r.segs)
		s.SegmentBytes += r.size
	}
	return s
}

func (db *DB) Close() error {
	db.mu.Lock()
	if db.closed {
		db.mu.Unlock()
		return nil
	}
	db.mu.Unlock()
	err := db.Flush()
	db.abort.Store(true)
	db.mu.Lock()
	db.closing = true
	close(db.jobCh)
	close(db.flushCh)
	db.cond.Broadcast()
	db.mu.Unlock()
	db.wg.Wait()
	db.mu.Lock()
	db.closed = true
	db.mu.Unlock()
	err = errors.Join(err, db.reclaim(), closeAll(db.current.Load().runs))
	db.current.Store(&view{})
	return err
}
