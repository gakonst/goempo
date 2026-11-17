package types_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/crypto/secp256k1"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

type testKey struct {
	key  secp256k1.PrivateKey
	addr primitives.Address
}

func secpKey(t *testing.T, seed byte) testKey {
	t.Helper()
	var key secp256k1.PrivateKey
	key[31] = seed
	key[0] = 0x11
	sig, err := secp256k1.Sign(primitives.Hash{1}, key)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := secp256k1.RecoverAddress(primitives.Hash{1}, sig)
	if err != nil {
		t.Fatal(err)
	}
	return testKey{key: key, addr: addr}
}

func secpSign(t *testing.T, hash primitives.Hash, key secp256k1.PrivateKey) types.Signature {
	t.Helper()
	sig, err := secp256k1.Sign(hash, key)
	if err != nil {
		t.Fatal(err)
	}
	return types.Signature{R: sig.R, S: sig.S, YParity: sig.RecoveryID == 1}
}

func sampleTempoTx() *types.TempoTx {
	to := primitives.Address{0x20, 0xc0}
	return &types.TempoTx{
		ChainID:              4217,
		MaxPriorityFeePerGas: u256.FromUint64(1),
		MaxFeePerGas:         u256.FromUint64(20_000_000_000),
		GasLimit:             100_000,
		Calls:                []types.Call{{To: &to, Value: u256.FromUint64(0), Input: []byte{0xa9, 0x05, 0x9c, 0xbb}}},
		NonceKey:             u256.FromUint64(7),
		Nonce:                3,
		ValidBefore:          2_000_000_000,
		ValidAfter:           1_000_000_000,
		FeeToken:             &to,
	}
}

