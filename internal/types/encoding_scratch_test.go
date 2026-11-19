package types_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
	"github.com/kapetan-io/tiger/assert"
	"golang.org/x/crypto/sha3"
)

// The oracle reads the recorded envelope directly, not through rlp.Encoder or
// TxData.encodePayload. It replaces only the signature/fee-payer fields specified
// by EIP-155/2718 and TempoTx::signature_hash in the reference format.
func scratchListFields(t *testing.T, raw []byte) [][]byte {
	t.Helper()
	item := scratchItem(t, raw)
	payload, total := item.payload, item.total
	if total != len(raw) || raw[0] < 0xc0 {
		t.Fatal("oracle expected exactly one list")
	}
	var out [][]byte
	steps := 0
	for ; len(payload) > 0 && steps < 1000; steps++ {
		n := scratchItem(t, payload).total
		out = append(out, bytes.Clone(payload[:n]))
		payload = payload[n:]
	}
	assert.Ok(steps < 1000, "oracle field count cap reached")
	if len(payload) != 0 {
		t.Fatal("oracle list field bound")
	}
	return out
}

type scratchDecoded struct {
	payload []byte
	total   int
}

func scratchItem(t *testing.T, raw []byte) scratchDecoded {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("oracle truncated item")
	}
	p := int(raw[0])
	if p < 0x80 {
		return scratchDecoded{raw[:1], 1}
	}
	base := 0x80
	if p >= 0xc0 {
		base = 0xc0
	}
	n, header := p-base, 1
	if n > 55 {
		lengthBytes := n - 55
		if lengthBytes > 8 || len(raw) < lengthBytes+1 {
			t.Fatal("oracle invalid length")
		}
		n = 0
		for i := 0; i < lengthBytes; i++ {
			n = n*256 + int(raw[i+1])
		}
		header += lengthBytes
	}
	if n > len(raw)-header {
		t.Fatal("oracle item exceeds input")
	}
	return scratchDecoded{raw[header : header+n], header + n}
}

func scratchWrap(base byte, payload []byte) []byte {
	if base == 0x80 && len(payload) == 1 && payload[0] < 0x80 {
		return bytes.Clone(payload)
	}
	if len(payload) <= 55 {
		return append([]byte{base + byte(len(payload))}, payload...)
	}
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(payload)))
	first := 0
	for first < 7 && size[first] == 0 {
		first++
	}
	out := []byte{base + 55 + byte(8-first)}
	out = append(out, size[first:]...)
	return append(out, payload...)
}

func scratchUint(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	i := 0
	for i < 8 && b[i] == 0 {
		i++
	}
	return scratchWrap(0x80, b[i:])
}

func scratchSigningBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	typed := raw[0] < 0x80
	payload := raw
	if typed {
		payload = raw[1:]
	}
	fields := scratchListFields(t, payload)
	if !typed {
		vBytes := scratchItem(t, fields[6]).payload
		var v uint64
		for _, b := range vBytes {
			v = v*256 + uint64(b)
		}
		fields = fields[:6]
		if v >= 35 {
			fields = append(fields, scratchUint((v-35)/2), []byte{0x80}, []byte{0x80})
		}
	} else if raw[0] == 0x76 {
		fields = fields[:len(fields)-1]
		if fields[11][0] >= 0xc0 {
			fields[10] = []byte{0x80}
			fields[11] = []byte{0x00}
		}
	} else {
		fields = fields[:len(fields)-3]
	}
	out := scratchWrap(0xc0, bytes.Join(fields, nil))
	if typed {
		out = append([]byte{raw[0]}, out...)
	}
	return out
}

func scratchHash(parts ...[]byte) primitives.Hash {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		n, err := h.Write(p)
		assert.NoError(err, "oracle keccak write")
		assert.Equal(n, len(p), "oracle keccak write length")
	}
	var out primitives.Hash
	copy(out[:], h.Sum(nil))
	return out
}

func TestEncodingScratchRecordedEnvelopeOracle(t *testing.T) {
	scratch := types.NewEncodingScratch()
	var retained [][]byte
	var wantBytes [][]byte
	fixtures := readLines[txFixture](t, "testdata/txs.jsonl")
	for _, fx := range fixtures {
		raw := mustHex(t, fx.Envelope)
		tx, err := types.DecodeTransaction(raw)
		if err != nil {
			t.Fatal(err)
		}
		encoded := scratch.EncodeTransaction(tx)
		if !bytes.Equal(encoded, raw) {
			t.Fatalf("%s envelope", fx.Label)
		}
		if scratch.TransactionHash(tx) != mustHash(t, fx.Hash) {
			t.Fatalf("%s recorded hash", fx.Label)
		}
		signing := scratchSigningBytes(t, raw)
		if scratch.SigningHash(tx) != scratchHash(signing) {
			t.Fatalf("%s independent signing hash", fx.Label)
		}
		sender := mustAddress(t, fx.From)
		if scratch.UniqueID(tx, sender) != scratchHash(signing, sender[:]) {
			t.Fatalf("%s independent unique ID", fx.Label)
		}
		retained = append(retained, encoded)
		wantBytes = append(wantBytes, bytes.Clone(raw))
	}
	for i := range retained {
		if !bytes.Equal(retained[i], wantBytes[i]) {
			t.Fatalf("retained envelope %d changed", i)
		}
	}
}

