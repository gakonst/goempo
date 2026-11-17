package precompiles

import (
	"bytes"
	"encoding/hex"
	"errors"

	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
)

const (
	zoneVerifierBaseGas      = 40_000
	zoneVerifierSignatureGas = 150_000
	zoneVerifierFutureSkewMs = 300_000
	zoneVerifyStaticWords    = 17
	zoneVerifyMaxCalldata    = 4 + zoneVerifyStaticWords*32 + 2*32 + 32 + nitroMaxDocument
	zoneApprovedPCRCount     = 3
)

const _ = uint(zoneVerifyMaxCalldata - 25_220)
const _ = uint(25_220 - zoneVerifyMaxCalldata)

var awsNitroRootDER = mustHex(
	"3082021130820196a003020102021100f93175681b90afe11d46ccb4e4e7f856300a06082a8648ce3d0403033049310b" +
		"3009060355040613025553310f300d060355040a0c06416d617a6f6e310c300a060355040b0c03415753311b30190603" +
		"5504030c126177732e6e6974726f2d656e636c61766573301e170d3139313032383133323830355a170d343931303238" +
		"3134323830355a3049310b3009060355040613025553310f300d060355040a0c06416d617a6f6e310c300a060355040b" +
		"0c03415753311b301906035504030c126177732e6e6974726f2d656e636c617665733076301006072a8648ce3d020106" +
		"052b8104002203620004fc0254eba608c1f36870e29ada90be46383292736e894bfff672d989444b5051e534a4b1f6db" +
		"e3c0bc581a32b7b176070ede12d69a3fea211b66e752cf7dd1dd095f6f1370f4170843d9dc100121e4cf630128096644" +
		"87c9796284304dc53ff4a3423040300f0603551d130101ff040530030101ff301d0603551d0e041604149025b50dd905" +
		"47e796c396fa729dcf99a9df4b96300e0603551d0f0101ff040403020186300a06082a8648ce3d040303036900306602" +
		"3100a37f2f91a1c9bd5ee7b8627c1698d255038e1f0343f95b63a9628c3d39809545a11ebcbf2e3b55d8aeee71b4c3d6" +
		"adf3023100a2f39b1605b27028a5dd4ba069b5016e65b4fbde8fe0061d6a53197f9cdaf5d943bc61fc2beb03cb6fee8d" +
		"2302f3dff6")

type zonePCRPolicy struct {
	since hardfork.Fork
	pcrs  [zoneApprovedPCRCount][]byte
}

var zoneApprovedPCRs = []zonePCRPolicy{{
	since: hardfork.T13,
	pcrs: [zoneApprovedPCRCount][]byte{
		mustHex("d64354677406bfda618e6f694528eec9e96c967296a4021249ad0153234f10f95861cd13509fbb7240f7a6d38ce69302"),
		mustHex("0cfa8adbf2c8842affa4e9d2bf3cd59ba3782c538ca5086c203d2382ed8b9945f289537c86dcbe52ac85057e8895fdbd"),
		mustHex("7cde50b7d313a691c03106281a84f6ae3c7bcdcffc8063bd1729d5f4969f535c508553c7102475bc1f02b97432ab56b9"),
	},
}}

var (
	zoneModeNitroV1 = []byte{0x01}
	zoneModeNoProof = []byte{0x02}

	zoneBatchAttestationType = topic("NitroBatchAttestation(uint256 parentChainId,address verifier," +
		"uint32 zoneId,uint64 tempoBlockNumber,uint64 anchorBlockNumber,bytes32 anchorBlockHash," +
		"uint64 expectedWithdrawalBatchIndex,uint256 nextZoneHeight,bytes32 prevBlockHash," +
		"bytes32 nextBlockHash,bytes32 prevProcessedHash,bytes32 nextProcessedHash," +
		"uint64 prevDepositNumber,uint64 nextDepositNumber,uint64 prevProcessedTokenCount," +
		"uint64 nextProcessedTokenCount,bytes32 withdrawalQueueHash,bytes32 verifierConfigHash)")

	zoneVerifyArgTypes = []abiType{
		tU32, tU64, tU64, tB32, tU64, tU256,
		tB32, tB32,
		tB32, tB32, tU64, tU64,
		tU64, tU64,
		tB32, tBytes, tBytes,
	}
)

const (
	zvZoneID = iota
	zvTempoBlock
	zvAnchorBlock
	zvAnchorHash
	zvWithdrawalIndex
	zvNextZoneHeight
	zvPrevBlockHash
	zvNextBlockHash
	zvPrevProcessed
	zvNextProcessed
	zvPrevDeposit
	zvNextDeposit
	zvPrevTokenCount
	zvNextTokenCount
	zvWithdrawalQueue
	zvConfig
	zvProof
	zvArgCount
)

