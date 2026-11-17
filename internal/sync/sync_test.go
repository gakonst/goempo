package sync_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	gosync "sync"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/p2p"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/sync"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

type fixture struct {
	numbers []uint64
	hashes  []primitives.Hash
	blocks  [][]byte
}

func hash(t *testing.T, s string) primitives.Hash {
	t.Helper()
	var h primitives.Hash
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		t.Fatalf("bad hash %q", s)
	}
	copy(h[:], b)
	return h
}

func load(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile("../p2p/testdata/mainnet_49392000_8.txt")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatal("bad fixture line")
		}
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		b, err := hex.DecodeString(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		f.numbers = append(f.numbers, n)
		f.hashes = append(f.hashes, hash(t, fields[1]))
		f.blocks = append(f.blocks, b)
	}
	return f
}

func (f fixture) raws(t *testing.T) []sync.Raw {
	t.Helper()
	out := make([]sync.Raw, len(f.blocks))
	for i, b := range f.blocks {
		r, err := sync.SplitBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = r
	}
	return out
}

func (f fixture) parent(t *testing.T) sync.Link {
	t.Helper()
	block, err := types.DecodeBlock(f.blocks[0])
	if err != nil {
		t.Fatal(err)
	}
	return sync.Link{Number: f.numbers[0] - 1, Hash: block.Header.Inner.ParentHash}
}

func TestVerifyLinksMainnetBlocks(t *testing.T) {
	f := load(t)
	parent := f.parent(t)
	txs := 0
	for i, raw := range f.raws(t) {
		v, err := sync.Verify(parent, raw)
		if err != nil {
			t.Fatalf("block %d: %v", f.numbers[i], err)
		}
		if v.Link.Hash != f.hashes[i] || v.Link.Number != f.numbers[i] {
			t.Fatalf("block %d: hash %x want %x", f.numbers[i], v.Link.Hash, f.hashes[i])
		}
		txs += len(v.Block.Transactions)
		parent = v.Link
	}
	if txs == 0 {
		t.Fatal("fixture has no transactions, so the tx root check is untested")
	}
}

func TestVerifyRejectsAlteredBlocks(t *testing.T) {
	f := load(t)
	raws := f.raws(t)
	parent := f.parent(t)
	if _, err := sync.Verify(sync.Link{Number: parent.Number, Hash: f.hashes[3]}, raws[0]); !errors.Is(err, sync.ErrParent) {
		t.Fatalf("wrong parent: %v", err)
	}
	if _, err := sync.Verify(sync.Link{Number: parent.Number + 1, Hash: parent.Hash}, raws[0]); !errors.Is(err, sync.ErrNumber) {
		t.Fatalf("wrong number: %v", err)
	}
	busiest := 0
	for i, r := range raws {
		if len(r.Body) > len(raws[busiest].Body) {
			busiest = i
		}
	}
	p := parent
	if busiest > 0 {
		p = sync.Link{Number: f.numbers[busiest] - 1, Hash: f.hashes[busiest-1]}
	}
	shuffled := sync.Raw{Header: raws[busiest].Header, Body: append([]byte(nil), raws[busiest].Body...)}
	shuffled.Body[len(shuffled.Body)/2] ^= 0x01
	if _, err := sync.Verify(p, shuffled); err == nil {
		t.Fatal("a body with a flipped byte passed")
	}
	other := sync.Raw{Header: raws[busiest].Header, Body: raws[(busiest+1)%len(raws)].Body}
	if _, err := sync.Verify(p, other); !errors.Is(err, sync.ErrTxRoot) {
		t.Fatalf("another block's body: %v", err)
	}
}

type fakeRPC struct {
	f     fixture
	calls int
}

