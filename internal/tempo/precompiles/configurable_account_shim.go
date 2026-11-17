package precompiles

import (
	"bytes"
	"encoding/binary"

	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/types"
)

const (
	caSignatureType      = 0x05
	caOwnerSigBytesMax   = 1 + 2048
	caSecpVerifyGas      = 3_000
	caP256VerifyGas      = 8_000
	caTokenCost          = 4
	caNonzeroTokens      = 4
	caSignaturePreimage  = 36 + 32 + 20 + 8
	caAccountPreimageFix = 34 + 32 + 2
	caConfigPreimageFix  = 33 + 32 + 8 + 2
	caOwnerPreimageBytes = 21
)

const _ = uint(caP256VerifyGas - caSecpVerifyGas)
const _ = uint(caOwnerSigBytesMax - caSignaturePreimage)

var (
	caSigDomain        = []byte("tempo:configurable-account:signature")
	selIsValidSig      = selector("isValidSignature(bytes32,bytes)")
	selOnERC721        = selector("onERC721Received(address,address,uint256,bytes)")
	caIsValidSigArgs   = []abiType{tB32, tBytes}
	caOnERC721Args     = []abiType{tAddress, tAddress, tU256, tBytes}
	caInvalidSignature = [4]byte{0xff, 0xff, 0xff, 0xff}
)

type caSignature struct {
	account Address
	config  caConfig
	witness []byte
	sigs    []types.PrimitiveSignature
}

type caWeights struct {
	threshold uint8
	prev      Address
	hasPrev   flag
	weight    int
	minWeight int
	signers   int
}

type caShimCheck struct {
	account Address
	hash    Hash
	raw     []byte
}

func decodeCAOwners(s *rlp.Stream) ([]caOwner, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []caOwner
	for range caMaxOwners + 1 {
		if list.Empty() {
			return out, nil
		}
		item, err := list.List()
		if err != nil {
			return nil, err
		}
		owner, err := item.Address()
		if err != nil {
			return nil, err
		}
		w, err := item.UintBytes(1)
		if err != nil {
			return nil, err
		}
		if err := item.Finish(); err != nil {
			return nil, err
		}
		weight := uint8(0)
		if len(w) == 1 {
			weight = w[0]
		}
		out = append(out, caOwner{owner: owner, weight: weight})
	}
	return nil, errABI
}

func decodeCAConfig(s *rlp.Stream) (caConfig, error) {
	list, err := s.List()
	if err != nil {
		return caConfig{}, err
	}
	var cfg caConfig
	if cfg.salt, err = list.Hash(); err != nil {
		return caConfig{}, err
	}
	if cfg.version, err = list.Uint64(); err != nil {
		return caConfig{}, err
	}
	thr, err := list.UintBytes(1)
	if err != nil {
		return caConfig{}, err
	}
	if len(thr) == 1 {
		cfg.threshold = thr[0]
	}
	if cfg.owners, err = decodeCAOwners(&list); err != nil {
		return caConfig{}, err
	}
	return cfg, list.Finish()
}

func decodeCASigs(s *rlp.Stream) ([]types.PrimitiveSignature, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []types.PrimitiveSignature
	for range caMaxSignatures + 1 {
		if list.Empty() {
			return out, nil
		}
		raw, err := list.Bytes()
		if err != nil {
			return nil, err
		}
		if len(raw) > caOwnerSigBytesMax {
			return nil, errABI
		}
		sig, err := types.ParsePrimitiveSignature(raw)
		if err != nil || sig.Kind == types.SignatureAccount {
			return nil, errABI
		}
		out = append(out, sig)
	}
	return nil, errABI
}

func parseCASignature(raw []byte) (caSignature, error) {
	if len(raw) < 2 || raw[0] != caSignatureType {
		return caSignature{}, errABI
	}
	outer := rlp.NewStream(raw[1:])
	body, err := outer.List()
	if err != nil {
		return caSignature{}, err
	}
	if err := outer.Finish(); err != nil {
		return caSignature{}, err
	}
	start := body.Rest()
	var sig caSignature
	if sig.account, err = body.Address(); err != nil {
		return caSignature{}, err
	}
	if sig.config, err = decodeCAConfig(&body); err != nil {
		return caSignature{}, err
	}
	sig.witness = start[:len(start)-len(body.Rest())]
	if sig.sigs, err = decodeCASigs(&body); err != nil {
		return caSignature{}, err
	}
	if err := body.Finish(); err != nil {
		return caSignature{}, err
	}
	if sig.account == (Address{}) || len(sig.sigs) == 0 {
		return caSignature{}, errABI
	}
	if err := sig.config.validate(sig.account, true); err != nil {
		return caSignature{}, errABI
	}
	return sig, nil
}

func caTokens(b []byte) uint64 {
	n := uint64(0)
	for _, x := range b {
		if x == 0 {
			n++
			continue
		}
		n += caNonzeroTokens
	}
	return n
}

func caKeccakGas(n int) uint64 { return gasKeccak + gasKeccakWord*uint64((n+31)/32) }

