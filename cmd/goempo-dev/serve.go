package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/rpc"
	"github.com/gakonst/goempo/internal/rpcexec"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

type servePaths struct {
	blocks      string
	chunks      string
	datadir     string
	proofWindow uint64
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8545", "address to serve JSON-RPC (HTTP and WebSocket) on, serving up to rpc.ConnsActiveMax connections at once")
	blocks := fs.String("blocks", defaultBlockDir(), "fetched blocks with receipts (fetch)")
	chunks := fs.String("chunks", defaultChunkDir(), "raw block chunks for replay (fetch-range)")
	to := fs.Uint64("state", 0, "replay state up to this block and serve state methods there")
	window := fs.Uint64("proof-window", 0, "blocks behind the head that eth_getProof serves (0: the reference's 5000)")
	datadir := fs.String("datadir", "", "start from the replay state kept here (replay -datadir) instead of genesis; the served state lives in memory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths := servePaths{blocks: *blocks, chunks: *chunks, datadir: *datadir, proofWindow: *window}
	srv, err := buildServer(paths, min(*to, replayBlocksMax))
	if err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		return 1
	}
	l, err := io.ListenTCP(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "serving JSON-RPC on", l.Address())
	tr := rpc.NewTransport(srv)
	if err := tr.Serve(l); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		return 1
	}
	return 0
}

func buildServer(paths servePaths, to uint64) (*rpc.Server, error) {
	r, err := newReplayerStore(paths.chunks, storeConfig{datadir: paths.datadir})
	if err != nil {
		return nil, err
	}
	to = max(min(to, r.head+diffBlocksMax, replayBlocksMax), r.head)
	chain := rpc.NewMemChain()
	if err := loadFetchedBlocks(chain, paths.blocks); err != nil {
		return nil, err
	}
	if err := loadChunkBlocks(chain, paths.chunks, chunkSpan{start: r.head, to: to}); err != nil {
		return nil, err
	}
	view := newChunkChain(chain, paths.chunks)
	if err := replayTo(r, chain, to); err != nil {
		return nil, err
	}
	if r.failed != nil {
		fmt.Fprintf(os.Stderr, "serve: replay stopped at block %d: %s\n", r.head, r.failed.String())
	}
	fmt.Fprintln(os.Stderr, "serve: state replayed to block", r.head)
	states := &replayState{r: r}
	exec := &rpcexec.Executor{ChainID: hardfork.MainnetChainID, Chain: view, States: states, Blocks: r.exec, Lock: &states.mu}
	return rpc.NewServer(rpc.Config{ChainID: rpc.MainnetChainID, ProofWindow: paths.proofWindow}, view, states, exec), nil
}
