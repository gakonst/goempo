package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	gosync "sync"
	"time"

	"github.com/gakonst/goempo/internal/consensus"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
)

const (
	certifyItemBlocks  = 2000
	certifyWindowItems = 256
	certifyWorkersMax  = 256
	certifyBatch       = 256
	certifyBlocksMax   = 100_000_000
	certifySpanMax     = certifyItemBlocks - 1
	certifyItemsMax    = certifyBlocksMax/certifyItemBlocks + 2
	certifyEpochsMax   = certifyWindowItems*certifyItemBlocks/consensus.MainnetEpochLength + 2
	tailnetRPC         = "http://100.98.157.16:8545"
)

const _ = uint(certifyWindowItems - certifyWorkersMax)

const _ = uint(chunkBlocks - certifySpanMax)

const _ = uint(certifyItemsMax*certifyItemBlocks - certifyBlocksMax)

const _ = uint(consensus.IdentitiesMax - certifyEpochsMax)

const _ = uint(consensus.BatchMax - certifyBatch)

const _ = uint(chunkBlocks % certifyItemBlocks)

const _ = uint(consensus.MainnetEpochLength - certifyItemBlocks)

var (
	errLink      = errors.New("certify: block does not link to its parent")
	errExtra     = errors.New("certify: boundary block's extra data differs from the one registered")
	errUncovered = errors.New("certify: blocks at the end of the range have no certified descendant")
)

type certifyOptions struct {
	rpc     string
	dir     string
	from    uint64
	to      uint64
	workers int
	out     string
}

type certifyItem struct {
	lo, hi uint64
}

type certifyResult struct {
	item          certifyItem
	firstParent   primitives.Hash
	lastHash      primitives.Hash
	certified     uint64
	lastCertified uint64
	gaps          []uint64
	fromCache     bool
	boundaries    []boundaryExtra
	err           error
}

type boundaryExtra struct {
	height uint64
	extra  []byte
}

type certifySummary struct {
	From              uint64   `json:"from"`
	To                uint64   `json:"to"`
	Blocks            uint64   `json:"blocks"`
	Certificates      uint64   `json:"certificates"`
	Uncertified       uint64   `json:"uncertified"`
	UncertifiedFirst  uint64   `json:"uncertified_first,omitempty"`
	UncertifiedLast   uint64   `json:"uncertified_last,omitempty"`
	FirstEpoch        uint64   `json:"first_epoch"`
	LastEpoch         uint64   `json:"last_epoch"`
	Transitions       uint64   `json:"transitions"`
	IdentityChanges   uint64   `json:"identity_changes"`
	Anchor            string   `json:"anchor"`
	NetworkIdentityAt string   `json:"network_identity_check"`
	CacheBlocks       uint64   `json:"cache_blocks"`
	Seconds           float64  `json:"seconds"`
	Error             string   `json:"error,omitempty"`
	Identities        []string `json:"identities"`
}

func runCertify(args []string) int {
	var o certifyOptions
	fs := flag.NewFlagSet("certify", flag.ContinueOnError)
	fs.StringVar(&o.rpc, "rpc", tailnetRPC, "node answering consensus_getFinalization and debug_getRawBlock")
	fs.StringVar(&o.dir, "dir", defaultChunkDir(), "block chunk cache (blocks not in it are fetched)")
	fs.Uint64Var(&o.from, "from", 44107200-consensus.MainnetEpochLength, "first block")
	fs.Uint64Var(&o.to, "to", 44107200+consensus.MainnetEpochLength-1, "last block")
	fs.IntVar(&o.workers, "workers", 16, "parallel workers")
	fs.StringVar(&o.out, "out", "", "write the JSON summary here")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if o.to < o.from || o.to-o.from >= certifyBlocksMax || o.workers < 1 || o.workers > certifyWorkersMax {
		fmt.Fprintln(os.Stderr, "certify: bad range or workers")
		return 2
	}
	sum, err := certify(o)
	if err != nil {
		sum.Error = err.Error()
	}
	if o.out != "" {
		if werr := writeSummary(o.out, sum); werr != nil {
			fmt.Fprintln(os.Stderr, "certify:", werr)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "certify:", err)
		fmt.Println("R4 certify FAIL")
		return 1
	}
	fmt.Printf("certified %d blocks %d..%d: %d finalization certificates verified, %d without their own certificate (certified through a descendant's parent hash), epochs %d..%d, %d epoch transitions (%d identity changes), anchor %s, %s, %d blocks from the cache, %.0fs\n",
		sum.Blocks, sum.From, sum.To, sum.Certificates, sum.Uncertified, sum.FirstEpoch, sum.LastEpoch, sum.Transitions, sum.IdentityChanges, sum.Anchor, sum.NetworkIdentityAt, sum.CacheBlocks, sum.Seconds)
	fmt.Println("R4 certify PASS")
	return 0
}

