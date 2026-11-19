package storage

import "github.com/kapetan-io/tiger/assert"

type ReadCursor struct{ view DB }

func (db *DB) ReadCursor() *ReadCursor {
	assert.Ok(len(db.segs) <= segmentsMax, "storage read cursor segment bound")
	c := &ReadCursor{view: DB{mem: db.mem, segs: make([]*segment, len(db.segs))}}
	for i, s := range db.segs {
		private := *s
		private.scratch = nil
		c.view.segs[i] = &private
	}
	return c
}

func (c *ReadCursor) Get(key []byte) ([]byte, error) { return c.view.Get(key) }
