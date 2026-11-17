// Package storage is goempo's on-disk key-value store: a small log-structured
// merge tree over the files of an io.FS, so tests run on io.MemFS and the
// node on io.DirFS.
//
// Write applies a Batch to the in-memory memtable. Once the memtable holds
// Options.MemtableBytes, or on Flush, it is written out as an immutable,
// sorted segment file and the MANIFEST (the list of live segments, replaced
// by an atomic rename) is rewritten. What was written since the last flush is
// lost on a crash, so callers flush at a point they can resume from, such as a
// block boundary, and keep their own progress marker in the same batch.
//
// A segment holds records sorted by key in blocks of up to 64 records, each
// block checked with CRC-32C, then a sparse index (each block's first key) and
// a bloom filter that are loaded into memory when the segment opens. Get looks
// in the memtable and then in the segments from newest to oldest. Segments are
// merged in tiers: whenever the newest four are in the same size class (sizes
// within a factor of four), they are merged into one; tombstones are dropped
// when a merge reaches the oldest segment. Iterate walks every live key with a
// prefix in order, merging all sources.
//
// A DB is used from one goroutine.
package storage