func writeSummary(path string, sum certifySummary) error {
	raw, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), fileMode)
}

type rawFetcher struct {
	rpc *rpcClient
}

func (f rawFetcher) rawBlock(n uint64) ([]byte, error) {
	res, err := f.rpc.call("debug_getRawBlock", blockTag(n))
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(res, &s); err != nil {
		return nil, err
	}
	return decodeHex(s)
}

func (f rawFetcher) rawBlocks(it certifyItem) ([][]byte, error) {
	lo, hi := it.lo, it.hi
	span := min(hi-lo, certifySpanMax)
	out := make([][]byte, 0, span+1)
	for k := range span/chunkRequestsMax + 1 {
		start := lo + k*chunkRequestsMax
		end := min(hi, start+chunkRequestsMax-1)
		reqs := make([]rpcRequest, 0, end-start+1)
		for j := range min(end-start+1, chunkRequestsMax) {
			n := start + j
			reqs = append(reqs, rpcRequest{JSONRPC: jsonRPCVersion, ID: int(j), Method: "debug_getRawBlock", Params: []any{blockTag(n)}})
		}
		res, err := f.rpc.batch(reqs)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			var s string
			if err := json.Unmarshal(r, &s); err != nil {
				return nil, err
			}
			raw, err := decodeHex(s)
			if err != nil {
				return nil, err
			}
			out = append(out, raw)
		}
	}
	return out, nil
}

func (f rawFetcher) finalizations(it certifyItem) ([][]byte, error) {
	lo, hi := it.lo, it.hi
	span := min(hi-lo, certifySpanMax)
	out := make([][]byte, 0, span+1)
	for k := range span/rpcBatchMax + 1 {
		start := lo + k*rpcBatchMax
		end := min(hi, start+rpcBatchMax-1)
		reqs := make([]rpcRequest, 0, end-start+1)
		for j := range min(end-start+1, rpcBatchMax) {
			n := start + j
			reqs = append(reqs, rpcRequest{JSONRPC: jsonRPCVersion, ID: int(j), Method: "consensus_getFinalization", Params: []any{map[string]uint64{"height": n}}})
		}
		res, err := f.batchLoose(reqs)
		if err != nil {
			return nil, err
		}
		out = append(out, res...)
	}
	return out, nil
}

type certifiedBlockJSON struct {
	Epoch       uint64 `json:"epoch"`
	View        uint64 `json:"view"`
	Certificate string `json:"certificate"`
}

func (f rawFetcher) batchLoose(reqs []rpcRequest) ([][]byte, error) {
	body, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := range rpcRetries {
		raw, err := f.rpc.post(body)
		if err == nil {
			var resps []rpcResponse
			if err = json.Unmarshal(raw, &resps); err == nil && len(resps) != len(reqs) {
				err = fmt.Errorf("rpc: got %d responses for %d requests", len(resps), len(reqs))
			}
			if err == nil {
				out := make([][]byte, len(reqs))
				for _, r := range resps {
					if r.ID < 0 || r.ID >= len(reqs) {
						return nil, fmt.Errorf("rpc: response id %d out of range", r.ID)
					}
					if r.Error != nil {
						if r.Error.Code != rpcCodeUnavailable {
							return nil, fmt.Errorf("%w: %d %s", errRPC, r.Error.Code, r.Error.Message)
						}
						continue
					}
					var cb certifiedBlockJSON
					if err := json.Unmarshal(r.Result, &cb); err != nil {
						return nil, err
					}
					cert, err := hex.DecodeString(strings.TrimPrefix(cb.Certificate, "0x"))
					if err != nil {
						return nil, err
					}
					out[r.ID] = cert
				}
				return out, nil
			}
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "rpc: attempt %d: %v\n", attempt, err)
		time.Sleep(min(rpcRetryBackoff<<attempt, rpcBackoffMax))
	}
	return nil, lastErr
}

