package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/rpc"
)

const (
	stepsMax      = 1 << 40
	retriesMax    = 20
	backoffMax    = 10 * time.Second
	logEvery      = 10 * time.Second
	tipRefresh    = 15 * time.Second
	nearTip       = 64
	fileMode      = 0o644
	flushInterval = 5 * time.Minute
)

const _ = uint(stepsMax - retriesMax)

const _ = uint(tipRefresh - backoffMax)

const _ = uint(1<<62 - stepsMax)

type follower struct {
	o                             *options
	n                             *node.Node
	up                            *node.Upstream
	transport                     *rpc.Transport
	serving                       sync.WaitGroup
	listener                      *goio.TCPListener
	tip                           uint64
	tipAt                         time.Time
	began                         time.Time
	lastLog                       time.Time
	prevExec, prevCommit, prevGas uint64
	prevLog                       node.Stats
	perfAt                        time.Time
	flushed                       uint64
	flushedAt                     time.Time
	retries                       int
}

func (f *follower) serve() error {
	if f.o.http == "" {
		return nil
	}
	l, err := goio.ListenTCP(f.o.http)
	if err != nil {
		return err
	}
	f.listener = l
	f.transport = rpc.NewTransport(f.n.Server(rpc.Config{ChainID: rpc.MainnetChainID}))
	f.serving.Go(func() {
		if err := f.transport.Serve(l); err != nil {
			fmt.Fprintln(os.Stderr, "goempo: rpc serve:", err)
		}
	})
	fmt.Fprintln(os.Stderr, "goempo: JSON-RPC on", l.Address())
	return nil
}

func (f *follower) refreshTip(head uint64) error {
	if head+node.BatchMax < f.tip && time.Since(f.tipAt) < tipRefresh {
		return nil
	}
	return f.pollTip()
}

func (f *follower) pollTip() error {
	tip, err := f.up.Tip()
	if err != nil {
		return err
	}
	f.tip, f.tipAt = tip, time.Now()
	return nil
}

func (f *follower) target() uint64 {
	if f.o.to == 0 {
		return f.tip
	}
	return min(f.o.to, f.tip)
}

func (f *follower) loop(ctx context.Context) error {
	for range stepsMax {
		if ctx.Err() != nil {
			return nil
		}
		head := f.n.Head().Number
		if f.o.to != 0 && head >= f.o.to {
			if !f.o.hold {
				return nil
			}
			f.sleep(ctx, f.o.poll)
			continue
		}
		err := f.step(head)
		if err == nil {
			f.retries = 0
			continue
		}
		if failed := f.n.Failed(); failed != nil {
			return failed
		}
		f.retries++
		if f.retries > retriesMax && f.o.to != 0 {
			return err
		}
		fmt.Fprintf(os.Stderr, "goempo: retry %d: %v\n", f.retries, err)
		f.sleep(ctx, min(f.o.poll<<min(f.retries, 8), backoffMax))
	}
	return nil
}

func (f *follower) step(head uint64) error {
	if err := f.refreshTip(head); err != nil {
		return err
	}
	if head >= f.target() {
		time.Sleep(f.o.poll)
		return f.pollTip()
	}
	p, err := f.n.Advance(node.Target{To: f.target(), Tip: f.tip})
	if err != nil {
		return err
	}
	for _, b := range p.Blocks {
		f.report(b)
	}
	if p.Waiting {
		time.Sleep(f.o.poll)
	}
	return f.maybeFlush(p.Head)
}

func (f *follower) report(b *rpc.BlockData) {
	if f.transport != nil {
		if err := f.transport.Publish(b); err != nil {
			fmt.Fprintln(os.Stderr, "goempo: publish:", err)
		}
	}
	n := b.Number()
	st := f.n.Stats()
	execNs, commitNs := st.ExecNanos-f.prevExec, st.CommitNanos-f.prevCommit
	f.prevExec, f.prevCommit = st.ExecNanos, st.CommitNanos
	if time.Since(f.perfAt) >= logEvery {
		f.perfAt = time.Now()
		mgas := func(gas, ns uint64) float64 {
			if ns == 0 {
				return 0
			}
			return float64(gas) / float64(ns) * 1e3
		}
		fmt.Fprintf(os.Stderr, "perf: blocks %d gas %d exec %.3fs commit %.3fs | exec %.1f Mgas/s, exec+commit %.1f Mgas/s, %.0fus/block exec, %.0fus/block commit\n",
			st.Blocks, st.Gas, float64(st.ExecNanos)/1e9, float64(st.CommitNanos)/1e9,
			mgas(st.Gas, st.ExecNanos), mgas(st.Gas, st.ExecNanos+st.CommitNanos),
			float64(st.ExecNanos)/1e3/float64(max(st.Blocks, 1)), float64(st.CommitNanos)/1e3/float64(max(st.Blocks, 1)))
	}
	if f.tip-min(f.tip, n) < nearTip {
		h := &b.Block.Header.Inner
		fmt.Fprintf(os.Stderr, "block %d %s txs=%d gas=%d exec=%dus commit=%dus stateRoot=%s ok\n", n, b.Hash, len(b.Block.Transactions), h.GasUsed, execNs/1000, commitNs/1000, h.StateRoot)
		return
	}
	if time.Since(f.lastLog) >= logEvery {
		f.lastLog = time.Now()
		s := f.n.Stats()
		secs := time.Since(f.began).Seconds()
		d := s.Since(f.prevLog)
		f.prevLog = s
		fmt.Fprintf(os.Stderr, "progress: head %d tip %d executed %d (%.0f blk/s) certified %d | ms fetch %d verify %d certify %d wait %d exec %d commit %d flush %d\n",
			n, f.tip, s.Blocks, float64(s.Blocks)/secs, s.Certify.Certified,
			d.FetchNanos/1e6, d.VerifyNanos/1e6, d.CertifyNanos/1e6, d.WaitNanos/1e6, d.ExecNanos/1e6, d.CommitNanos/1e6, d.FlushNanos/1e6)
	}
}

