//go:build interop

package cwp2p_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gakonst/goempo/internal/cwp2p"
	gio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

const (
	tempoPeersMax   = 128
	captureEachMax  = 3
	captureBytesMax = 1 << 16
)

const _ = uint(captureBytesMax - tempoPeersMax*captureEachMax)

type captured struct {
	Channel uint64 `json:"channel"`
	From    string `json:"from"`
	Data    string `json:"data"`
}

func tempoKey(path string) ed25519.PrivateKey {
	raw, err := os.ReadFile(path)
	assert.NoError(err, "read signing key")
	seed, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(string(raw)), "0x"))
	assert.NoError(err, "signing key hex")
	assert.Equal(len(seed), ed25519.SeedSize, "signing key is an ed25519 seed")
	return ed25519.NewKeyFromSeed(seed)
}

func TestPeerWithTempoValidators(_ *testing.T) {
	listen, peerSpecs := os.Getenv("CWP2P_TEMPO_LISTEN"), os.Getenv("CWP2P_TEMPO_PEERS")
	assert.Ok(listen != "" && peerSpecs != "", "set CWP2P_TEMPO_LISTEN, CWP2P_TEMPO_PEERS and CWP2P_TEMPO_KEY")
	secs, err := strconv.Atoi(os.Getenv("CWP2P_TEMPO_SECS"))
	assert.NoError(err, "CWP2P_TEMPO_SECS")
	k := tempoKey(os.Getenv("CWP2P_TEMPO_KEY"))
	self := cwp2p.Peer{Key: cwp2p.PublicKeyOf(k), Addr: listen}
	set := []cwp2p.Peer{self}
	for _, spec := range strings.Split(peerSpecs, ",")[:min(tempoPeersMax, strings.Count(peerSpecs, ",")+1)] {
		set = append(set, interopPeer(spec))
	}
	l, err := gio.ListenTCP(listen)
	assert.NoError(err, "listen")
	n, err := cwp2p.New(cwp2p.Config{
		Key: k, Namespace: cwp2p.TempoNamespace(), Listener: l, Dialer: gio.TCPDialer{TimeoutNanos: 2_000_000_000},
		Clock: wallClock{start: time.Now()}, Sleeper: timerSleeper{}, Entropy: rand.Reader,
		Channels: tempoChannels(), MaxMessageSize: 8_388_780,
	})
	assert.NoError(err, "new network")
	defer func() { assert.NoError(n.Close(), "close") }()
	assert.Ok(n.Track(0, set), "track")
	fmt.Printf("SELF %x tracking %d peers\n", self.Key[:], len(set)-1)
	counts := make(map[string]int)
	bytes := make(map[uint64]int)
	var capture []captured
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for range secs * 100 {
		if time.Now().After(deadline) {
			break
		}
		for _, ch := range tempoChannels() {
			capture = drainTempo(n, ch.ID, tempoTally{counts: counts, bytes: bytes}, capture)
		}
		time.Sleep(10 * time.Millisecond)
	}
	connected := n.Connected()
	fmt.Printf("CONNECTED %d/%d\n", len(connected), len(set)-1)
	for _, key := range connected {
		fmt.Printf("  %x\n", key[:])
	}
	fmt.Printf("BYTES per channel %v\n", bytes)
	for k, v := range counts {
		fmt.Printf("RECV %s %d\n", k, v)
	}
	if path := os.Getenv("CWP2P_TEMPO_CAPTURE"); path != "" {
		out, err := json.MarshalIndent(capture, "", " ")
		assert.NoError(err, "encode capture")
		assert.NoError(os.WriteFile(path, out, 0o644), "write capture")
	}
	assert.Equal(len(connected), len(set)-1, "connected to every tempo validator")
	for _, p := range set[1:] {
		assert.Ok(counts[fmt.Sprintf("ch=0 from=%x", p.Key[:4])] > 0, fmt.Sprintf("votes from %x", p.Key[:4]))
	}
}

type tempoTally struct {
	counts map[string]int
	bytes  map[uint64]int
}

func drainTempo(n *cwp2p.Network, ch uint64, t tempoTally, capture []captured) []captured {
	for range cwp2p.DefaultBacklog {
		select {
		case m := <-n.Inbound(ch):
			key := fmt.Sprintf("ch=%d from=%x", ch, m.From[:4])
			if t.counts[key] < captureEachMax && len(m.Data) <= captureBytesMax {
				capture = append(capture, captured{Channel: ch, From: hex.EncodeToString(m.From[:]), Data: hex.EncodeToString(m.Data)})
			}
			t.counts[key]++
			t.bytes[ch] += len(m.Data)
		default:
			return capture
		}
	}
	return capture
}
