package validator_test

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/gakonst/goempo/internal/crypto/bls12381"
	"github.com/gakonst/goempo/internal/dkg"
	"github.com/gakonst/goempo/internal/validator"
	"github.com/kapetan-io/tiger/assert"
)

const (
	rustSigningKey   = "0x7848b5d711bc9883996317a3f9c90269d56771005d540a19184939c9e8d0db2a"
	rustSigningShare = "0x00594108e8326f1a4f1dcfd0a473141bb95c54c9a591983922158f1f082c671e31"
)

func TestSigningKeyRustSnapshot(_ *testing.T) {
	k, err := validator.ParseSigningKey("  " + rustSigningKey + "\n")
	assert.NoError(err, "parse tempo's consensus-config snapshot key")
	assert.Equal(k.Hex(), rustSigningKey, "hex round trip")
	seed := k.Seed()
	want := ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey)
	pub := k.Public()
	assert.Ok(bytes.Equal(pub[:], want), "public key is the RFC 8032 key of the seed")
}

func TestSigningKeyRejects(_ *testing.T) {
	_, err := validator.ParseSigningKey("0x1234")
	assert.Ok(err == validator.ErrKeyLength, "short key")
	_, err = validator.ParseSigningKey("0xzz")
	assert.Ok(err == validator.ErrKeyHex, "not hex")
}

func TestSigningShareRustSnapshot(_ *testing.T) {
	s, err := validator.ParseShare(rustSigningShare)
	assert.NoError(err, "parse tempo's consensus-config snapshot share")
	assert.Equal(s.Index, 0, "index")
	assert.Equal(validator.ShareHex(s), rustSigningShare, "hex round trip")
}

func TestShareIndexVarint(_ *testing.T) {
	k := bls12381.ScalarFromUint64(7)
	for _, idx := range []int{0, 1, 127, 128, 300, 1 << 20} {
		s := dkg.Share{Index: idx, Private: k}
		got, err := validator.DecodeShare(validator.EncodeShare(s))
		assert.NoError(err, "decode")
		assert.Equal(got.Index, idx, "index round trips")
	}
	_, err := validator.DecodeShare(append([]byte{0x80, 0x00}, k[:]...))
	assert.Ok(err == validator.ErrShareIndex, "non-canonical varint")
	_, err = validator.DecodeShare(append([]byte{0x00}, make([]byte, bls12381.ScalarLength)...))
	assert.Ok(err == validator.ErrShareScalar, "zero scalar")
	_, err = validator.DecodeShare(append([]byte{0x00}, k[:31]...))
	assert.Ok(err == validator.ErrShareScalar, "short scalar")
}

func TestKeyFiles(t *testing.T) {
	dir := t.TempDir()
	k, err := validator.ParseSigningKey(rustSigningKey)
	assert.NoError(err, "parse")
	path := filepath.Join(dir, "signing.key")
	assert.NoError(validator.WriteSigningKey(path, k, validator.NoOverwrite), "write")
	info, err := os.Stat(path)
	assert.NoError(err, "stat")
	assert.Equal(info.Mode().Perm(), os.FileMode(0o600), "key file is owner-only")
	assert.Ok(validator.WriteSigningKey(path, k, validator.NoOverwrite) == validator.ErrKeyExists, "no overwrite without force")
	assert.NoError(validator.WriteSigningKey(path, k, validator.Replace), "force overwrites")
	back, err := validator.ReadSigningKey(path)
	assert.NoError(err, "read")
	assert.Equal(back, k, "file round trip")
	s, err := validator.ParseShare(rustSigningShare)
	assert.NoError(err, "share")
	sp := filepath.Join(dir, "signing.share")
	assert.NoError(validator.WriteShare(sp, s, validator.NoOverwrite), "write share")
	sb, err := validator.ReadShare(sp)
	assert.NoError(err, "read share")
	assert.Equal(sb, s, "share file round trip")
}

func TestUnionUnique(_ *testing.T) {
	ns := bytes.Repeat([]byte{1}, 200)
	got := validator.UnionUnique(ns, []byte{9})
	assert.Ok(bytes.Equal(got[:2], []byte{0xc8, 0x01}), "varint length prefix")
	assert.Equal(len(got), 2+200+1, "length")
}