const rpcCodeUnavailable = 503

func certify(o certifyOptions) (sum certifySummary, err error) {
	started := time.Now()
	chain := consensus.NewMainnetChain()
	ep := chain.Epocher()
	net := chain.Network()
	f := rawFetcher{rpc: newRPCClient(o.rpc)}
	sum = certifySummary{From: o.from, To: o.to, FirstEpoch: ep.Epoch(o.from), LastEpoch: ep.Epoch(o.to)}
	defer func() { sum.Seconds = time.Since(started).Seconds() }()

	identities := map[uint64][]byte{}
	if sum.FirstEpoch >= net.FromEpoch {
		sum.Anchor = fmt.Sprintf("network identity (chainspec, from epoch %d)", net.FromEpoch)
	} else {
		boundary := ep.First(sum.FirstEpoch) - 1
		raw, err := f.rawBlock(boundary)
		if err != nil {
			return sum, err
		}
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			return sum, err
		}
		if _, err := chain.RegisterBoundary(boundary, blk.Header.Inner.ExtraData); err != nil {
			return sum, err
		}
		sum.Anchor = fmt.Sprintf("dkg outcome in block %d %s (taken on trust, before the range)", boundary, blk.Hash())
	}
	if id, ok := chain.Identity(sum.FirstEpoch); ok {
		b := id.Bytes()
		identities[sum.FirstEpoch] = b[:]
	}
	sum.NetworkIdentityAt = "range does not cross the network identity's first epoch"
	if sum.FirstEpoch >= net.FromEpoch {
		sum.NetworkIdentityAt = "anchored at the network identity"
	}

	items := certifyItems(certifyItem{lo: o.from, hi: o.to})
	var prevHash primitives.Hash
	havePrev := false
	var lastCertified uint64
	haveCertified := false
	for w := 0; w < len(items); w += certifyWindowItems {
		window := items[w:min(len(items), w+certifyWindowItems)]
		registered := map[uint64][]byte{}
		firstEpoch := ep.Epoch(window[0].lo)
		epochs := min(ep.Epoch(window[len(window)-1].hi)-firstEpoch, certifyEpochsMax)
		for k := range epochs {
			last := ep.Last(firstEpoch + k)
			raw, err := f.rawBlock(last)
			if err != nil {
				return sum, err
			}
			blk, err := types.DecodeBlock(raw)
			if err != nil {
				return sum, err
			}
			next, err := chain.RegisterBoundary(last, blk.Header.Inner.ExtraData)
			if err != nil {
				return sum, err
			}
			if next == net.FromEpoch {
				sum.NetworkIdentityAt = fmt.Sprintf("dkg outcome in block %d equals the chainspec network identity for epoch %d", last, next)
			}
			registered[last] = blk.Header.Inner.ExtraData
			id, _ := chain.Identity(next)
			b := id.Bytes()
			if prev := identities[next-1]; prev != nil && !bytes.Equal(prev, b[:]) {
				sum.IdentityChanges++
			}
			identities[next] = b[:]
			sum.Transitions++
		}
		results := runCertifyWindow(chain, f, o, window)
		for _, r := range results {
			if r.err != nil {
				return sum, fmt.Errorf("blocks %d..%d: %w", r.item.lo, r.item.hi, r.err)
			}
			if havePrev && r.firstParent != prevHash {
				return sum, &consensus.HeightError{Height: r.item.lo, Err: errLink}
			}
			prevHash, havePrev = r.lastHash, true
			for _, b := range r.boundaries {
				want, ok := registered[b.height]
				if ok && !bytes.Equal(want, b.extra) {
					return sum, &consensus.HeightError{Height: b.height, Err: errExtra}
				}
			}
			sum.Blocks += r.item.hi - r.item.lo + 1
			sum.Certificates += r.certified
			if r.fromCache {
				sum.CacheBlocks += r.item.hi - r.item.lo + 1
			}
			for _, h := range r.gaps {
				if sum.Uncertified == 0 {
					sum.UncertifiedFirst = h
				}
				sum.UncertifiedLast = h
				sum.Uncertified++
			}
			if r.certified > 0 {
				haveCertified = true
				lastCertified = r.lastCertified
			}
		}
		fmt.Fprintf(os.Stderr, "certify: through %d: %d blocks, %d certificates, %d missing, %d transitions, %.0fs\n",
			window[len(window)-1].hi, sum.Blocks, sum.Certificates, sum.Uncertified, sum.Transitions, time.Since(started).Seconds())
	}
	for _, e := range slices.Sorted(maps.Keys(identities)) {
		sum.Identities = append(sum.Identities, fmt.Sprintf("%d:%x", e, identities[e][:8]))
	}
	if !haveCertified || lastCertified < o.to {
		return sum, errUncovered
	}
	return sum, nil
}

