package rpc

import (
	"errors"
	"slices"

	"github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const (
	ConnsActiveMax = 256
	acceptsMax     = 1 << 40
)

const _ = uint(acceptsMax - ConnsActiveMax)

var ErrTransportClosed = errors.New("rpc: transport closed")

func (t *Transport) Serve(l io.Listener) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return ErrTransportClosed
	}
	t.listener = l
	done := t.done
	t.mu.Unlock()
	slots := make(chan struct{}, ConnsActiveMax)
	for range acceptsMax {
		select {
		case slots <- struct{}{}:
		case <-done:
			return nil
		}
		conn, err := l.Accept()
		if err != nil {
			release(slots)
			if t.isClosed() {
				return nil
			}
			return err
		}
		id, ok := t.track(conn)
		if !ok {
			release(slots)
			return errors.Join(conn.Close(), ErrTransportClosed)
		}
		t.wg.Go(func() {
			defer release(slots)
			defer t.untrack(id)
			t.ServeConn(conn)
		})
	}
	assert.Fail("rpc transport accepted acceptsMax connections")
	return nil
}

func release(slots chan struct{}) {
	select {
	case <-slots:
	default:
	}
}

func (t *Transport) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *Transport) track(conn io.Conn) (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, false
	}
	t.connSeq++
	t.conns[t.connSeq] = conn
	return t.connSeq, true
}

func (t *Transport) untrack(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, id)
}

func (t *Transport) Close() error {
	t.mu.Lock()
	if !t.closed {
		close(t.done)
	}
	t.closed = true
	l := t.listener
	conns := make([]io.Conn, 0, len(t.conns))
	for _, id := range sortedConnIDs(t.conns) {
		conns = append(conns, t.conns[id])
	}
	t.mu.Unlock()
	var err error
	if l != nil {
		err = l.Close()
	}
	for _, c := range conns {
		err = errors.Join(err, c.Close())
	}
	t.wg.Wait()
	return err
}

func sortedConnIDs(m map[uint64]io.Conn) []uint64 {
	ids := make([]uint64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