func (c *fakeRPC) Call(body []byte) ([]byte, error) {
	c.calls++
	var reqs []struct {
		ID     int               `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &reqs); err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, r := range reqs {
		last := len(c.f.numbers) - 1
		switch r.Method {
		case "eth_getBlockByNumber":
			out = append(out, map[string]any{"id": r.ID, "result": map[string]string{
				"number": "0x" + strconv.FormatUint(c.f.numbers[last], 16),
				"hash":   "0x" + hex.EncodeToString(c.f.hashes[last][:]),
			}})
		case "debug_getRawBlock":
			var q string
			if err := json.Unmarshal(r.Params[0], &q); err != nil {
				return nil, err
			}
			n, err := strconv.ParseUint(strings.TrimPrefix(q, "0x"), 16, 64)
			if err != nil {
				return nil, err
			}
			i := int(n - c.f.numbers[0])
			out = append(out, map[string]any{"id": r.ID, "result": "0x" + hex.EncodeToString(c.f.blocks[i])})
		}
	}
	return json.Marshal(out)
}

type recorder struct {
	head  sync.Link
	order []uint64
}

func (r *recorder) Execute(b *types.Block) error {
	if b.Header.Inner.Number != r.head.Number+1 || b.Header.Inner.ParentHash != r.head.Hash {
		return errors.New("out of order")
	}
	r.head = sync.Link{Number: b.Header.Inner.Number, Hash: b.Hash()}
	r.order = append(r.order, r.head.Number)
	return nil
}

func (r *recorder) Head() sync.Link { return r.head }

func TestFollowerReachesUpstreamTip(t *testing.T) {
	f := load(t)
	exec := &recorder{head: f.parent(t)}
	follower := sync.NewFollower(sync.NewRPCSource(&fakeRPC{f: f}), exec, 3)
	var p sync.Progress
	for range 4 {
		var err error
		if p, err = follower.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if p.Head.Hash != f.hashes[7] || len(exec.order) != 8 || p.Executed != 0 {
		t.Fatalf("head %+v executed %v", p.Head, exec.order)
	}
}

type lyingSource struct {
	inner sync.Source
}

func (s lyingSource) Tip() (sync.Link, error) {
	tip, err := s.inner.Tip()
	tip.Hash[0] ^= 1
	return tip, err
}

func (s lyingSource) Blocks(from uint64, count int) ([]sync.Raw, error) {
	return s.inner.Blocks(from, count)
}

func TestFollowerRefusesChainThatMissesTheAnchor(t *testing.T) {
	f := load(t)
	exec := &recorder{head: f.parent(t)}
	follower := sync.NewFollower(lyingSource{inner: sync.NewRPCSource(&fakeRPC{f: f})}, exec, 8)
	if _, err := follower.Step(); !errors.Is(err, sync.ErrAnchor) {
		t.Fatalf("anchor: %v", err)
	}
	if len(exec.order) != 0 {
		t.Fatal("blocks executed before the batch reached the anchor")
	}
}

type store struct {
	f   fixture
	raw []sync.Raw
}

func (s *store) HeaderNumber(h primitives.Hash) (uint64, bool) {
	for i, x := range s.f.hashes {
		if x == h {
			return s.f.numbers[i], true
		}
	}
	return 0, false
}

func (s *store) Header(n uint64) ([]byte, bool) {
	for i, x := range s.f.numbers {
		if x == n {
			return s.raw[i].Header, true
		}
	}
	return nil, false
}

func (s *store) Body(h primitives.Hash) ([]byte, bool) {
	for i, x := range s.f.hashes {
		if x == h {
			return s.raw[i].Body, true
		}
	}
	return nil, false
}

type entropy struct {
	seed    [32]byte
	counter uint64
	buf     []byte
}

func (e *entropy) Read(p []byte) (int, error) {
	for range len(p)/32 + 2 {
		if len(e.buf) >= len(p) {
			break
		}
		var block [40]byte
		copy(block[:], e.seed[:])
		binary.BigEndian.PutUint64(block[32:], e.counter)
		e.counter++
		sum := sha256.Sum256(block[:])
		e.buf = append(e.buf, sum[:]...)
	}
	n := copy(p, e.buf)
	e.buf = e.buf[n:]
	return n, nil
}

func peerConfig(t *testing.T, tag string, f fixture) p2p.Config {
	t.Helper()
	var key secp256k1.PrivateKey
	sum := sha256.Sum256([]byte(tag))
	copy(key[:], sum[:])
	genesis := hash(t, "49d7ec7085e77bf5a403d0fcb4cfc42a4084a89dfff60477579c5e09c9e03c54")
	sched, err := p2p.NewSchedule(p2p.Chain{Genesis: genesis, Forks: p2p.TempoForks(hardfork.MainnetChainID)})
	if err != nil {
		t.Fatal(err)
	}
	return p2p.Config{
		Key: key, Entropy: &entropy{seed: sha256.Sum256([]byte("e" + tag))}, Client: "goempo/test", NetworkID: 4217,
		Genesis: genesis, Schedule: sched, Head: p2p.Head{Number: f.numbers[7], Timestamp: 1_794_900_000}, HeadHash: f.hashes[7],
	}
}

func TestFollowerOverSimulatedPeers(t *testing.T) {
	f := load(t)
	network := io.NewMemNetwork()
	listener, err := network.Listen("server:30303")
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := peerConfig(t, "server", f)
	serverCfg.Store = &store{f: f, raw: f.raws(t)}
	serverPub, err := serverCfg.Key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	var wg gosync.WaitGroup
	var serveErr error
	wg.Go(func() {
		conn, err := listener.Accept()
		if err != nil {
			serveErr = err
			return
		}
		server, err := p2p.Accept(conn, serverCfg)
		if err != nil {
			serveErr = err
			return
		}
		serveErr = server.Serve(4)
	})
	conn, err := network.Dial("server:30303")
	if err != nil {
		t.Fatal(err)
	}
	clientCfg := peerConfig(t, "client", f)
	clientCfg.Head = p2p.Head{Number: f.numbers[0] - 1, Timestamp: 1_794_900_000}
	peer, err := p2p.Connect(conn, serverPub, clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := sync.NewPeers([]sync.Peer{peer})
	if err != nil {
		t.Fatal(err)
	}
	exec := &recorder{head: f.parent(t)}
	follower := sync.NewFollower(peers, exec, 5)
	for range 2 {
		if _, err := follower.Step(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if serveErr != nil {
		t.Fatal(serveErr)
	}
	if exec.head.Hash != f.hashes[7] {
		t.Fatalf("head %d", exec.head.Number)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
