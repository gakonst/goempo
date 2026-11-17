package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/crypto/p256"
	"github.com/gakonst/goempo/internal/primitives"
)

const (
	authDataBytes = 37
	authFlagsAt   = 32
	flagUP        = 0x01
	flagUV        = 0x04
	flagAT        = 0x40
	flagED        = 0x80
	addressAt     = scalarBytes - primitives.AddressLength
	jsonTokensMax = webAuthnSignatureMax
	webAuthnGet   = "webauthn.get"
)

const _ = uint(webAuthnSignatureMax - authDataBytes - scalarBytes)

const _ = uint(jsonTokensMax - webAuthnSignatureMax)

func verifyP256(msg primitives.Hash, sig *PrimitiveSignature) error {
	if !p256.IsLowS(sig.S) {
		return ErrHighS
	}
	key := p256.PublicKey{X: sig.PubKeyX, Y: sig.PubKeyY}
	if !p256.Verify(msg, p256.Signature{R: sig.R, S: sig.S}, key) {
		return ErrP256
	}
	return nil
}

func (sig *PrimitiveSignature) p256Address() primitives.Address {
	h := keccak.Sum256Concat(sig.PubKeyX[:], sig.PubKeyY[:])
	return primitives.BytesToAddress(h[addressAt:])
}

func (sig *PrimitiveSignature) recoverP256(hash primitives.Hash) (primitives.Address, error) {
	msg := hash
	if sig.PreHash {
		msg = sha256.Sum256(hash[:])
	}
	if err := verifyP256(msg, sig); err != nil {
		return primitives.Address{}, err
	}
	return sig.p256Address(), nil
}

func (sig *PrimitiveSignature) recoverWebAuthn(hash primitives.Hash) (primitives.Address, error) {
	msg, err := WebAuthnMessage(sig.WebAuthnData, hash)
	if err != nil {
		return primitives.Address{}, err
	}
	if err := verifyP256(msg, sig); err != nil {
		return primitives.Address{}, err
	}
	return sig.p256Address(), nil
}

func WebAuthnMessage(data []byte, hash primitives.Hash) (primitives.Hash, error) {
	if len(data) < authDataBytes+scalarBytes {
		return primitives.Hash{}, ErrWebAuthn
	}
	flags := data[authFlagsAt]
	if flags&(flagUP|flagUV) == 0 {
		return primitives.Hash{}, ErrWebAuthn
	}
	if flags&flagAT != 0 {
		return primitives.Hash{}, ErrWebAuthn
	}
	if flags&flagED != 0 {
		return primitives.Hash{}, ErrWebAuthn
	}
	client := data[authDataBytes:]
	fields, err := parseClientData(client)
	if err != nil {
		return primitives.Hash{}, err
	}
	if fields.kind != webAuthnGet {
		return primitives.Hash{}, ErrWebAuthn
	}
	if fields.challenge != base64.RawURLEncoding.EncodeToString(hash[:]) {
		return primitives.Hash{}, ErrWebAuthn
	}
	clientHash := sha256.Sum256(client)
	h := sha256.New()
	h.Write(data[:authDataBytes])
	h.Write(clientHash[:])
	var out primitives.Hash
	copy(out[:], h.Sum(nil))
	return out, nil
}

type clientData struct {
	kind      string
	challenge string
	seen      uint8
}

const (
	seenType      = 1
	seenChallenge = 2
)

func parseClientData(b []byte) (clientData, error) {
	var out clientData
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return out, ErrWebAuthn
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return out, ErrWebAuthn
	}
	if tok != json.Delim('{') {
		return out, ErrWebAuthn
	}
	fields := 0
	for ; fields < jsonTokensMax; fields++ {
		if !dec.More() {
			break
		}
		if err := out.readField(dec); err != nil {
			return out, err
		}
	}
	if fields == jsonTokensMax {
		return out, ErrWebAuthn
	}
	if out.seen != seenType|seenChallenge {
		return out, ErrWebAuthn
	}
	return out, nil
}

func (c *clientData) readField(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return ErrWebAuthn
	}
	key, ok := tok.(string)
	if !ok {
		return ErrWebAuthn
	}
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return ErrWebAuthn
	}
	switch key {
	case "type":
		return c.store(raw, seenType)
	case "challenge":
		return c.store(raw, seenChallenge)
	default:
		return nil
	}
}

func (c *clientData) store(raw json.RawMessage, bit uint8) error {
	if c.seen&bit != 0 {
		return ErrWebAuthn
	}
	c.seen |= bit
	n := len(raw)
	if n < 2 {
		return ErrWebAuthn
	}
	if raw[0] != '"' {
		return ErrWebAuthn
	}
	body := raw[1 : n-1]
	for i := range body {
		if body[i] == '\\' {
			return ErrWebAuthn
		}
	}
	if bit == seenType {
		c.kind = string(body)
		return nil
	}
	c.challenge = string(body)
	return nil
}
