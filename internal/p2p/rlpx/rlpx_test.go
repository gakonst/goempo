package rlpx_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/io"
	"github.com/gakonst/goempo/internal/p2p/rlpx"
	"golang.org/x/crypto/sha3"
)

func vectors(t *testing.T, name string) map[string][]byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("bad line %q", line)
		}
		if f[1] == "-" {
			out[f[0]] = []byte{}
			continue
		}
		b, err := hex.DecodeString(f[1])
		if err != nil {
			t.Fatal(err)
		}
		out[f[0]] = b
	}
	return out
}

func key(b []byte) secp256k1.PrivateKey {
	var k secp256k1.PrivateKey
	copy(k[:], b)
	return k
}

func nonce(b []byte) rlpx.Nonce {
	var n rlpx.Nonce
	copy(n[:], b)
	return n
}

func pub(b []byte) secp256k1.PublicKey {
	var p secp256k1.PublicKey
	copy(p[:], b)
	return p
}

type stream struct {
	seed    [32]byte
	counter uint64
	buf     []byte
}

func (s *stream) Read(p []byte) (int, error) {
	for range len(p)/32 + 2 {
		if len(s.buf) >= len(p) {
			break
		}
		var block [40]byte
		copy(block[:], s.seed[:])
		binary.BigEndian.PutUint64(block[32:], s.counter)
		s.counter++
		sum := sha256.Sum256(block[:])
		s.buf = append(s.buf, sum[:]...)
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func entropy(tag string) *stream {
	return &stream{seed: sha256.Sum256([]byte(tag))}
}

func TestReadAuthEIP8Vectors(t *testing.T) {
	v := vectors(t, "handshake.txt")
	for _, name := range []string{"auth_eip8_v4_message", "auth_eip8_v56_message"} {
		h := rlpx.NewRecipient(rlpx.Keys{Static: key(v["key_b"]), Ephemeral: key(v["eph_b"]), Nonce: nonce(v["nonce_b"])}, entropy("r"))
		if err := h.ReadAuth(v[name]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.RemoteEphemeral() != pub(v["eph_a_public"]) {
			t.Fatalf("%s: remote ephemeral %x", name, h.RemoteEphemeral())
		}
		if h.RemoteStatic() != pub(v["key_a_public"]) {
			t.Fatalf("%s: remote static", name)
		}
		if h.RemoteNonce() != nonce(v["nonce_a"]) {
			t.Fatalf("%s: remote nonce", name)
		}
	}
}

func TestReadAckEIP8Vectors(t *testing.T) {
	v := vectors(t, "handshake.txt")
	for _, name := range []string{"ack_eip8_v4_message", "ack_eip8_v57_message"} {
		keys := rlpx.Keys{Static: key(v["key_a"]), Ephemeral: key(v["eph_a"]), Nonce: nonce(v["nonce_a"])}
		h := rlpx.NewInitiator(keys, pub(v["key_b_public"]), entropy("i"))
		if err := h.ReadAck(v[name]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.RemoteEphemeral() != pub(v["eph_b_public"]) || h.RemoteNonce() != nonce(v["nonce_b"]) {
			t.Fatalf("%s: wrong remote values", name)
		}
	}
}

func TestSecretsMatchEIP8Vectors(t *testing.T) {
	v := vectors(t, "handshake.txt")
	h := rlpx.NewRecipient(rlpx.Keys{Static: key(v["key_b"]), Ephemeral: key(v["eph_b"]), Nonce: nonce(v["nonce_b"])}, entropy("r"))
	if err := h.ReadAuth(v["auth_eip8_v4_message"]); err != nil {
		t.Fatal(err)
	}
	h.SetTranscript(rlpx.Transcript{Auth: v["auth_eip8_v4_message"], Ack: v["ack_eip8_v4_message"]})
	s, err := h.Secrets()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.AES[:], v["want_aes"]) || !bytes.Equal(s.MAC[:], v["want_mac"]) {
		t.Fatalf("aes %x mac %x", s.AES, s.MAC)
	}
	s.Ingress.Write([]byte("foo"))
	if got := s.Ingress.Sum(nil); !bytes.Equal(got, v["want_ingress_foo"]) {
		t.Fatalf("ingress mac %x", got)
	}
}

func frameCodec(t *testing.T, v map[string][]byte) *rlpx.Codec {
	t.Helper()
	egress := sha3.NewLegacyKeccak256()
	egress.Write(v["egress_seed"])
	ingress := sha3.NewLegacyKeccak256()
	ingress.Write(v["egress_seed"])
	var s rlpx.Secrets
	copy(s.AES[:], v["aes_secret"])
	copy(s.MAC[:], v["mac_secret"])
	s.Egress, s.Ingress = egress, ingress
	c, err := rlpx.NewCodec(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFramesMatchGoldens(t *testing.T) {
	v := vectors(t, "frames.txt")
	enc := frameCodec(t, v)
	dec := frameCodec(t, v)
	for i := range 4 {
		payload := v["payload_"+string(rune('0'+i))]
		want := v["frame_"+string(rune('0'+i))]
		got, err := enc.Seal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d: %x want %x", i, got, want)
		}
		size, err := dec.OpenHeader(want[:rlpx.HeaderLength])
		if err != nil || size != len(payload) {
			t.Fatalf("frame %d header: %d %v", i, size, err)
		}
		plain, err := dec.OpenBody(size, want[rlpx.HeaderLength:])
		if err != nil || !bytes.Equal(plain, payload) {
			t.Fatalf("frame %d body: %v", i, err)
		}
	}
}

func TestTamperedFrameFailsMAC(t *testing.T) {
	v := vectors(t, "frames.txt")
	dec := frameCodec(t, v)
	frame := append([]byte(nil), v["frame_0"]...)
	frame[rlpx.HeaderLength] ^= 1
	size, err := dec.OpenHeader(frame[:rlpx.HeaderLength])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.OpenBody(size, frame[rlpx.HeaderLength:]); !errors.Is(err, rlpx.ErrMAC) {
		t.Fatalf("tampered body: %v", err)
	}
	bad := frameCodec(t, v)
	header := append([]byte(nil), v["frame_0"][:rlpx.HeaderLength]...)
	header[0] ^= 1
	if _, err := bad.OpenHeader(header); !errors.Is(err, rlpx.ErrMAC) {
		t.Fatalf("tampered header: %v", err)
	}
}

type dialResult struct {
	conn *rlpx.Conn
	err  error
}

func TestDialAcceptExchangeMessages(t *testing.T) {
	serverKey := secp256k1.PrivateKey{31: 0x55}
	clientKey := secp256k1.PrivateKey{31: 0x66}
	serverPub, err := serverKey.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	clientPub, err := clientKey.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	pair := io.Pipe()
	var res dialResult
	var wg sync.WaitGroup
	serverKeys, err := rlpx.GenerateKeys(entropy("server"), serverKey)
	if err != nil {
		t.Fatal(err)
	}
	recipient := rlpx.NewRecipient(serverKeys, entropy("server pad"))
	wg.Go(func() {
		res.conn, res.err = rlpx.Accept(pair.B, recipient)
	})
	clientKeys, err := rlpx.GenerateKeys(entropy("client"), clientKey)
	if err != nil {
		t.Fatal(err)
	}
	client, err := rlpx.Dial(pair.A, rlpx.NewInitiator(clientKeys, serverPub, entropy("client pad")))
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if res.err != nil {
		t.Fatal(res.err)
	}
	server := res.conn
	if recipient.RemoteStatic() != clientPub {
		t.Fatal("server saw the wrong client key")
	}
	if err := client.WriteMsg(rlpx.Msg{Code: 0, Payload: []byte{0xc0}}); err != nil {
		t.Fatal(err)
	}
	m, err := server.ReadMsg()
	if err != nil || m.Code != 0 || !bytes.Equal(m.Payload, []byte{0xc0}) {
		t.Fatalf("plain message %+v %v", m, err)
	}
	client.EnableSnappy()
	server.EnableSnappy()
	big := bytes.Repeat([]byte("tempo"), 50000)
	if err := server.WriteMsg(rlpx.Msg{Code: 0x13, Payload: big}); err != nil {
		t.Fatal(err)
	}
	m, err = client.ReadMsg()
	if err != nil || m.Code != 0x13 || !bytes.Equal(m.Payload, big) {
		t.Fatalf("snappy message code %d len %d %v", m.Code, len(m.Payload), err)
	}
}

func TestAcceptRejectsAuthForAnotherKey(t *testing.T) {
	serverKey := secp256k1.PrivateKey{31: 0x55}
	otherPub, err := (secp256k1.PrivateKey{31: 0x77}).PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	clientKeys, err := rlpx.GenerateKeys(entropy("client"), secp256k1.PrivateKey{31: 0x66})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := rlpx.NewInitiator(clientKeys, otherPub, entropy("pad")).WriteAuth()
	if err != nil {
		t.Fatal(err)
	}
	serverKeys, err := rlpx.GenerateKeys(entropy("server"), serverKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := rlpx.NewRecipient(serverKeys, entropy("s")).ReadAuth(auth); !errors.Is(err, rlpx.ErrECIES) {
		t.Fatalf("wrong key: %v", err)
	}
}