func TestEncodingScratchMutationAndOwnership(t *testing.T) {
	scratch := types.NewEncodingScratch()
	other := types.NewEncodingScratch()
	target := primitives.Address{19: 0xa5}
	var retained [][]byte
	var copies [][]byte
	for n := 0; n < 90; n++ {
		data := bytes.Repeat([]byte{byte(n)}, []int{0, 1, 55, 56, 512, 4096}[n%6])
		dynamic := types.DynamicFeeTx{ChainID: uint64(n), Nonce: uint64(n), Gas: 21000, To: &target, Data: data, MaxFeePerGas: u256.FromUint64(9)}
		txs := []types.Transaction{
			{Data: &types.LegacyTx{V: uint64([]int{27, 28, 35, 36, 8437}[n%5]), Nonce: uint64(n), Data: data}},
			{Data: &types.AccessListTx{ChainID: uint64(n), Data: data}},
			{Data: &dynamic},
			{Data: &types.SetCodeTx{DynamicFeeTx: dynamic}},
			{Data: &types.TempoTx{ChainID: uint64(n), Calls: []types.Call{{To: &target, Input: data}}, Nonce: uint64(n)}},
		}
		for _, tx := range txs {
			sender := primitives.Address{0: byte(n), 19: byte(n + 1)}
			raw := scratch.EncodeTransaction(tx)
			if !bytes.Equal(raw, tx.Encode()) {
				t.Fatal("encoding differs after reuse")
			}
			signing := scratchSigningBytes(t, raw)
			got := scratch.UniqueID(tx, sender)
			if got != scratchHash(signing, sender[:]) || got != tx.UniqueID(sender) {
				t.Fatal("mutated unique ID")
			}
			if scratch.SigningHash(tx) != scratchHash(signing) || scratch.SigningHash(tx) != tx.SigningHash() {
				t.Fatal("mutated signing hash")
			}
			if scratch.TransactionHash(tx) != scratchHash(raw) {
				t.Fatal("mutated transaction hash")
			}
			retained = append(retained, raw)
			copies = append(copies, bytes.Clone(raw))
			if len(raw) > 0 {
				raw[0] ^= 0xff
				raw[0] ^= 0xff
			}
			_ = other.TransactionHash(tx)
		}
	}
	for i := range retained {
		if !bytes.Equal(retained[i], copies[i]) {
			t.Fatal("encoded bytes alias later reuse")
		}
	}
}

func scratchReceiptOracle(r *types.Receipt) []byte {
	var logs []byte
	for _, l := range r.Logs {
		var topics []byte
		for _, topic := range l.Topics {
			topics = append(topics, scratchWrap(0x80, topic[:])...)
		}
		item := scratchWrap(0x80, l.Address[:])
		item = append(item, scratchWrap(0xc0, topics)...)
		item = append(item, scratchWrap(0x80, l.Data)...)
		logs = append(logs, scratchWrap(0xc0, item)...)
	}
	success := uint64(0)
	if r.Success {
		success = 1
	}
	fields := scratchUint(success)
	fields = append(fields, scratchUint(r.CumulativeGasUsed)...)
	fields = append(fields, scratchWrap(0x80, r.Bloom[:])...)
	fields = append(fields, scratchWrap(0xc0, logs)...)
	out := scratchWrap(0xc0, fields)
	if r.Type != types.TxTypeLegacy {
		out = append([]byte{byte(r.Type)}, out...)
	}
	return out
}

func scratchBloomOracle(logs []types.Log) types.Bloom {
	var out types.Bloom
	var inputs [][]byte
	for i := range logs {
		inputs = append(inputs, logs[i].Address[:])
		for j := range logs[i].Topics {
			inputs = append(inputs, logs[i].Topics[j][:])
		}
	}
	for _, input := range inputs {
		hash := scratchHash(input)
		for j := 0; j < 6; j += 2 {
			bit := binary.BigEndian.Uint16(hash[j:j+2]) % 2048
			out[255-bit/8] |= byte(1 << (bit % 8))
		}
	}
	return out
}

func TestEncodingScratchReceiptsBloomAndOwnership(t *testing.T) {
	scratch := types.NewEncodingScratch()
	var retained [][]byte
	var want [][]byte
	for n := 0; n < 80; n++ {
		logs := []types.Log{{Address: primitives.Address{19: byte(n)}, Topics: []primitives.Hash{{31: byte(n)}, {31: byte(n + 1)}, {31: byte(n)}}, Data: bytes.Repeat([]byte{byte(n)}, n*17)}}
		if n%3 == 0 {
			logs = nil
		}
		bloom := scratch.LogsBloom(logs)
		if bloom != scratchBloomOracle(logs) {
			t.Fatal("independent bloom oracle")
		}
		r := types.Receipt{Type: []types.TxType{types.TxTypeLegacy, types.TxTypeDynamicFee, types.TxTypeTempo}[n%3], Success: n%2 == 0, CumulativeGasUsed: uint64(n * 21000), Bloom: bloom, Logs: logs}
		encoded := scratch.EncodeReceipt(&r)
		oracle := scratchReceiptOracle(&r)
		if !bytes.Equal(encoded, oracle) {
			t.Fatal("independent receipt oracle")
		}
		retained = append(retained, encoded)
		want = append(want, oracle)
	}
	for i := range retained {
		if !bytes.Equal(retained[i], want[i]) {
			t.Fatal("receipt encoding alias")
		}
	}
}

func TestEncodingScratchIndependentOwners(t *testing.T) {
	for worker := 0; worker < 4; worker++ {
		t.Run(string(rune('A'+worker)), func(t *testing.T) {
			t.Parallel()
			scratch := types.NewEncodingScratch()
			for n := 0; n < 64; n++ {
				tx := types.Transaction{Data: &types.DynamicFeeTx{ChainID: uint64(worker + 1), Nonce: uint64(n), Data: bytes.Repeat([]byte{byte(worker), byte(n)}, n)}}
				sender := primitives.Address{0: byte(worker), 19: byte(n)}
				want := tx.UniqueID(sender)
				if scratch.UniqueID(tx, sender) != want {
					t.Fatal("independent scratch owner contaminated")
				}
			}
		})
	}
}
