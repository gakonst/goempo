package cwp2p_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/gakonst/goempo/internal/cwp2p"
	gio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

type wallClock struct{ start time.Time }

func (c wallClock) MonotonicNanos() uint64 { return uint64(time.Since(c.start).Nanoseconds()) + 1 }

func (c wallClock) RealtimeNanos() uint64 { return uint64(time.Now().UnixNano()) }

type timerSleeper struct{}

func (timerSleeper) Sleep(ctx context.Context, nanos uint64) bool {
	t := time.NewTimer(time.Duration(nanos))
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

const (
	pollsMax      = 400
	pollMillis    = 10
	waitMillisMax = 10_000
)

const _ = uint(waitMillisMax - pollsMax*pollMillis)

func waitFor(cond func() bool, what string) {
	for range pollsMax {
		if cond() {
			return
		}
		time.Sleep(pollMillis * time.Millisecond)
	}
	assert.Fail("timed out waiting for %s", what)
}

func tempoChannels() []cwp2p.Channel {
	return []cwp2p.Channel{{ID: 0, Rate: 128}, {ID: 1, Rate: 128}, {ID: 2, Rate: 128}, {ID: 3, Rate: 8}, {ID: 4, Rate: 8}, {ID: 5, Rate: 128}}
}

type node struct {
	net  *cwp2p.Network
	peer cwp2p.Peer
}

func startNode(mem *gio.MemNetwork, seed byte) node {
	addr := fmt.Sprintf("node-%d", seed)
	l, err := mem.Listen(addr)
	assert.NoError(err, "listen")
	k := key(seed)
	n, err := cwp2p.New(cwp2p.Config{
		Key: k, Namespace: cwp2p.TempoNamespace(), Listener: l, Dialer: mem,
		Clock: wallClock{start: time.Now()}, Sleeper: timerSleeper{}, Entropy: rand.Reader,
		Channels: tempoChannels(), MaxMessageSize: 1 << 20, DialNanos: 20_000_000,
	})
	assert.NoError(err, "new network")
	return node{net: n, peer: cwp2p.Peer{Key: cwp2p.PublicKeyOf(k), Addr: addr}}
}

func peersOf(nodes ...node) []cwp2p.Peer {
	out := make([]cwp2p.Peer, len(nodes))
	for i, n := range nodes {
		out[i] = n.peer
	}
	return out
}

func connectedTo(n node, others ...node) func() bool {
	return func() bool {
		got := n.net.Connected()
		for _, o := range others {
			if !slices.Contains(got, o.peer.Key) {
				return false
			}
		}
		return len(got) == len(others)
	}
}

func receive(n node, ch uint64) cwp2p.Message {
	var got cwp2p.Message
	waitFor(func() bool {
		select {
		case got = <-n.net.Inbound(ch):
			return true
		default:
			return false
		}
	}, fmt.Sprintf("a message on channel %d", ch))
	return got
}

func closeAll(nodes ...node) {
	for _, n := range nodes {
		assert.NoError(n.net.Close(), "close")
	}
}

func TestNetworkExchangesOnEveryChannelBothWays(_ *testing.T) {
	mem := gio.NewMemNetwork()
	a, b, c := startNode(mem, 1), startNode(mem, 2), startNode(mem, 3)
	defer closeAll(a, b, c)
	set := peersOf(a, b, c)
	for _, n := range []node{a, b, c} {
		assert.Ok(n.net.Track(1, set), "track")
	}
	waitFor(connectedTo(a, b, c), "a connected")
	waitFor(connectedTo(b, a, c), "b connected")
	waitFor(connectedTo(c, a, b), "c connected")
	for _, ch := range tempoChannels() {
		msg := []byte(fmt.Sprintf("a->all %d", ch.ID))
		sent := a.net.Send(ch.ID, nil, msg, cwp2p.PriorityLow)
		assert.Equal(len(sent), 2, "a sends to both peers")
		for _, r := range []node{b, c} {
			got := receive(r, ch.ID)
			assert.Equal(got.From, a.peer.Key, "sender")
			assert.Equal(string(got.Data), string(msg), "payload")
		}
		reply := []byte(fmt.Sprintf("b->a %d", ch.ID))
		assert.Equal(len(b.net.Send(ch.ID, []cwp2p.PublicKey{a.peer.Key}, reply, cwp2p.PriorityHigh)), 1, "b sends to a")
		got := receive(a, ch.ID)
		assert.Equal(got.From, b.peer.Key, "reply sender")
		assert.Equal(string(got.Data), string(reply), "reply payload")
	}
	assert.Equal(len(a.net.Send(0, nil, make([]byte, 1<<20+1), cwp2p.PriorityLow)), 0, "oversized message is not sent")
	assert.Equal(len(a.net.Send(9, nil, []byte("x"), cwp2p.PriorityLow)), 0, "unregistered channel is not sent")
}

func TestPeerSetUpdateDropsRemovedPeer(_ *testing.T) {
	mem := gio.NewMemNetwork()
	a, b, c := startNode(mem, 1), startNode(mem, 2), startNode(mem, 3)
	defer closeAll(a, b, c)
	for _, n := range []node{a, b, c} {
		assert.Ok(n.net.Track(1, peersOf(a, b, c)), "track 1")
	}
	waitFor(connectedTo(a, b, c), "a connected to b and c")
	assert.Ok(!a.net.Track(1, peersOf(a, b)), "a stale set id is refused")
	assert.Ok(a.net.Track(2, peersOf(a, b)), "track 2")
	waitFor(connectedTo(a, b), "a keeps only b")
	waitFor(connectedTo(c, b), "c loses a")
	assert.Equal(len(a.net.Authorized()), 2, "authorized set follows the latest set")
}

func TestUnauthorizedPeerCannotConnect(_ *testing.T) {
	mem := gio.NewMemNetwork()
	a, d := startNode(mem, 1), startNode(mem, 4)
	defer closeAll(a, d)
	assert.Ok(a.net.Track(1, peersOf(a)), "a tracks only itself")
	assert.Ok(d.net.Track(1, peersOf(a, d)), "d tracks a")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(len(a.net.Connected()), 0, "a refuses d")
	assert.Equal(len(d.net.Connected()), 0, "d never completes a handshake")
}

func TestOutboundRateLimitAndBlock(_ *testing.T) {
	mem := gio.NewMemNetwork()
	a, b := startNode(mem, 1), startNode(mem, 2)
	defer closeAll(a, b)
	for _, n := range []node{a, b} {
		assert.Ok(n.net.Track(1, peersOf(a, b)), "track")
	}
	waitFor(connectedTo(a, b), "connected")
	sent := 0
	for range 20 {
		sent += len(a.net.Send(3, nil, []byte("slow"), cwp2p.PriorityLow))
	}
	assert.Equal(sent, 8, "channel 3 allows a burst of its per-second rate")
	for range 8 {
		receive(b, 3)
	}
	a.net.Block(b.peer.Key)
	waitFor(func() bool { return len(a.net.Connected()) == 0 }, "a drops the blocked peer")
	time.Sleep(100 * time.Millisecond)
	assert.Equal(len(a.net.Connected()), 0, "blocked peer stays out")
}

func TestBucketAllowsBurstThenRate(_ *testing.T) {
	b := cwp2p.NewBucket(4)
	for i := range 4 {
		assert.Ok(b.Take(1_000), fmt.Sprintf("burst token %d", i))
	}
	assert.Ok(!b.Take(1_000), "burst exhausted")
	assert.Equal(b.Wait(1_000), uint64(250_000_000), "next token after one interval")
	assert.Ok(b.Take(250_001_000), "token refills")
}