func roundTrip(t *testing.T, tx types.Transaction) types.Transaction {
	t.Helper()
	enc := tx.Encode()
	back, err := types.DecodeTransaction(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(back.Encode()) != string(enc) {
		t.Fatal("round trip differs")
	}
	return back
}

func TestTempoSecp256k1AndFeePayer(t *testing.T) {
	k1 := secpKey(t, 1)
	key, addr := k1.key, k1.addr
	k2 := secpKey(t, 2)
	payerKey, payer := k2.key, k2.addr
	data := sampleTempoTx()
	tx := types.Transaction{Data: data}
	hashBefore := tx.SigningHash()
	data.FeePayerSignature = &types.Signature{}
	if tx.SigningHash() == hashBefore {
		t.Fatal("sponsorship must change the signing hash")
	}
	sig := secpSign(t, tx.SigningHash(), key)
	data.Signature = types.TempoSignature{Inner: types.PrimitiveSignature{Kind: types.SignatureSecp256k1, R: sig.R, S: sig.S, YParity: sig.YParity}}
	fp := secpSign(t, data.FeePayerSigningHash(addr), payerKey)
	data.FeePayerSignature = &fp
	back := roundTrip(t, tx)
	sender, err := back.Sender()
	if err != nil {
		t.Fatal(err)
	}
	if sender != addr {
		t.Fatalf("sender %s want %s", sender, addr)
	}
	got, err := back.Data.(*types.TempoTx).FeePayer(sender)
	if err != nil {
		t.Fatal(err)
	}
	if got != payer {
		t.Fatalf("fee payer %s want %s", got, payer)
	}
}

func TestKeychainV2(t *testing.T) {
	k3 := secpKey(t, 3)
	accessKey, keyAddr := k3.key, k3.addr
	user := primitives.Address{0xaa, 0xbb}
	data := sampleTempoTx()
	tx := types.Transaction{Data: data}
	inner := keccak.Sum256Concat([]byte{0x04}, func() []byte { h := tx.SigningHash(); return h[:] }(), user[:])
	sig := secpSign(t, inner, accessKey)
	data.Signature = types.TempoSignature{
		Keychain:    types.KeychainV2,
		UserAddress: user,
		Inner:       types.PrimitiveSignature{Kind: types.SignatureSecp256k1, R: sig.R, S: sig.S, YParity: sig.YParity},
	}
	back := roundTrip(t, tx)
	sender, err := back.Sender()
	if err != nil {
		t.Fatal(err)
	}
	if sender != user {
		t.Fatalf("sender %s want user %s", sender, user)
	}
	id, err := back.Data.(*types.TempoTx).Signature.KeyID(back.SigningHash())
	if err != nil {
		t.Fatal(err)
	}
	if id != keyAddr {
		t.Fatalf("key id %s want %s", id, keyAddr)
	}
}

func p256Key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func p256Sign(t *testing.T, k *ecdsa.PrivateKey, msg []byte) types.PrimitiveSignature {
	t.Helper()
	r, s, err := ecdsa.Sign(rand.Reader, k, msg)
	if err != nil {
		t.Fatal(err)
	}
	n := elliptic.P256().Params().N
	if s.Cmp(new(big.Int).Rsh(n, 1)) > 0 {
		s.Sub(n, s)
	}
	var sig types.PrimitiveSignature
	sig.Kind = types.SignatureP256
	r.FillBytes(sig.R[:])
	s.FillBytes(sig.S[:])
	k.X.FillBytes(sig.PubKeyX[:])
	k.Y.FillBytes(sig.PubKeyY[:])
	return sig
}

func p256Address(sig types.PrimitiveSignature) primitives.Address {
	h := keccak.Sum256Concat(sig.PubKeyX[:], sig.PubKeyY[:])
	return primitives.BytesToAddress(h[12:])
}

func TestP256Signatures(t *testing.T) {
	k := p256Key(t)
	data := sampleTempoTx()
	tx := types.Transaction{Data: data}
	h := tx.SigningHash()
	pre := sha256.Sum256(h[:])
	for _, prehash := range []bool{false, true} {
		msg := h[:]
		if prehash {
			msg = pre[:]
		}
		sig := p256Sign(t, k, msg)
		sig.PreHash = prehash
		data.Signature = types.TempoSignature{Inner: sig}
		back := roundTrip(t, tx)
		sender, err := back.Sender()
		if err != nil {
			t.Fatal(err)
		}
		if sender != p256Address(sig) {
			t.Fatalf("prehash %v: sender %s", prehash, sender)
		}
	}
	sig := p256Sign(t, k, h[:])
	n := elliptic.P256().Params().N
	high := new(big.Int).Sub(n, new(big.Int).SetBytes(sig.S[:]))
	high.FillBytes(sig.S[:])
	if _, err := sig.Recover(h); !errors.Is(err, types.ErrHighS) {
		t.Fatalf("high s accepted: %v", err)
	}
}

func webAuthnData(challenge primitives.Hash, flags byte, kind string) []byte {
	auth := make([]byte, 37)
	auth[32] = flags
	client := `{"type":"` + kind + `","challenge":"` + base64.RawURLEncoding.EncodeToString(challenge[:]) + `","origin":"https://x"}`
	return append(auth, client...)
}

func TestWebAuthn(t *testing.T) {
	k := p256Key(t)
	data := sampleTempoTx()
	tx := types.Transaction{Data: data}
	h := tx.SigningHash()
	wa := webAuthnData(h, 0x05, "webauthn.get")
	msg, err := types.WebAuthnMessage(wa, h)
	if err != nil {
		t.Fatal(err)
	}
	sig := p256Sign(t, k, msg[:])
	sig.Kind = types.SignatureWebAuthn
	sig.WebAuthnData = wa
	data.Signature = types.TempoSignature{Inner: sig}
	back := roundTrip(t, tx)
	sender, err := back.Sender()
	if err != nil {
		t.Fatal(err)
	}
	if sender != p256Address(sig) {
		t.Fatalf("sender %s", sender)
	}
	bad := []struct {
		name string
		data []byte
	}{
		{"no user flags", webAuthnData(h, 0x00, "webauthn.get")},
		{"attested data", webAuthnData(h, 0x45, "webauthn.get")},
		{"extension data", webAuthnData(h, 0x85, "webauthn.get")},
		{"wrong type", webAuthnData(h, 0x05, "webauthn.create")},
		{"wrong challenge", webAuthnData(primitives.Hash{9}, 0x05, "webauthn.get")},
		{"short", make([]byte, 40)},
	}
	for _, c := range bad {
		if _, err := types.WebAuthnMessage(c.data, h); !errors.Is(err, types.ErrWebAuthn) {
			t.Fatalf("%s: accepted", c.name)
		}
	}
}

func TestKeyAuthorizationEncoding(t *testing.T) {
	k4 := secpKey(t, 4)
	signer, account := k4.key, k4.addr
	ka := types.KeyAuthorization{
		ChainID:   4217,
		KeyType:   types.SignatureP256,
		KeyID:     primitives.Address{0x01},
		Expiry:    1_900_000_000,
		HasLimits: true,
		Limits:    []types.TokenLimit{{Token: primitives.Address{0x20}, Limit: u256.FromUint64(1000), Period: 86400}},
		IsAdmin:   true,
	}
	sig := secpSign(t, ka.SigningHash(), signer)
	data := sampleTempoTx()
	data.KeyAuthorization = &types.SignedKeyAuthorization{
		Authorization: ka,
		Signature:     types.PrimitiveSignature{Kind: types.SignatureSecp256k1, R: sig.R, S: sig.S, YParity: sig.YParity},
	}
	txSig := secpSign(t, types.Transaction{Data: data}.SigningHash(), signer)
	data.Signature = types.TempoSignature{Inner: types.PrimitiveSignature{Kind: types.SignatureSecp256k1, R: txSig.R, S: txSig.S, YParity: txSig.YParity}}
	back := roundTrip(t, types.Transaction{Data: data})
	got := back.Data.(*types.TempoTx).KeyAuthorization
	if got == nil {
		t.Fatal("key authorization lost")
	}
	if !got.Authorization.HasLimits || got.Authorization.HasAllowedCalls || !got.Authorization.IsAdmin {
		t.Fatal("optional fields not preserved")
	}
	who, err := got.Signer()
	if err != nil {
		t.Fatal(err)
	}
	if who != account {
		t.Fatalf("signer %s want %s", who, account)
	}
	enc := ka.Encode()
	padded := append(append([]byte{enc[0] + 1}, enc[1:]...), 0x80)
	if _, err := types.DecodeTransaction(append([]byte{0x76}, padded...)); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestValidateRules(t *testing.T) {
	to := primitives.Address{1}
	cases := []struct {
		name  string
		edit  func(tx *types.TempoTx)
		error error
	}{
		{"no calls", func(tx *types.TempoTx) { tx.Calls = nil }, types.ErrCalls},
		{"create not first", func(tx *types.TempoTx) { tx.Calls = append(tx.Calls, types.Call{}) }, types.ErrCalls},
		{"create with auth list", func(tx *types.TempoTx) {
			tx.Calls = []types.Call{{}}
			tx.AuthorizationList = []types.AASignedAuthorization{{Signature: types.TempoSignature{Inner: types.PrimitiveSignature{Kind: types.SignatureSecp256k1}}}}
		}, types.ErrCalls},
		{"validity window", func(tx *types.TempoTx) { tx.ValidBefore = tx.ValidAfter }, types.ErrValidity},
		{"create first ok", func(tx *types.TempoTx) { tx.Calls = []types.Call{{}, {To: &to}} }, nil},
	}
	for _, c := range cases {
		tx := sampleTempoTx()
		c.edit(tx)
		if err := tx.Validate(); !errors.Is(err, c.error) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.error)
		}
		_, err := types.DecodeTransaction(types.Transaction{Data: tx}.Encode())
		if !errors.Is(err, c.error) {
			t.Fatalf("%s: decode got %v want %v", c.name, err, c.error)
		}
	}
}

