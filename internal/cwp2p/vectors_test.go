package cwp2p_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/gakonst/goempo/internal/cwp2p"
	"github.com/kapetan-io/tiger/assert"
)

type sakeVector struct {
	Name                 string `json:"name"`
	Namespace            string `json:"namespace"`
	NowMs                uint64 `json:"now_ms"`
	DialerSeed           string `json:"dialer_seed"`
	ListenerSeed         string `json:"listener_seed"`
	DialerEntropyStart   byte   `json:"dialer_entropy_start"`
	ListenerEntropyStart byte   `json:"listener_entropy_start"`
	DialerPublic         string `json:"dialer_public"`
	ListenerPublic       string `json:"listener_public"`
	Syn                  string `json:"syn"`
	SynAck               string `json:"synack"`
	Ack                  string `json:"ack"`
	Summary              string `json:"summary"`
	SealHello            string `json:"d2l_seal_hello"`
	SealEmpty            string `json:"d2l_seal_empty"`
	SealBack             string `json:"l2d_seal_back"`
}

func unhex(s string) []byte {
	b, err := hex.DecodeString(s)
	assert.NoError(err, "vector hex")
	return b
}

func loadVectors() []sakeVector {
	raw, err := os.ReadFile("testdata/sake_cups_vectors.json")
	assert.NoError(err, "read vectors")
	var out []sakeVector
	assert.NoError(json.Unmarshal(raw, &out), "decode vectors")
	assert.Equal(len(out), 2, "v0 and v1 vectors")
	return out
}

type side struct {
	seed    string
	peer    string
	entropy byte
}

func vectorContext(v *sakeVector, s side) *cwp2p.HandshakeContext {
	version := cwp2p.SakeV0
	if v.Name == "v1" {
		version = cwp2p.SakeV1
	}
	return &cwp2p.HandshakeContext{
		Version: version, Namespace: unhex(v.Namespace), NowMs: v.NowMs,
		MaxAgeMs: 10_000, SynchronyMs: 5_000,
		Key: ed25519.NewKeyFromSeed(unhex(s.seed)), Peer: cwp2p.PublicKey(unhex(s.peer)),
		Entropy: &counterEntropy{next: s.entropy},
	}
}

func TestHandshakeMatchesCommonwareVectors(_ *testing.T) {
	for _, v := range loadVectors() {
		assert.Equal(hex.EncodeToString(cwp2p.TempoNamespace()), v.Namespace, "tempo namespace")
		dc := vectorContext(&v, side{v.DialerSeed, v.ListenerPublic, v.DialerEntropyStart})
		assert.Equal(hex.EncodeToString(func() []byte { p := cwp2p.PublicKeyOf(dc.Key); return p[:] }()), v.DialerPublic, "dialer public key")
		dial, err := cwp2p.DialStart(dc)
		assert.NoError(err, "dial start")
		assert.Equal(hex.EncodeToString(dial.Syn.Encode()), v.Syn, v.Name+" syn")
		lc := vectorContext(&v, side{v.ListenerSeed, v.DialerPublic, v.ListenerEntropyStart})
		listen, err := cwp2p.ListenStart(lc, dial.Syn)
		assert.NoError(err, "listen start")
		assert.Equal(hex.EncodeToString(listen.SynAck.Encode()), v.SynAck, v.Name+" synack")
		ack, err := dial.Finish(listen.SynAck)
		assert.NoError(err, "dial finish")
		assert.Equal(hex.EncodeToString(ack.Confirmation[:]), v.Ack, v.Name+" ack")
		t, err := listen.Finish(ack)
		assert.NoError(err, "listen finish")
		summary := t.Summarize()
		assert.Equal(hex.EncodeToString(summary[:]), v.Summary, v.Name+" summary")
		cups := cwp2p.CupsV0
		if v.Name == "v1" {
			cups = cwp2p.CupsV1
		}
		dialer := cwp2p.DeriveCiphers(dial.Transcript(), cups, cwp2p.RoleDialer)
		listener := cwp2p.DeriveCiphers(t, cups, cwp2p.RoleListener)
		sealed, err := dialer.Send.Seal([]byte("hello"))
		assert.NoError(err, "seal hello")
		assert.Equal(hex.EncodeToString(sealed), v.SealHello, v.Name+" first d2l record")
		sealed, err = dialer.Send.Seal(nil)
		assert.NoError(err, "seal empty")
		assert.Equal(hex.EncodeToString(sealed), v.SealEmpty, v.Name+" second d2l record")
		opened, err := listener.Recv.Open(unhex(v.SealHello))
		assert.NoError(err, "open hello")
		assert.Equal(string(opened), "hello", "listener opens the dialer record")
		sealed, err = listener.Send.Seal([]byte("back"))
		assert.NoError(err, "seal back")
		assert.Equal(hex.EncodeToString(sealed), v.SealBack, v.Name+" first l2d record")
	}
}