const _ = uint(zvConfig + 2 - zoneVerifyStaticWords)
const _ = uint(zoneVerifyStaticWords - zvConfig - 2)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func zonePCRsAt(fork hardfork.Fork) ([zoneApprovedPCRCount][]byte, bool) {
	var best *zonePCRPolicy
	for i := range zoneApprovedPCRs {
		e := &zoneApprovedPCRs[i]
		if fork < e.since {
			continue
		}
		if best == nil || e.since > best.since {
			best = e
		}
	}
	if best == nil {
		return [zoneApprovedPCRCount][]byte{}, false
	}
	return best.pcrs, true
}

func zonePCRsMatch(doc nitroDoc, approved [zoneApprovedPCRCount][]byte) bool {
	for i, want := range approved {
		found := false
		for _, p := range doc.pcrs {
			if p.index == i {
				found = bytes.Equal(p.value, want)
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func zoneBatchCommitment(chainID Hash, a []abiValue) Hash {
	words := make([]byte, 0, 32*19)
	add := func(h Hash) { words = append(words, h[:]...) }
	add(zoneBatchAttestationType)
	add(chainID)
	add(addressWord(ZoneVerifierAddress))
	for i := zvZoneID; i < zvConfig; i++ {
		add(a[i].word)
	}
	add(keccak.Sum256(a[zvConfig].data))
	return keccak.Sum256(words)
}

func (c *Context) zoneVerifyAttestation(proof []byte) (nitroDoc, error) {
	if len(proof) > nitroMaxDocument {
		return nitroDoc{}, errNitro
	}
	if err := c.charge(zoneVerifierBaseGas); err != nil {
		return nitroDoc{}, err
	}
	doc, err := parseNitroDocument(proof)
	if err != nil {
		return nitroDoc{}, err
	}
	if err := c.charge(uint64(len(doc.cabundle)+1) * zoneVerifierSignatureGas); err != nil {
		return nitroDoc{}, err
	}
	key, err := nitroValidateChain(doc, c.Block.Time)
	if err != nil {
		return nitroDoc{}, err
	}
	digest := nitroSigStructureHash(doc)
	if !p384VerifyFixed(p384Check{key: key, digest: digest[:], sig: doc.signature}) {
		return nitroDoc{}, errNitro
	}
	return doc, nil
}

func (c *Context) zoneVerify(a []abiValue) (bool, error) {
	portal := ZonePortalAddress(uint32(a[zvZoneID].u64()))
	if c.Caller != portal {
		return false, nil
	}
	config, proof := a[zvConfig].data, a[zvProof].data
	if !bytes.Equal(config, zoneModeNitroV1) || len(proof) == 0 {
		return bytes.Equal(config, zoneModeNoProof) && len(proof) == 0, nil
	}
	doc, err := c.zoneVerifyAttestation(proof)
	if errors.Is(err, errNitro) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	approved, has := zonePCRsAt(c.Fork)
	if !has || !zonePCRsMatch(doc, approved) {
		return false, nil
	}
	if doc.timestamp > zoneMaxAttestationMs(c.Block.Time) || len(doc.userData) != 32 {
		return false, nil
	}
	want := zoneBatchCommitment(toHash(c.ChainID), a)
	return bytes.Equal(doc.userData, want[:]), nil
}

func zoneMaxAttestationMs(blockTime uint64) uint64 {
	limit := ^uint64(0)
	if blockTime > limit/1_000 {
		return limit
	}
	ms := blockTime * 1_000
	if ms > limit-zoneVerifierFutureSkewMs {
		return limit
	}
	return ms + zoneVerifierFutureSkewMs
}

func (c *Context) zoneVerifyCall(a []abiValue) ([]byte, error) {
	data := a[0].data
	if len(data)+4 > zoneVerifyMaxCalldata {
		return retBool(false), nil
	}
	mode := decodeMode{strict: flag(c.at(hardfork.T11)), trailing: flag(c.at(hardfork.T12))}
	args, err := decodeABI(data, zoneVerifyArgTypes, mode)
	if err != nil {
		return nil, revert(nil)
	}
	ok, err := c.zoneVerify(args)
	return retBool(flag(ok)), err
}

var zoneVerifierTable = register(ZoneVerifierAddress, newTable(
	view("verify(uint32,uint64,uint64,bytes32,uint64,uint256,(bytes32,bytes32),(bytes32,bytes32,uint64,uint64),(uint64,uint64),bytes32,bytes,bytes)", []abiType{tRaw}, func(c *Context, a []abiValue) ([]byte, error) {
		return c.zoneVerifyCall(a)
	}).from(hardfork.T13),
))
