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

const serveConnsMax = 1 << 30

const _ = uint(serveConnsMax - diffBlocksMax)

type servePaths struct {
	blocks string
	chunks string
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8545", "address to serve JSON-RPC (HTTP and WebSocket) on, one connection at a time")
	blocks := fs.String("blocks", defaultBlockDir(), "fetched blocks with receipts (fetch)")
	chunks := fs.String("chunks", defaultChunkDir(), "raw block chunks for replay (fetch-range)")
	to := fs.Uint64("state", 0, "replay state up to this block and serve state methods there")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	srv, err := buildServer(servePaths{blocks: *blocks, chunks: *chunks}, min(*to, diffBlocksMax))
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
	for range serveConnsMax {
		conn, err := l.Accept()
		if err != nil {
			break
		}
		tr.ServeConn(conn)
	}
	return 0
}

func buildServer(paths servePaths, to uint64) (*rpc.Server, error) {
	chain := rpc.NewMemChain()
	if err := loadFetchedBlocks(chain, paths.blocks); err != nil {
		return nil, err
	}
	if err := loadChunkBlocks(chain, paths.chunks, to); err != nil {
		return nil, err
	}
	r, err := newReplayer(paths.chunks)
	if err != nil {
		return nil, err
	}
	view := newChunkChain(chain, paths.chunks)
	if err := replayTo(r, chain, to); err != nil {
		return nil, err
	}
	states := &replayState{r: r}
	exec := &rpcexec.Executor{ChainID: hardfork.MainnetChainID, Chain: view, States: states}
	return rpc.NewServer(rpc.Config{ChainID: rpc.MainnetChainID}, view, states, exec), nil
}
