package secp256k1_test

import (
	"encoding/hex"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
)

type signedMessage struct {
	hash   primitives.Hash
	sig    secp256k1.Signature
	signer primitives.Address
	label  string
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := primitives.DecodeHex(s)
	if err != nil {
		t.Fatalf("hex %q: %v", s, err)
	}
	return b
}

func scalar32(t *testing.T, b []byte) [32]byte {
	t.Helper()
	if len(b) > 32 {
		t.Fatalf("scalar of %d bytes", len(b))
	}
	var out [32]byte
	copy(out[32-len(b):], b)
	return out
}

func listItems(t *testing.T, payload []byte) [][]byte {
	t.Helper()
	s := rlp.NewStream(payload)
	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var items [][]byte
	for i := 0; i < 64; i++ {
		if list.Empty() {
			return items
		}
		raw, err := list.Raw()
		if err != nil {
			t.Fatalf("item: %v", err)
		}
		items = append(items, raw)
	}
	t.Fatal("too many items")
	return nil
}

func itemBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	b, err := rlp.DecodeBytes(raw)
	if err != nil {
		t.Fatalf("decode bytes: %v", err)
	}
	return b
}

func encodeList(items [][]byte) []byte {
	e := rlp.NewEncoder(256)
	e.ListStart()
	for _, it := range items {
		e.WriteRaw(it)
	}
	e.ListEnd()
	return e.Encoded()
}

type hashSig struct {
	hash primitives.Hash
	sig  secp256k1.Signature
}

func decodeSignedTx(t *testing.T, raw []byte) hashSig {
	t.Helper()
	if raw[0] >= 0xc0 {
		return decodeLegacy(t, raw)
	}
	items := listItems(t, raw[1:])
	n := len(items)
	parity := new(big.Int).SetBytes(itemBytes(t, items[n-3]))
	sig := secp256k1.Signature{
		R:          scalar32(t, itemBytes(t, items[n-2])),
		S:          scalar32(t, itemBytes(t, items[n-1])),
		RecoveryID: byte(parity.Uint64()),
	}
	payload := append([]byte{raw[0]}, encodeList(items[:n-3])...)
	return hashSig{hash: keccak.Sum256(payload), sig: sig}
}

func decodeLegacy(t *testing.T, raw []byte) hashSig {
	t.Helper()
	items := listItems(t, raw)
	if len(items) != 9 {
		t.Fatalf("legacy tx with %d items", len(items))
	}
	v := new(big.Int).SetBytes(itemBytes(t, items[6])).Uint64()
	sig := secp256k1.Signature{R: scalar32(t, itemBytes(t, items[7])), S: scalar32(t, itemBytes(t, items[8]))}
	unsigned := items[:6]
	if v == 27 || v == 28 {
		sig.RecoveryID = byte(v - 27)
		return hashSig{hash: keccak.Sum256(encodeList(unsigned)), sig: sig}
	}
	chainID := (v - 35) / 2
	sig.RecoveryID = byte((v - 35) % 2)
	withChain := append(append([][]byte{}, unsigned...), rlp.EncodeUint64(chainID), rlp.EncodeUint64(0), rlp.EncodeUint64(0))
	return hashSig{hash: keccak.Sum256(encodeList(withChain)), sig: sig}
}

func authorizationMessage(t *testing.T, fields []string) signedMessage {
	t.Helper()
	chain := mustDecodeHex(t, fields[1])
	addr := mustDecodeHex(t, fields[2])
	nonce := mustDecodeHex(t, fields[3])
	parity := new(big.Int).SetBytes(mustDecodeHex(t, fields[4]))
	body := encodeList([][]byte{
		rlp.EncodeBytes(primitives.TrimLeftZeros(chain)),
		rlp.EncodeBytes(addr),
		rlp.EncodeBytes(primitives.TrimLeftZeros(nonce)),
	})
	return signedMessage{
		hash: keccak.Sum256(append([]byte{0x05}, body...)),
		sig: secp256k1.Signature{
			R:          scalar32(t, mustDecodeHex(t, fields[5])),
			S:          scalar32(t, mustDecodeHex(t, fields[6])),
			RecoveryID: byte(parity.Uint64()),
		},
		signer: primitives.BytesToAddress(mustDecodeHex(t, fields[7])),
		label:  "auth " + fields[5],
	}
}

func loadLines(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Fields(line))
	}
	return out
}

type txLine struct {
	raw    string
	sender string
}

