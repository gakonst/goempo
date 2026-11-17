//go:build interop

package cwp2p_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
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
	interopRounds    = 5
	interopLargeSize = 200_000
	interopPollsMax  = 6_000
)

const _ = uint(interopPollsMax - interopRounds)

func interopPeer(spec string) cwp2p.Peer {
	k, addr, ok := strings.Cut(spec, "@")
	assert.Ok(ok, "CWP2P_INTEROP_PEER is hexkey@host:port")
	raw, err := hex.DecodeString(k)
	assert.NoError(err, "peer key hex")
	assert.Equal(len(raw), cwp2p.PublicKeySize, "peer key size")
	return cwp2p.Peer{Key: cwp2p.PublicKey(raw), Addr: addr}
}

func interopPayload(ch uint64, round int) []byte {
	out := []byte(fmt.Sprintf("go ch=%d n=%d ", ch, round))
	if ch == 4 && round == 0 {
		out = append(out, bytes.Repeat([]byte{0xcd}, interopLargeSize-len(out))...)
	}
	return out
}

func TestInteropWithRustCommonware(_ *testing.T) {
	listen, peerSpec := os.Getenv("CWP2P_INTEROP_LISTEN"), os.Getenv("CWP2P_INTEROP_PEER")
	assert.Ok(listen != "" && peerSpec != "", "set CWP2P_INTEROP_LISTEN and CWP2P_INTEROP_PEER to a Rust commonware node")
	seed, err := strconv.Atoi(os.Getenv("CWP2P_INTEROP_SEED"))
	assert.NoError(err, "CWP2P_INTEROP_SEED")
	rust := interopPeer(peerSpec)
	if os.Getenv("CWP2P_INTEROP_LISTEN_ONLY") != "" {
		rust.Addr = ""
	}
	l, err := gio.ListenTCP(listen)
	assert.NoError(err, "listen")
	k := key(byte(seed))
	self := cwp2p.Peer{Key: cwp2p.PublicKeyOf(k), Addr: listen}
	fmt.Printf("GO PUBKEY %x\n", self.Key[:])
	n, err := cwp2p.New(cwp2p.Config{
		Key: k, Namespace: cwp2p.TempoNamespace(), Listener: l, Dialer: gio.TCPDialer{TimeoutNanos: 2_000_000_000},
		Clock: wallClock{start: time.Now()}, Sleeper: timerSleeper{}, Entropy: rand.Reader,
		Channels: tempoChannels(), MaxMessageSize: 8_388_780,
	})
	assert.NoError(err, "new network")
	defer func() { assert.NoError(n.Close(), "close") }()
	assert.Ok(n.Track(0, []cwp2p.Peer{self, rust}), "track")
	waitFor(func() bool { return len(n.Connected()) == 1 }, "connection to the rust node")
	fmt.Printf("GO CONNECTED %x\n", n.Connected()[0][:])
	got := make(map[uint64]int)
	sent := make(map[uint64]int)
	for i := range interopPollsMax {
		if i%50 == 0 && i/50 < interopRounds {
			for _, ch := range tempoChannels() {
				if len(n.Send(ch.ID, nil, interopPayload(ch.ID, i/50), cwp2p.PriorityLow)) == 1 {
					sent[ch.ID]++
				}
			}
		}
		for _, ch := range tempoChannels() {
			drainInterop(n, ch.ID, rust.Key, got)
		}
		if interopDone(tally{got: got, sent: sent}) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Printf("GO SENT %v GOT %v\n", sent, got)
	for _, ch := range tempoChannels() {
		assert.Equal(sent[ch.ID], interopRounds, fmt.Sprintf("sent every round on channel %d", ch.ID))
		assert.Equal(got[ch.ID], interopRounds, fmt.Sprintf("received every rust round on channel %d", ch.ID))
	}
	time.Sleep(2 * time.Second)
}

func drainInterop(n *cwp2p.Network, ch uint64, from cwp2p.PublicKey, got map[uint64]int) {
	for range interopRounds + 1 {
		select {
		case m := <-n.Inbound(ch):
			assert.Equal(m.From, from, "sender is the rust node")
			want := fmt.Sprintf("rust ch=%d n=%d ", ch, got[ch])
			assert.Ok(strings.HasPrefix(string(m.Data), want), "rust payload "+want)
			if ch == 4 && got[ch] == 0 {
				assert.Equal(len(m.Data), interopLargeSize, "large marshal-channel message")
			}
			fmt.Printf("GO RECV ch=%d len=%d %q\n", ch, len(m.Data), m.Data[:min(len(m.Data), 24)])
			got[ch]++
		default:
			return
		}
	}
}

type tally struct {
	got  map[uint64]int
	sent map[uint64]int
}

func interopDone(t tally) bool {
	for _, ch := range tempoChannels() {
		if t.got[ch.ID] < interopRounds || t.sent[ch.ID] < interopRounds {
			return false
		}
	}
	return true
}
