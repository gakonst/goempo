package p2p_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/p2p"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

var mainnetGenesis = mustHash("49d7ec7085e77bf5a403d0fcb4cfc42a4084a89dfff60477579c5e09c9e03c54")

func mustHash(s string) primitives.Hash {
	var h primitives.Hash
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(h) {
		return h
	}
	copy(h[:], b)
	return h
}

func mainnetSchedule(t *testing.T) p2p.Schedule {
	t.Helper()
	s, err := p2p.NewSchedule(p2p.Chain{Genesis: mainnetGenesis, Forks: p2p.TempoForks(hardfork.MainnetChainID)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMainnetForkIDMatchesReference(t *testing.T) {
	s := mainnetSchedule(t)
	cases := []struct {
		name string
		time uint64
		hash uint32
		next uint64
	}{
		{"T11", 1_791_000_000, 0x820adcc4, 1_791_900_000},
		{"T12", 1_791_900_000, 0x118c7fce, 1_793_718_000},
		{"T13", 1_793_718_000, 0x292f0850, 1_796_742_000},
	}
	for _, c := range cases {
		id := s.ID(c.time)
		if binary.BigEndian.Uint32(id.Hash[:]) != c.hash || id.Next != c.next {
			t.Fatalf("%s: got %x next %d", c.name, id.Hash, id.Next)
		}
	}
}

func TestForkIDCompatibility(t *testing.T) {
	s := mainnetSchedule(t)
	head := p2p.Head{Number: 49_000_000, Timestamp: 1_794_000_000}
	t13 := s.ID(1_793_718_000)
	if !s.Compatible(head, t13) {
		t.Fatal("same fork refused")
	}
	if !s.Compatible(head, p2p.ForkID{Hash: t13.Hash}) {
		t.Fatal("a peer whose build lacks T14 must pass before T14")
	}
	t12 := s.ID(1_791_900_000)
	if !s.Compatible(head, t12) {
		t.Fatal("a peer still syncing at T12 is compatible")
	}
	if s.Compatible(p2p.Head{Number: 1, Timestamp: 1_796_742_000}, p2p.ForkID{Hash: t13.Hash}) {
		t.Fatal("a T13 peer without T14 must fail once T14 is active")
	}
	if s.Compatible(head, p2p.ForkID{Hash: [4]byte{0xde, 0xad, 0xbe, 0xef}}) {
		t.Fatal("unknown fork hash accepted")
	}
	if !s.Compatible(p2p.Head{Number: 1, Timestamp: 1_791_000_000}, t13) {
		t.Fatal("a peer ahead on a known fork is compatible")
	}
}

func TestParseEnode(t *testing.T) {
	url := "enode://a3cc3ec05f75b5d71d838a6b4a822044efef67b145ba14fcac3a7798de92806e59b12def3087c9453968e41a8d46439b35510a19014592afdd8962ab27060d67@40.160.24.210:30004?discport=30005"
	n, err := p2p.ParseEnode(url)
	if err != nil {
		t.Fatal(err)
	}
	if n.Address != "40.160.24.210:30004" || n.ID[0] != 0xa3 {
		t.Fatalf("parsed %+v", n)
	}
	for _, bad := range []string{"enode://zz@1.2.3.4:1", "http://x", "enode://" + strings.Repeat("00", 64) + "@1.2.3.4:5", url[:60]} {
		if _, err := p2p.ParseEnode(bad); !errors.Is(err, p2p.ErrEnode) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

type block struct {
	number uint64
	hash   primitives.Hash
	header []byte
	body   []byte
}

type store struct {
	blocks []block
}

func loadStore(t *testing.T) *store {
	t.Helper()
	raw, err := os.ReadFile("testdata/mainnet_49392000_8.txt")
	if err != nil {
		t.Fatal(err)
	}
	s := &store{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("bad line")
		}
		number, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		data, err := hex.DecodeString(f[2])
		if err != nil {
			t.Fatal(err)
		}
		outer := rlp.NewStream(data)
		list, err := outer.List()
		if err != nil {
			t.Fatal(err)
		}
		header, err := list.Raw()
		if err != nil {
			t.Fatal(err)
		}
		s.blocks = append(s.blocks, block{number: number, hash: mustHash(f[1]), header: header, body: rlp.EncodeList(list.Rest())})
	}
	return s
}

func (s *store) HeaderNumber(hash primitives.Hash) (uint64, bool) {
	for _, b := range s.blocks {
		if b.hash == hash {
			return b.number, true
		}
	}
	return 0, false
}

func (s *store) Header(number uint64) ([]byte, bool) {
	for _, b := range s.blocks {
		if b.number == number {
			return b.header, true
		}
	}
	return nil, false
}

func (s *store) Body(hash primitives.Hash) ([]byte, bool) {
	for _, b := range s.blocks {
		if b.hash == hash {
			return b.body, true
		}
	}
	return nil, false
}

type entropy struct {
	seed    [32]byte
	counter uint64
	buf     []byte
}

func newEntropy(tag string) *entropy { return &entropy{seed: sha256.Sum256([]byte(tag))} }

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

func config(t *testing.T, tag string, st p2p.Store) p2p.Config {
	t.Helper()
	key, err := rlpxKey(tag)
	if err != nil {
		t.Fatal(err)
	}
	return p2p.Config{
		Key:       key,
		Entropy:   newEntropy(tag),
		Client:    "goempo/test",
		NetworkID: 4217,
		Genesis:   mainnetGenesis,
		Schedule:  mainnetSchedule(t),
		Head:      p2p.Head{Number: 49_392_007, Timestamp: 1_794_900_000},
		Store:     st,
	}
}

func rlpxKey(tag string) (secp256k1.PrivateKey, error) {
	var k secp256k1.PrivateKey
	sum := sha256.Sum256([]byte("key " + tag))
	copy(k[:], sum[:])
	_, err := k.PublicKey()
	return k, err
}

type sides struct {
	client p2p.Config
	server p2p.Config
}

type session struct {
	client *p2p.Peer
	server *p2p.Peer
}

func connect(t *testing.T, cfg sides) session {
	client, server := cfg.client, cfg.server
	t.Helper()
	pair := io.Pipe()
	serverPub, err := server.Key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	var s session
	var serverErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		s.server, serverErr = p2p.Accept(pair.B, server)
	})
	s.client, err = p2p.Connect(pair.A, serverPub, client)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if serverErr != nil {
		t.Fatal(serverErr)
	}
	return s
}

func exchange(t *testing.T, server *p2p.Peer, request func() error) {
	t.Helper()
	var wg sync.WaitGroup
	var serveErr error
	wg.Go(func() {
		serveErr = server.Serve(1)
	})
	err := request()
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if serveErr != nil {
		t.Fatal(serveErr)
	}
}

func TestHandshakeAndStatus(t *testing.T) {
	st := loadStore(t)
	server := config(t, "server", st)
	server.HeadHash = st.blocks[7].hash
	s := connect(t, sides{client: config(t, "client", nil), server: server})
	if s.client.Version() != 69 || s.server.Version() != 69 {
		t.Fatalf("versions %d %d", s.client.Version(), s.server.Version())
	}
	got := s.client.Status()
	if got.Latest != 49_392_007 || got.LatestHash != st.blocks[7].hash || got.Genesis != mainnetGenesis {
		t.Fatalf("status %+v", got)
	}
	if s.client.Hello().Client != "goempo/test" {
		t.Fatalf("hello %+v", s.client.Hello())
	}
}

func TestDownloadHeadersAndBodies(t *testing.T) {
	st := loadStore(t)
	s := connect(t, sides{client: config(t, "client", nil), server: config(t, "server", st)})
	var headers [][]byte
	exchange(t, s.server, func() (err error) {
		headers, err = s.client.Headers(p2p.HeadersRequest{Number: 49_392_000, Amount: 8})
		return err
	})
	if len(headers) != 8 {
		t.Fatalf("%d headers", len(headers))
	}
	hashes := make([]primitives.Hash, len(headers))
	for i, h := range headers {
		if !bytes.Equal(h, st.blocks[i].header) {
			t.Fatalf("header %d differs", i)
		}
		hashes[i] = st.blocks[i].hash
	}
	var bodies [][]byte
	exchange(t, s.server, func() (err error) {
		bodies, err = s.client.Bodies(hashes)
		return err
	})
	if len(bodies) != len(hashes) {
		t.Fatalf("%d bodies", len(bodies))
	}
	for i, b := range bodies {
		if !bytes.Equal(b, st.blocks[i].body) {
			t.Fatalf("body %d differs", i)
		}
	}
	var back [][]byte
	exchange(t, s.server, func() (err error) {
		back, err = s.client.Headers(p2p.HeadersRequest{ByHash: true, Hash: st.blocks[7].hash, Amount: 3, Skip: 1, Reverse: true})
		return err
	})
	if len(back) != 3 || !bytes.Equal(back[0], st.blocks[7].header) || !bytes.Equal(back[2], st.blocks[3].header) {
		t.Fatalf("reverse skip: %d headers", len(back))
	}
}

func TestWrongGenesisIsRefused(t *testing.T) {
	server := config(t, "server", nil)
	server.Genesis = mustHash("ba49153e6f3996a340f3fe3d69e7c0826b717ae672cb115cf0527cada0b78e9e")
	pair := io.Pipe()
	serverPub, err := server.Key.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := p2p.Accept(pair.B, server); err == nil {
			t.Error("server accepted a client on another chain")
		}
	})
	_, err = p2p.Connect(pair.A, serverPub, config(t, "client", nil))
	wg.Wait()
	if !errors.Is(err, p2p.ErrWrongChain) {
		t.Fatalf("connect: %v", err)
	}
}