func txMessage(t *testing.T, line txLine) signedMessage {
	t.Helper()
	hs := decodeSignedTx(t, mustDecodeHex(t, line.raw))
	return signedMessage{hash: hs.hash, sig: hs.sig, signer: primitives.BytesToAddress(mustDecodeHex(t, line.sender)), label: line.raw[:min(len(line.raw), 24)]}
}

func checkRecovers(t *testing.T, m signedMessage) {
	t.Helper()
	got, err := secp256k1.RecoverAddress(m.hash, m.sig)
	if err != nil {
		t.Fatalf("%s: %v", m.label, err)
	}
	if got != m.signer {
		t.Fatalf("%s: recovered %s, want %s", m.label, got.Hex(), m.signer.Hex())
	}
}

func TestTempoMainnetSenders(t *testing.T) {
	lines := loadLines(t, "testdata/tempo_mainnet_senders.txt")
	if len(lines) < 20 {
		t.Fatalf("only %d fixtures", len(lines))
	}
	for _, f := range lines {
		checkRecovers(t, txMessage(t, txLine{raw: f[0], sender: f[1]}))
	}
}

func TestEESTSenders(t *testing.T) {
	lines := loadLines(t, "testdata/eest_senders.txt")
	counts := map[string]int{}
	for _, f := range lines {
		switch f[0] {
		case "tx":
			checkRecovers(t, txMessage(t, txLine{raw: f[1], sender: f[2]}))
		case "auth":
			checkRecovers(t, authorizationMessage(t, f))
		default:
			t.Fatalf("unknown line kind %q", f[0])
		}
		counts[f[0]]++
	}
	if counts["tx"] < 100 || counts["auth"] < 20 {
		t.Fatalf("fixture counts %v", counts)
	}
}

func TestEIP155Example(t *testing.T) {
	hash := primitives.Hash(scalar32(t, mustDecodeHex(t, "0xdaf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53")))
	r, _ := new(big.Int).SetString("18515461264373351373200002665853028612451056578545711640558177340181847433846", 10)
	s, _ := new(big.Int).SetString("46948507304638947509940763649030358759909902576025900602547168820602576006531", 10)
	sig := secp256k1.Signature{RecoveryID: 0}
	r.FillBytes(sig.R[:])
	s.FillBytes(sig.S[:])
	want := primitives.BytesToAddress(mustDecodeHex(t, "0x9d8a62f656a8d1615c1294fd71e9cfb3e4855a4f"))
	checkRecovers(t, signedMessage{hash: hash, sig: sig, signer: want, label: "eip155"})
	var key secp256k1.PrivateKey
	for i := range key {
		key[i] = 0x46
	}
	addr, err := key.Address()
	if err != nil || addr != want {
		t.Fatalf("private key address %s %v", addr.Hex(), err)
	}
	signed, err := secp256k1.Sign(hash, key)
	if err != nil {
		t.Fatal(err)
	}
	if signed != sig {
		t.Fatalf("deterministic signature r=%x s=%x id=%d differs from the EIP-155 example", signed.R, signed.S, signed.RecoveryID)
	}
}

func TestKnownKeys(t *testing.T) {
	cases := []struct {
		name string
		key  string
		addr string
	}{
		{name: "eest", key: "0x45a915e4d060149eb4365960e6a7a45f334393093061116b197e3240065ff2d8", addr: "0xa94f5374fce5edbc8e2a8697c15331677e6ebf0b"},
		{name: "one", key: "0x0000000000000000000000000000000000000000000000000000000000000001", addr: "0x7e5f4552091a69125d5dfcb7b8c2659029395bdf"},
	}
	for _, c := range cases {
		key := secp256k1.PrivateKey(scalar32(t, mustDecodeHex(t, c.key)))
		addr, err := key.Address()
		if err != nil {
			t.Fatal(err)
		}
		if addr.Hex() != c.addr {
			t.Fatalf("address of %s = %s, want %s", c.key, addr.Hex(), c.addr)
		}
	}
	var zero secp256k1.PrivateKey
	if _, err := zero.PublicKey(); err == nil {
		t.Fatal("zero key accepted")
	}
	order := secp256k1.Order().Bytes32()
	if _, err := secp256k1.PrivateKey(order).PublicKey(); err == nil {
		t.Fatal("key n accepted")
	}
}

