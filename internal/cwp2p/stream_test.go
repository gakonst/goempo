package cwp2p_test

import (
	"bytes"
	"crypto/ed25519"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/cwp2p"
	gio "github.com/gakonst/goempo/internal/io"
	"github.com/kapetan-io/tiger/assert"
)

type counterEntropy struct{ next byte }

func (c *counterEntropy) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = c.next
		c.next++
	}
	return len(p), nil
}

func key(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
}

func streamConfig(k ed25519.PrivateKey, sake cwp2p.SakeVersion, cups cwp2p.CupsVersion, seed byte) *cwp2p.StreamConfig {
	return &cwp2p.StreamConfig{
		Sake: sake, Cups: cups, Namespace: cwp2p.TempoNamespace(), Key: k,
		Clock:    &gio.FixedClock{Realtime: 1_700_000_000_000_000_000},
		MaxAgeMs: 10_000, SynchronyMs: 5_000,
		Entropy: &counterEntropy{next: seed}, MaxMessageSize: 1 << 20,
	}
}

type handshake struct {
	dialed   *cwp2p.Stream
	accepted *cwp2p.Stream
}

func connect(sake cwp2p.SakeVersion, cups cwp2p.CupsVersion) handshake {
	pair := gio.Pipe()
	dk, lk := key(1), key(2)
	var wg sync.WaitGroup
	var h handshake
	var dialErr error
	wg.Go(func() {
		h.dialed, dialErr = cwp2p.DialStream(pair.A, streamConfig(dk, sake, cups, 10), cwp2p.PublicKeyOf(lk))
	})
	accepted, err := cwp2p.AcceptStream(pair.B, streamConfig(lk, sake, cups, 100), func(cwp2p.PublicKey) bool { return true })
	wg.Wait()
	assert.NoError(err, "accept")
	assert.NoError(dialErr, "dial")
	assert.Equal(accepted.Peer, cwp2p.PublicKeyOf(dk), "listener learns the dialer key")
	h.accepted = accepted
	return h
}

func roundTrip(sake cwp2p.SakeVersion, cups cwp2p.CupsVersion) {
	h := connect(sake, cups)
	var wg sync.WaitGroup
	var sendErr error
	wg.Go(func() {
		sendErr = h.dialed.Sender.SendMany([][]byte{[]byte("hello"), {}, bytes.Repeat([]byte{7}, 70000)})
	})
	got, err := h.accepted.Receiver.Recv()
	assert.NoError(err, "recv hello")
	assert.Equal(string(got), "hello", "first record")
	got, err = h.accepted.Receiver.Recv()
	assert.NoError(err, "recv empty")
	assert.Equal(len(got), 0, "empty record")
	got, err = h.accepted.Receiver.Recv()
	assert.NoError(err, "recv large")
	assert.Equal(len(got), 70000, "large record")
	wg.Wait()
	assert.NoError(sendErr, "send")
	wg.Go(func() { sendErr = h.accepted.Sender.Send([]byte("back")) })
	got, err = h.dialed.Receiver.Recv()
	wg.Wait()
	assert.NoError(sendErr, "send back")
	assert.NoError(err, "recv back")
	assert.Equal(string(got), "back", "reply")
}

func TestStreamRoundTripV0(_ *testing.T) { roundTrip(cwp2p.SakeV0, cwp2p.CupsV0) }

func TestStreamRoundTripV1(_ *testing.T) { roundTrip(cwp2p.SakeV1, cwp2p.CupsV1) }

func TestBouncerRejectsUnknownPeer(_ *testing.T) {
	pair := gio.Pipe()
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := cwp2p.DialStream(pair.A, streamConfig(key(1), cwp2p.SakeV0, cwp2p.CupsV0, 1), cwp2p.PublicKeyOf(key(2)))
		assert.Ok(err != nil, "dial fails once the listener hangs up")
	})
	_, err := cwp2p.AcceptStream(pair.B, streamConfig(key(2), cwp2p.SakeV0, cwp2p.CupsV0, 2), func(cwp2p.PublicKey) bool { return false })
	assert.Equal(err, cwp2p.ErrPeerRejected, "bouncer")
	assert.NoError(pair.B.Close(), "close listener side")
	wg.Wait()
}

func TestWrongPeerKeyFailsSignature(_ *testing.T) {
	pair := gio.Pipe()
	var wg sync.WaitGroup
	var dialErr error
	wg.Go(func() {
		_, dialErr = cwp2p.DialStream(pair.A, streamConfig(key(1), cwp2p.SakeV0, cwp2p.CupsV0, 1), cwp2p.PublicKeyOf(key(3)))
	})
	_, err := cwp2p.AcceptStream(pair.B, streamConfig(key(2), cwp2p.SakeV0, cwp2p.CupsV0, 2), func(cwp2p.PublicKey) bool { return true })
	assert.Equal(err, cwp2p.ErrSignature, "listener sees a syn signed for another listener")
	assert.NoError(pair.B.Close(), "close listener side")
	wg.Wait()
	assert.Ok(dialErr != nil, "dial fails")
}
