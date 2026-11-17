package io

type Clock interface {
	MonotonicNanos() uint64
	RealtimeNanos() uint64
}

type FixedClock struct {
	Monotonic uint64
	Realtime  uint64
}

func (c *FixedClock) MonotonicNanos() uint64 { return c.Monotonic }

func (c *FixedClock) RealtimeNanos() uint64 { return c.Realtime }

func (c *FixedClock) Advance(nanos uint64) {
	c.Monotonic += nanos
	c.Realtime += nanos
}