func certifyItems(span certifyItem) []certifyItem {
	from, to := span.lo, span.hi
	count := min(to/certifyItemBlocks-from/certifyItemBlocks+1, certifyItemsMax)
	items := make([]certifyItem, 0, count)
	for k := range count {
		lo := max(from, (from/certifyItemBlocks+k)*certifyItemBlocks)
		hi := min(to, (from/certifyItemBlocks+k+1)*certifyItemBlocks-1)
		items = append(items, certifyItem{lo: lo, hi: hi})
	}
	return items
}

func runCertifyWindow(chain *consensus.Chain, f rawFetcher, o certifyOptions, window []certifyItem) []certifyResult {
	results := make([]certifyResult, len(window))
	workers := min(o.workers, len(window))
	var wg gosync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for k := range len(window) {
				if k%workers == w {
					results[k] = certifyRun(chain, f, o.dir, window[k])
				}
			}
		})
	}
	wg.Wait()
	return results
}

type certifySource struct {
	raws      [][]byte
	fromCache bool
}

func certifyBlocks(f rawFetcher, dir string, it certifyItem) (certifySource, error) {
	start := chunkStart(it.lo)
	if chunkPresent(dir, start) {
		raws, err := readChunk(dir, start)
		if err == nil && uint64(len(raws)) == chunkBlocks {
			return certifySource{raws: raws[it.lo-start : it.hi-start+1], fromCache: true}, nil
		}
	}
	raws, err := f.rawBlocks(it)
	return certifySource{raws: raws}, err
}

func certifyRun(chain *consensus.Chain, f rawFetcher, dir string, it certifyItem) certifyResult {
	r := certifyResult{item: it}
	src, err := certifyBlocks(f, dir, it)
	if err != nil {
		r.err = err
		return r
	}
	raws := src.raws
	r.fromCache = src.fromCache
	certs, err := f.finalizations(it)
	if err != nil {
		r.err = err
		return r
	}
	ep := chain.Epocher()
	batch := make([]consensus.Certified, 0, certifyBatch)
	contexts := make([]consensus.Proposal, 0, certifyBatch)
	flush := func() error {
		err := chain.Verify(batch)
		r.certified += uint64(len(batch))
		batch, contexts = batch[:0], contexts[:0]
		return err
	}
	var parent primitives.Hash
	for i, raw := range raws {
		h := it.lo + uint64(i)
		blk, err := types.DecodeBlock(raw)
		if err != nil {
			r.err = &consensus.HeightError{Height: h, Err: err}
			return r
		}
		if blk.Header.Inner.Number != h {
			r.err = &consensus.HeightError{Height: h, Err: errChunk}
			return r
		}
		hash := blk.Hash()
		if i == 0 {
			r.firstParent = blk.Header.Inner.ParentHash
		} else if blk.Header.Inner.ParentHash != parent {
			r.err = &consensus.HeightError{Height: h, Err: errLink}
			return r
		}
		parent = hash
		if ep.IsLast(h) {
			r.boundaries = append(r.boundaries, boundaryExtra{height: h, extra: blk.Header.Inner.ExtraData})
		}
		if certs[i] == nil {
			r.gaps = append(r.gaps, h)
			continue
		}
		c := consensus.Certified{Height: h, Hash: consensus.Digest(hash), Certificate: certs[i]}
		if ctx := blk.Header.Consensus; ctx != nil {
			contexts = append(contexts, consensus.Proposal{Round: consensus.Round{Epoch: ctx.Epoch, View: ctx.View}, Parent: ctx.ParentView})
			c.Context = &contexts[len(contexts)-1]
		}
		batch = append(batch, c)
		r.lastCertified = h
		if len(batch) == certifyBatch {
			if r.err = flush(); r.err != nil {
				return r
			}
		}
	}
	r.lastHash = parent
	if len(batch) > 0 {
		r.err = flush()
	}
	return r
}
