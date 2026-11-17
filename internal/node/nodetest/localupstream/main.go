package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	goio "github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/node"
	"github.com/gakonst/goempo/internal/node/nodetest"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("localupstream", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:18600", "address to serve JSON-RPC on")
	chunks := fs.String("chunks", "", "chunk archive directory")
	tip := fs.Uint64("tip", 0, "highest block served, reported by eth_blockNumber")
	delay := fs.Uint64("delay-ms", 0, "added latency per HTTP request in milliseconds")
	reject := fs.Uint64("reject-every", 0, "refuse every Nth request with 429; 0 never")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dir, err := goio.NewDirFS(*chunks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "localupstream:", err)
		return 1
	}
	u := &nodetest.LocalUpstream{
		Archive: node.NewArchive(dir), Tip: *tip,
		Delay: time.Duration(*delay) * time.Millisecond, RejectEvery: *reject,
	}
	srv := &http.Server{Addr: *listen, Handler: u, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintln(os.Stderr, "localupstream: serving on", *listen)
	err = srv.ListenAndServe()
	fmt.Fprintln(os.Stderr, "localupstream:", err)
	return 1
}