func (f *follower) maybeFlush(head uint64) error {
	due := head >= f.flushed+f.o.flushEvery || (head > f.flushed && time.Since(f.flushedAt) >= flushInterval)
	if f.o.flushEvery == 0 || !due {
		return nil
	}
	f.flushed, f.flushedAt = head, time.Now()
	return f.n.Flush()
}

func (f *follower) sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

type summary struct {
	Rung        string  `json:"rung"`
	Chain       string  `json:"chain"`
	Start       uint64  `json:"start"`
	Head        uint64  `json:"head"`
	HeadHash    string  `json:"head_hash"`
	Tip         uint64  `json:"tip"`
	Blocks      uint64  `json:"blocks_executed"`
	Txs         uint64  `json:"txs"`
	Gas         uint64  `json:"gas"`
	Certified   uint64  `json:"certificates_verified"`
	Linked      uint64  `json:"linked_by_hash"`
	Unchecked   uint64  `json:"uncertified"`
	Transitions uint64  `json:"epoch_transitions"`
	Trusted     uint64  `json:"boundaries_trusted"`
	Anchor      uint64  `json:"consensus_anchor"`
	Seconds     float64 `json:"seconds"`
	Stages      stages  `json:"stage_seconds"`
	RootChecks  uint64  `json:"root_checks"`
	Failed      string  `json:"failed,omitempty"`
}

type stages struct {
	Fetch   float64 `json:"fetch"`
	Verify  float64 `json:"verify"`
	Certify float64 `json:"certify"`
	Wait    float64 `json:"wait"`
	Exec    float64 `json:"exec"`
	Commit  float64 `json:"commit"`
	Flush   float64 `json:"flush"`
}

func (f *follower) finish(runErr error) int {
	head := f.n.Head()
	s := f.n.Stats()
	sum := summary{
		Rung: "R4", Chain: "mainnet", Start: f.n.Start(), Head: head.Number, HeadHash: head.Hash.String(), Tip: f.tip,
		Blocks: s.Blocks, Txs: s.Txs, Gas: s.Gas, Certified: s.Certify.Certified, Linked: s.Certify.Linked,
		Unchecked: s.Certify.Unchecked, Transitions: s.Certify.Transitions, Trusted: s.Certify.Trusted,
		Anchor: s.Certify.Anchor, Seconds: time.Since(f.began).Seconds(), RootChecks: s.RootChecks,
		Stages: stages{
			Fetch: float64(s.FetchNanos) / 1e9, Verify: float64(s.VerifyNanos) / 1e9, Certify: float64(s.CertifyNanos) / 1e9,
			Wait: float64(s.WaitNanos) / 1e9, Exec: float64(s.ExecNanos) / 1e9, Commit: float64(s.CommitNanos) / 1e9,
			Flush: float64(s.FlushNanos) / 1e9,
		},
	}
	if runErr != nil {
		sum.Failed = runErr.Error()
	}
	closeErr := f.Shutdown()
	out, err := json.MarshalIndent(&sum, "", "  ")
	if err == nil {
		fmt.Println(string(out))
		if f.o.summary != "" {
			err = os.WriteFile(f.o.summary, append(out, '\n'), fileMode)
		}
	}
	if err = errors.Join(err, closeErr); err != nil {
		fmt.Fprintln(os.Stderr, "goempo:", err)
	}
	if runErr != nil || err != nil {
		return 1
	}
	return 0
}

func (f *follower) Shutdown() error {
	var errs []error
	if f.transport != nil {
		errs = append(errs, f.transport.Close())
	}
	f.serving.Wait()
	if err := f.n.Flush(); err != nil && !errors.Is(err, node.ErrTainted) {
		errs = append(errs, err)
	}
	if err := f.n.Close(); errors.Is(err, node.ErrTainted) {
		fmt.Fprintln(os.Stderr, "goempo: not flushing: the last block's state is wrong; the datadir stays at its last flush")
	} else {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
