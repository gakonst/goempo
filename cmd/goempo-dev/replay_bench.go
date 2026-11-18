package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/gakonst/goempo/internal/tempo/executor"
)

type benchCounters struct {
	gas     uint64
	txs     uint64
	blocks  uint64
	buckets [execBuckets]execBucket
}

const execBuckets = 7

var execBucketMins = [execBuckets]int{0, 2, 4, 16, 64, 256, 1024}

type execBucket struct {
	MinTxs  int     `json:"min_txs"`
	Blocks  uint64  `json:"blocks"`
	Txs     uint64  `json:"txs"`
	Seconds float64 `json:"exec_seconds"`
}

func (c *benchCounters) exec(txs int, took time.Duration) {
	b := 0
	for i := 1; i < execBuckets; i++ {
		if txs >= execBucketMins[i] {
			b = i
		}
	}
	e := &c.buckets[b]
	e.MinTxs = execBucketMins[b]
	e.Blocks++
	e.Txs += uint64(txs)
	e.Seconds += took.Seconds()
}

type benchStats struct {
	Blocks   uint64                 `json:"blocks"`
	Gas      uint64                 `json:"gas"`
	Txs      uint64                 `json:"txs"`
	GasSec   float64                `json:"gas_per_sec"`
	TxsSec   float64                `json:"txs_per_sec"`
	CPUSec   float64                `json:"cpu_seconds"`
	MaxRSSMB float64                `json:"max_rss_mb"`
	GoMaxP   int                    `json:"gomaxprocs"`
	Host     string                 `json:"host,omitempty"`
	Buckets  []execBucket           `json:"exec_buckets,omitempty"`
	Parallel executor.ParallelStats `json:"parallel"`
}

type profiler struct {
	cpu *os.File
	mem string
}

func (c *benchCounters) add(gas uint64, txs int) {
	c.gas += gas
	c.txs += uint64(txs)
	c.blocks++
}

type profilePaths struct {
	cpu string
	mem string
}

func startProfiles(paths profilePaths) (*profiler, error) {
	p := &profiler{mem: paths.mem}
	if paths.cpu == "" {
		return p, nil
	}
	f, err := os.Create(paths.cpu)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		return nil, fmt.Errorf("cpu profile: %w", err)
	}
	p.cpu = f
	return p, nil
}

func (p *profiler) stop() error {
	if p.cpu != nil {
		pprof.StopCPUProfile()
		if err := p.cpu.Close(); err != nil {
			return err
		}
	}
	if p.mem == "" {
		return nil
	}
	f, err := os.Create(p.mem)
	if err != nil {
		return err
	}
	if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
		return err
	}
	return f.Close()
}

func (c *benchCounters) stats(took time.Duration) benchStats {
	s := benchStats{Blocks: c.blocks, Gas: c.gas, Txs: c.txs, GoMaxP: runtime.GOMAXPROCS(0)}
	for i := range c.buckets {
		if c.buckets[i].Blocks > 0 {
			s.Buckets = append(s.Buckets, c.buckets[i])
		}
	}
	if took > 0 {
		s.GasSec = float64(c.gas) / took.Seconds()
		s.TxsSec = float64(c.txs) / took.Seconds()
	}
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		s.CPUSec = time.Duration(ru.Utime.Nano() + ru.Stime.Nano()).Seconds()
		s.MaxRSSMB = float64(ru.Maxrss) / 1024
	}
	if h, err := os.Hostname(); err == nil {
		s.Host = h
	}
	return s
}
