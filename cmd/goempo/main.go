// Command goempo is the goempo node: it opens the state datadir, executes
// blocks from the local chunk archive and then from an upstream JSON-RPC
// node until it reaches the tip, verifies finalization certificates, checks
// every block's gas, receipts root, bloom and state root, and serves
// JSON-RPC over the live state while it follows the chain.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/state"
)

const (
	upstreamTimeout = 30 * time.Second
	cacheScale      = 4
	dirMode         = 0o755
)

type options struct {
	datadir     string
	archive     string
	upstream    string
	http        string
	to          uint64
	certify     bool
	certifyFrom uint64
	flushEvery  uint64
	poll        time.Duration
	summary     string
	recent      int
	hold        bool
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func parse(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("goempo", flag.ContinueOnError)
	fs.StringVar(&o.datadir, "datadir", "", "state directory (loaded with the mainnet genesis when empty)")
	fs.StringVar(&o.archive, "archive", defaultArchive(), "chunk archive directory (goempo-dev fetch format); empty disables it")
	fs.StringVar(&o.upstream, "upstream", "https://rpc.tempo.xyz", "upstream JSON-RPC node for blocks, the tip and certificates")
	fs.StringVar(&o.http, "http", "127.0.0.1:8545", "JSON-RPC listen address; empty disables it")
	fs.Uint64Var(&o.to, "to", 0, "stop after executing this block (0: follow the tip)")
	fs.BoolVar(&o.certify, "certify", true, "verify finalization certificates before executing")
	fs.Uint64Var(&o.certifyFrom, "certify-from", node.MainnetCertifyFrom, "first height whose certificate the upstream serves")
	fs.Uint64Var(&o.flushEvery, "flush", 10_000, "flush the state to disk every N blocks")
	fs.DurationVar(&o.poll, "poll", 500*time.Millisecond, "wait between tip polls")
	fs.StringVar(&o.summary, "summary", "", "write a JSON summary here on exit")
	fs.IntVar(&o.recent, "recent", node.DefaultRecentBlocks, "recent blocks kept with receipts for JSON-RPC")
	fs.BoolVar(&o.hold, "hold", false, "after -to, keep serving JSON-RPC until interrupted")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.datadir == "" {
		return o, errors.New("goempo: -datadir is required")
	}
	return o, nil
}

func defaultArchive() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "cache", "mainnet")
}

func openNode(o *options, up *node.Upstream) (*node.Node, error) {
	if err := os.MkdirAll(o.datadir, dirMode); err != nil {
		return nil, err
	}
	dir, err := goio.NewDirFS(o.datadir)
	if err != nil {
		return nil, err
	}
	cfg := node.Config{
		State:        dir,
		StateOptions: state.Options{MemtableBytes: 256 << 20, CacheSlots: cacheScale << 22, CacheAccounts: cacheScale << 20},
		CertifyFrom:  o.certifyFrom,
		RecentBlocks: o.recent,
	}
	if o.archive != "" {
		afs, err := goio.NewDirFS(o.archive)
		if err != nil {
			return nil, err
		}
		cfg.Archive = node.NewArchive(afs)
		cfg.Sources = append(cfg.Sources, cfg.Archive)
	}
	cfg.Sources = append(cfg.Sources, up)
	if o.certify {
		cfg.Finality = up
	}
	return node.Open(cfg)
}

func run(args []string) int {
	o, err := parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	up := node.NewUpstream(goio.NewHTTPCaller(o.upstream, uint64(upstreamTimeout)))
	n, err := openNode(&o, up)
	if err != nil {
		fmt.Fprintln(os.Stderr, "goempo: open:", err)
		return 1
	}
	head := n.Head()
	fmt.Fprintf(os.Stderr, "goempo: state at block %d %s\n", head.Number, head.Hash)
	f := &follower{o: &o, n: n, up: up, began: time.Now(), lastLog: time.Now(), flushed: head.Number, flushedAt: time.Now()}
	if err := f.serve(); err != nil {
		fmt.Fprintln(os.Stderr, "goempo: rpc:", errors.Join(err, n.Close()))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return f.finish(f.loop(ctx))
}
