package cwp2p

const nanosPerSecond = 1_000_000_000

type Bucket struct {
	interval  uint64
	tolerance uint64
	tat       uint64
}

func NewBucket(perSecond uint32) Bucket {
	if perSecond == 0 {
		return Bucket{}
	}
	interval := nanosPerSecond / uint64(perSecond)
	return Bucket{interval: interval, tolerance: interval * uint64(perSecond-1)}
}

func (b *Bucket) Wait(now uint64) uint64 {
	if b.interval == 0 {
		return 0
	}
	tat := max(b.tat, now)
	if tat <= now+b.tolerance {
		return 0
	}
	return tat - b.tolerance - now
}

func (b *Bucket) Take(now uint64) bool {
	if b.Wait(now) > 0 {
		return false
	}
	if b.interval != 0 {
		b.tat = max(b.tat, now) + b.interval
	}
	return true
}
