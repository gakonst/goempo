package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/p2p"
	"github.com/gakonst/goempo/internal/p2p/rlpx"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/sync"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

const (
	followTimeout     = 30 * time.Second
	followIdle        = 500 * time.Millisecond
	followBatch       = 32
	followCheckBatch  = 50
	followRetries     = 8
	followStepsMax    = 1 << 40
	mainnetNetworkID  = 4217
	mainnetGenesisHex = "0x49d7ec7085e77bf5a403d0fcb4cfc42a4084a89dfff60477579c5e09c9e03c54"
	tailscaleSocket   = "/run/tailscale/tailscaled.sock"
	clientName        = "goempo/follow"
)

const _ = uint(followTimeout - followIdle)

const _ = uint(rpcBatchMax - followCheckBatch)

const _ = uint(followStepsMax - followRetries)

type followOptions struct {
	upstream  string
	check     string
	peer      string
	tailscale string
	last      uint64
	steps     uint64
	batch     int
	certify   bool
}

func runFollow(args []string) int {
	var o followOptions
	fs := flag.NewFlagSet("follow", flag.ContinueOnError)
	fs.StringVar(&o.upstream, "upstream", mainnetRPC, "JSON-RPC upstream to take blocks from (debug_getRawBlock)")
	fs.StringVar(&o.check, "check", mainnetRPC, "JSON-RPC node whose block hashes every executed block is compared with")
	fs.StringVar(&o.peer, "peer", "", "take blocks from this devp2p peer (enode URL) instead of the upstream")
	fs.StringVar(&o.tailscale, "tailscale", "", "dial the peer through the tailnet at this host:port")
	fs.Uint64Var(&o.last, "last", 1000, "start this many blocks below the tip")
	fs.Uint64Var(&o.steps, "steps", 0, "stop after this many follower steps once the tip is reached (0: follow forever)")
	fs.IntVar(&o.batch, "batch", followBatch, "blocks per request")
	fs.BoolVar(&o.certify, "certify", true, "verify every block's finalization certificate (consensus_getFinalization from the upstream)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := follow(o); err != nil {
		fmt.Fprintln(os.Stderr, "follow:", err)
		fmt.Println("R4 FAIL")
		return 1
	}
	return 0
}

type checker struct {
	head    sync.Link
	rpc     *rpcClient
	pending []sync.Link
	checked uint64
	txs     int
	cert    *followCertifier
}

func (c *checker) Execute(block *types.Block) error {
	c.head = sync.Link{Number: block.Header.Inner.Number, Hash: block.Hash()}
	c.txs += len(block.Transactions)
	c.pending = append(c.pending, c.head)
	if c.cert != nil {
		if err := c.cert.add(block); err != nil {
			return err
		}
	}
	if len(c.pending) >= followCheckBatch {
		return c.flush()
	}
	return nil
}

func (c *checker) Head() sync.Link { return c.head }

func (c *checker) flush() error {
	if c.cert != nil {
		if err := c.cert.flush(); err != nil {
			return err
		}
	}
	if len(c.pending) == 0 {
		return nil
	}
	numbers := make([]uint64, len(c.pending))
	for i, l := range c.pending {
		numbers[i] = l.Number
	}
	hashes, err := blockHashes(c.rpc, numbers)
	if err != nil {
		return err
	}
	for i, l := range c.pending {
		if hashes[i] != l.Hash {
			return fmt.Errorf("block %d: hash %s, the check node has %s", l.Number, l.Hash, hashes[i])
		}
	}
	c.checked += uint64(len(c.pending))
	c.pending = c.pending[:0]
	return nil
}

type blockSummary struct {
	Number    string `json:"number"`
	Hash      string `json:"hash"`
	Timestamp string `json:"timestamp"`
}

func blockSummaries(c *rpcClient, tags []string) ([]blockSummary, error) {
	reqs := make([]rpcRequest, len(tags))
	for i, tag := range tags {
		reqs[i] = rpcRequest{JSONRPC: jsonRPCVersion, ID: i, Method: "eth_getBlockByNumber", Params: []any{tag, false}}
	}
	out, err := c.batch(reqs)
	if err != nil {
		return nil, err
	}
	sums := make([]blockSummary, len(out))
	for i, raw := range out {
		if err := json.Unmarshal(raw, &sums[i]); err != nil {
			return nil, err
		}
	}
	return sums, nil
}

func blockHashes(c *rpcClient, numbers []uint64) ([]primitives.Hash, error) {
	tags := make([]string, len(numbers))
	for i, n := range numbers {
		tags[i] = fmt.Sprintf(blockTagTemplate, n)
	}
	sums, err := blockSummaries(c, tags)
	if err != nil {
		return nil, err
	}
	hashes := make([]primitives.Hash, len(sums))
	for i, s := range sums {
		if hashes[i], err = primitives.HexToHash(s.Hash); err != nil {
			return nil, err
		}
	}
	return hashes, nil
}

type retryCaller struct {
	inner io.Caller
}