type namedBytes struct {
	name string
	data []byte
}

func TestDecodeErrors(t *testing.T) {
	good := types.Transaction{Data: sampleTempoTx()}.Encode()
	cases := []namedBytes{
		{"empty", []byte{}},
		{"unknown type", append([]byte{0x03}, good[1:]...)},
		{"blob type", append([]byte{0x05}, good[1:]...)},
		{"trailing", append(append([]byte{}, good...), 0x00)},
		{"truncated", good[:len(good)-3]},
	}
	for _, c := range cases {
		if _, err := types.DecodeTransaction(c.data); err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
	}
	sigs := []namedBytes{
		{"short", []byte{0x01}},
		{"bad p256", append([]byte{0x01}, make([]byte, 128)...)},
		{"configurable", append([]byte{0x05}, make([]byte, 100)...)},
		{"unknown", append([]byte{0x09}, make([]byte, 100)...)},
		{"bad v", append(make([]byte, 64), 5)},
	}
	for _, c := range sigs {
		if _, err := types.ParseTempoSignature(c.data); err == nil {
			t.Fatalf("signature %s: accepted", c.name)
		}
	}
}

func TestSecp256k1HighSRejected(t *testing.T) {
	k5 := secpKey(t, 5)
	key, _ := k5.key, k5.addr
	h := primitives.Hash{7}
	sig := secpSign(t, h, key)
	var n [32]byte
	secp256k1.Order().PutBytes32(n[:])
	high := new(big.Int).Sub(new(big.Int).SetBytes(n[:]), new(big.Int).SetBytes(sig.S[:]))
	high.FillBytes(sig.S[:])
	ps := types.PrimitiveSignature{Kind: types.SignatureSecp256k1, R: sig.R, S: sig.S, YParity: !sig.YParity}
	if _, err := ps.Recover(h); !errors.Is(err, types.ErrHighS) {
		t.Fatalf("high s accepted: %v", err)
	}
}

func TestLegacyEIP155(t *testing.T) {
	k6 := secpKey(t, 6)
	key, addr := k6.key, k6.addr
	to := primitives.Address{2}
	for _, chainID := range []uint64{0, 4217} {
		tx := &types.LegacyTx{Nonce: 1, GasPrice: u256.FromUint64(10), Gas: 21000, To: &to, Value: u256.FromUint64(5)}
		if chainID != 0 {
			tx.V = 35 + 2*chainID
		} else {
			tx.V = 27
		}
		sig := secpSign(t, types.Transaction{Data: tx}.SigningHash(), key)
		tx.R, tx.S = sig.R, sig.S
		if sig.YParity {
			tx.V++
		}
		back := roundTrip(t, types.Transaction{Data: tx})
		sender, err := back.Sender()
		if err != nil {
			t.Fatal(err)
		}
		if sender != addr {
			t.Fatalf("chain %d: sender %s", chainID, sender)
		}
		if got, ok := tx.ChainID(); ok != (chainID != 0) || got != chainID {
			t.Fatalf("chain id %d %v", got, ok)
		}
	}
}
