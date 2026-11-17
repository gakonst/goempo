package io_test

import (
	"testing"

	goio "github.com/gakonst/goempo/internal/io"
)

func TestFixedClockAdvance(t *testing.T) {
	c := &goio.FixedClock{Monotonic: 1, Realtime: 2}
	c.Advance(10)
	if c.MonotonicNanos() != 11 || c.RealtimeNanos() != 12 {
		t.Fatalf("got %d %d", c.MonotonicNanos(), c.RealtimeNanos())
	}
}