func caPrimitiveGas(sig types.PrimitiveSignature) uint64 {
	if sig.Kind == types.SignatureSecp256k1 {
		return caSecpVerifyGas
	}
	if sig.Kind == types.SignatureWebAuthn {
		return caP256VerifyGas + caTokens(sig.WebAuthnData)*caTokenCost
	}
	return caP256VerifyGas
}

func (sig caSignature) verificationGas() uint64 {
	gas := caTokens(sig.witness) * caTokenCost
	for _, s := range sig.sigs {
		gas += caPrimitiveGas(s)
	}
	gas += caKeccakGas(caConfigPreimageFix + caOwnerPreimageBytes*len(sig.config.owners))
	return gas + caKeccakGas(caSignaturePreimage)
}

func (sig caSignature) derivationGas() uint64 {
	return caKeccakGas(caAccountPreimageFix + caOwnerPreimageBytes*len(sig.config.owners))
}

func (sig caSignature) commitmentMatches(c *Context, stored Hash) bool {
	actual := c.keccak(sig.config.commitmentPreimage())
	if actual == (Hash{}) {
		return false
	}
	if stored != (Hash{}) {
		return stored == actual
	}
	if sig.config.version != 0 {
		return false
	}
	return wordAddress(c.keccak(sig.config.accountPreimage())) == sig.account
}

func (w *caWeights) quorum() bool { return w.signers > 0 && w.weight >= int(w.threshold) }

func (w *caWeights) record(owner Address, weight uint8) error {
	if w.quorum() || weight == 0 {
		return errSel("InvalidSignature()")
	}
	w.signers++
	if w.signers > caMaxSignatures {
		return errSel("InvalidSignature()")
	}
	if bool(w.hasPrev) && bytes.Compare(w.prev[:], owner[:]) >= 0 {
		return errSel("InvalidSignature()")
	}
	w.prev, w.hasPrev = owner, true
	w.weight += int(weight)
	if w.minWeight == 0 || int(weight) < w.minWeight {
		w.minWeight = int(weight)
	}
	return nil
}

func (w *caWeights) finish() error {
	if w.signers == 0 || w.weight < int(w.threshold) {
		return errSel("InvalidSignature()")
	}
	if w.weight >= int(w.threshold)+w.minWeight {
		return errSel("InvalidSignature()")
	}
	return nil
}

func (sig caSignature) verifyApprovals(c *Context, inner Hash) error {
	pre := append(append(append([]byte{}, caSigDomain...), inner[:]...), sig.account[:]...)
	digest := c.keccak(binary.BigEndian.AppendUint64(pre, sig.config.version))
	w := caWeights{threshold: sig.config.threshold}
	for i, s := range sig.sigs {
		owner, err := s.Recover(digest)
		if err != nil {
			return errSel("InvalidSignature()")
		}
		weight, _ := sig.config.ownerWeight(owner)
		if err := w.record(owner, weight); err != nil {
			return err
		}
		if w.quorum() && i+1 != len(sig.sigs) {
			return errSel("InvalidSignature()")
		}
	}
	return w.finish()
}

func (c *Context) verifyConfigurableSignature(in caShimCheck) (bool, error) {
	sig, err := parseCASignature(in.raw)
	if err != nil {
		return false, errSel("InvalidFormat()")
	}
	if err := c.loadAccountGas(in.account); err != nil {
		return false, err
	}
	commitment := c.commitmentOf(in.account)
	codeHash := c.State.CodeHash(in.account)
	if codeHash == (Hash{}) {
		codeHash = emptyCodeHash
	}
	gas := sig.verificationGas()
	if commitment == (Hash{}) {
		gas += sig.derivationGas()
	}
	if err := c.charge(gas); err != nil {
		return false, err
	}
	wantCode := emptyCodeHash
	if commitment != (Hash{}) {
		wantCode = configurableCodeHash
	}
	if sig.account != in.account || !ValidConfigurableAccount(in.account, c.Fork) || codeHash != wantCode {
		return false, nil
	}
	if !sig.commitmentMatches(c, commitment) {
		return false, nil
	}
	return true, sig.verifyApprovals(c, in.hash)
}

func ConfigurableAccountShim(c *Context, account Address, input []byte) ([]byte, error) {
	if err := c.charge(inputCost(c.Fork, len(input))); err != nil {
		return nil, err
	}
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) < 4 {
		return nil, revert(nil)
	}
	mode := decodeMode{strict: true}
	switch [4]byte(input[:4]) {
	case selIsValidSig:
		args, err := decodeABI(input[4:], caIsValidSigArgs, mode)
		if err != nil {
			return nil, revert(nil)
		}
		if err := c.charge(inputCost(c.Fork, 32)); err != nil {
			return nil, err
		}
		ok, err := c.verifyConfigurableSignature(caShimCheck{account: account, hash: args[0].word, raw: args[1].data})
		if err != nil {
			return nil, err
		}
		out := caInvalidSignature
		if ok {
			out = selIsValidSig
		}
		return encodeWords(Hash(append(out[:], make([]byte, 28)...))), nil
	case selOnERC721:
		if _, err := decodeABI(input[4:], caOnERC721Args, mode); err != nil {
			return nil, revert(nil)
		}
		return encodeWords(Hash(append(selOnERC721[:], make([]byte, 28)...))), nil
	default:
		return nil, revert(nil)
	}
}