func TestEcrecoverPrecompile(t *testing.T) {
	unrecoverable := mustDecodeHex(t, "0xa8b53bdf3306a35a7103ab5504a0c9b492295564b6202b1942a84ef300107281000000000000000000000000000000000000000000000000000000000000001b307835653165303366353363653138623737326363623030393366663731663366353366356337356237346463623331613835616138623838393262346538621122334455667788991011121314151617181920212223242526272829303132")
	if _, ok := secp256k1.Ecrecover(unrecoverable); ok {
		t.Fatal("unrecoverable key accepted")
	}
	input := mustDecodeHex(t, "0x38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02")
	out, ok := secp256k1.Ecrecover(input)
	want := "000000000000000000000000ceaccac640adf55b2028469bd36ba501f28b699d"
	if !ok || hex.EncodeToString(out[:]) != want {
		t.Fatalf("ecrecover = %x %v", out, ok)
	}
	bad := func(name string, mutate func(b []byte)) {
		b := append([]byte{}, input...)
		mutate(b)
		if _, ok := secp256k1.Ecrecover(b); ok {
			t.Fatalf("%s accepted", name)
		}
	}
	bad("v=29", func(b []byte) { b[63] = 29 })
	bad("v=0", func(b []byte) { b[63] = 0 })
	bad("v high byte", func(b []byte) { b[32] = 1 })
	bad("r=0", func(b []byte) { clear(b[64:96]) })
	bad("s=0", func(b []byte) { clear(b[96:128]) })
	bad("s=n", func(b []byte) { n := secp256k1.Order().Bytes32(); copy(b[96:128], n[:]) })
	if _, ok := secp256k1.Ecrecover(nil); ok {
		t.Fatal("empty input accepted")
	}
	if got, ok := secp256k1.Ecrecover(input[:100]); !ok || got == out {
		t.Fatal("truncated input must recover with s right-padded by zeros")
	}
	long := append(append([]byte{}, input...), 0xde, 0xad)
	if got, ok := secp256k1.Ecrecover(long); !ok || got != out {
		t.Fatal("trailing bytes changed the result")
	}
}

func TestHighSAccepted(t *testing.T) {
	var key secp256k1.PrivateKey
	key[31] = 7
	addr, err := key.Address()
	if err != nil {
		t.Fatal(err)
	}
	hash := keccak.Sum256([]byte("high s"))
	sig, err := secp256k1.Sign(hash, key)
	if err != nil {
		t.Fatal(err)
	}
	if !secp256k1.IsLowS(sig.S) {
		t.Fatal("Sign produced high s")
	}
	s := new(big.Int).SetBytes(sig.S[:])
	n := secp256k1.Order().Bytes32()
	s.Sub(new(big.Int).SetBytes(n[:]), s)
	high := sig
	s.FillBytes(high.S[:])
	high.RecoveryID ^= 1
	if secp256k1.IsLowS(high.S) {
		t.Fatal("twin is low s")
	}
	checkRecovers(t, signedMessage{hash: hash, sig: high, signer: addr, label: "high s"})
	var in [128]byte
	copy(in[:32], hash[:])
	in[63] = 27 + high.RecoveryID
	copy(in[64:96], high.R[:])
	copy(in[96:], high.S[:])
	out, ok := secp256k1.Ecrecover(in[:])
	if !ok || primitives.BytesToAddress(out[12:]) != addr {
		t.Fatal("ecrecover rejected high s")
	}
}

func TestRecoveryErrors(t *testing.T) {
	hash := keccak.Sum256([]byte("x"))
	sig := secp256k1.Signature{RecoveryID: 2}
	sig.R[31], sig.S[31] = 1, 1
	if _, err := secp256k1.RecoverPublicKey(hash, sig); err != secp256k1.ErrInvalidRecoveryID {
		t.Fatalf("recovery id 2: %v", err)
	}
	sig.RecoveryID = 0
	sig.R[31] = 5
	if _, err := secp256k1.RecoverPublicKey(hash, sig); err != secp256k1.ErrNoCurvePoint {
		t.Fatalf("x=5 has no point: %v", err)
	}
	if _, ok := secp256k1.SignatureFromBytes(make([]byte, 64)); ok {
		t.Fatal("64-byte signature accepted")
	}
	b := sig.Bytes()
	back, ok := secp256k1.SignatureFromBytes(b[:])
	if !ok || back != sig {
		t.Fatal("signature bytes round trip")
	}
}

func BenchmarkRecoverAddress(b *testing.B) {
	var key secp256k1.PrivateKey
	key[0] = 0x55
	key[31] = 0x99
	hash := keccak.Sum256([]byte("bench"))
	sig, err := secp256k1.Sign(hash, key)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, err := secp256k1.RecoverAddress(hash, sig); err != nil {
			b.Fatal(err)
		}
	}
}