func (r retryCaller) Call(body []byte) ([]byte, error) {
	var lastErr error
	for attempt := range followRetries {
		out, err := r.inner.Call(body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		time.Sleep(min(rpcRetryBackoff<<attempt, rpcBackoffMax))
	}
	return nil, lastErr
}

type anchor struct {
	link      sync.Link
	timestamp uint64
	tip       uint64
}

func findAnchor(c *rpcClient, last uint64) (anchor, error) {
	var a anchor
	tip, err := blockSummaries(c, []string{"latest"})
	if err != nil {
		return a, err
	}
	if a.tip, err = parseQuantity(tip[0].Number); err != nil {
		return a, err
	}
	start := a.tip - min(last, a.tip)
	sums, err := blockSummaries(c, []string{fmt.Sprintf(blockTagTemplate, start)})
	if err != nil {
		return a, err
	}
	if a.link.Hash, err = primitives.HexToHash(sums[0].Hash); err != nil {
		return a, err
	}
	if a.timestamp, err = parseQuantity(sums[0].Timestamp); err != nil {
		return a, err
	}
	a.link.Number = start
	return a, nil
}

func peerSource(o followOptions, a anchor) (*sync.Peers, error) {
	node, err := p2p.ParseEnode(o.peer)
	if err != nil {
		return nil, err
	}
	var dialer io.Dialer = io.TCPDialer{TimeoutNanos: uint64(followTimeout)}
	address := node.Address
	if o.tailscale != "" {
		dialer = io.TailscaleDialer{Socket: tailscaleSocket, TimeoutNanos: uint64(followTimeout)}
		address = o.tailscale
	}
	conn, err := dialer.Dial(address)
	if err != nil {
		return nil, err
	}
	key, err := rlpx.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	genesis, err := primitives.HexToHash(mainnetGenesisHex)
	if err != nil {
		return nil, err
	}
	schedule, err := p2p.NewSchedule(p2p.Chain{Genesis: genesis, Forks: p2p.TempoForks(hardfork.MainnetChainID)})
	if err != nil {
		return nil, err
	}
	peer, err := p2p.Connect(conn, node.ID, p2p.Config{
		Key: key, Entropy: rand.Reader, Client: clientName, NetworkID: mainnetNetworkID, Genesis: genesis,
		Schedule: schedule, Head: p2p.Head{Number: a.link.Number, Timestamp: a.timestamp}, HeadHash: a.link.Hash,
		TimeoutNanos: uint64(followTimeout),
	})
	if err != nil {
		return nil, err
	}
	st := peer.Status()
	fmt.Printf("peer %s eth/%d latest %d %s\n", peer.Hello().Client, peer.Version(), st.Latest, st.LatestHash)
	peers, err := sync.NewPeers([]sync.Peer{peer})
	if err != nil {
		return nil, err
	}
	peers.Drop = func(_ sync.Peer, err error) { fmt.Fprintln(os.Stderr, "peer dropped:", err) }
	return peers, nil
}

func follow(o followOptions) error {
	check := newRPCClient(o.check)
	a, err := findAnchor(check, o.last)
	if err != nil {
		return err
	}
	fmt.Printf("anchor %d %s, tip %d\n", a.link.Number, a.link.Hash, a.tip)
	var source sync.Source = sync.NewRPCSource(retryCaller{inner: io.NewHTTPCaller(o.upstream, uint64(followTimeout))})
	if o.peer != "" {
		if source, err = peerSource(o, a); err != nil {
			return err
		}
	}
	exec := &checker{head: a.link, rpc: check}
	if o.certify {
		if exec.cert, err = newFollowCertifier(rawFetcher{rpc: newRPCClient(o.upstream)}, a.link.Number+1); err != nil {
			return err
		}
		fmt.Printf("consensus anchor: %s\n", exec.cert.anchor)
	}
	follower := sync.NewFollower(source, exec, o.batch)
	started := time.Now()
	idle := uint64(0)
	reached := false
	for range uint64(followStepsMax) {
		p, err := follower.Step()
		if err != nil {
			return errors.Join(err, exec.flush())
		}
		if reached {
			idle++
		}
		if o.steps > 0 && idle > o.steps {
			return exec.flush()
		}
		if p.Executed > 0 {
			fmt.Printf("head %d %s tip %d (+%d)\n", p.Head.Number, p.Head.Hash, p.Tip.Number, p.Executed)
			continue
		}
		if err := exec.flush(); err != nil {
			return err
		}
		if !reached {
			reached = true
			fmt.Printf("reached tip %d: %d blocks, %d transactions, %d hashes checked against %s in %s\n",
				p.Head.Number, p.Head.Number-a.link.Number, exec.txs, exec.checked, o.check, time.Since(started).Round(time.Millisecond))
			if exec.cert != nil {
				fmt.Printf("finalization certificates: %d verified, %d certified through a descendant, %d awaiting a certificate, %d epoch transitions\n",
					exec.cert.verified, exec.cert.linked, len(exec.cert.pending), exec.cert.transitions)
			}
			fmt.Println("R4 PASS")
		}
		time.Sleep(followIdle)
	}
	return nil
}
