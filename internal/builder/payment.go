package builder

import (
	"github.com/gakonst/goempo/internal/crypto/keccak"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/rlp"
	"github.com/gakonst/goempo/internal/tempo/chain"
	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/types"
)

const (
	word                      = 32
	selectorLen               = 4
	paymentTrailingV3         = 32
	paymentCalldataMax        = 2048
	keyAuthorizationRLPMax    = 1024
	descriptorWords           = 7
	addressPadding            = word - primitives.AddressLength
	uint96Padding             = word - 12
	channelReserveAddressByte = 0x4d
)

const _ = uint(paymentCalldataMax - keyAuthorizationRLPMax)

var (
	tip20Prefix           = [12]byte{0x20, 0xc0}
	channelReserveAddress = primitives.Address{channelReserveAddressByte, 0x50, 0x50}
)

type staticCall struct {
	selector [selectorLen]byte
	words    int
}

type dynamicCall struct {
	selector [selectorLen]byte
	uint96s  int
}

const descriptor = "(address,address,address,address,bytes32,address,bytes32)"

var (
	tip20Payments = []staticCall{
		static("transfer(address,uint256)", 2),
		static("transferWithMemo(address,uint256,bytes32)", 3),
		static("mint(address,uint256)", 2),
		static("mintWithMemo(address,uint256,bytes32)", 3),
		static("transferFrom(address,address,uint256)", 3),
		static("transferFromWithMemo(address,address,uint256,bytes32)", 4),
		static("approve(address,uint256)", 2),
		static("burn(uint256)", 1),
		static("burnWithMemo(uint256,bytes32)", 2),
	}
	reservePayments = []staticCall{
		static("open(address,address,address,uint96,bytes32,address)", 6),
		static("topUp("+descriptor+",uint96)", descriptorWords+1),
		static("requestClose("+descriptor+")", descriptorWords),
		static("withdraw("+descriptor+")", descriptorWords),
	}
	reserveSigned = []dynamicCall{
		{selector: selectorOf("close(" + descriptor + ",uint96,uint96,bytes)"), uint96s: 2},
		{selector: selectorOf("settle(" + descriptor + ",uint96,bytes)"), uint96s: 1},
	}
)

func selectorOf(sig string) [selectorLen]byte {
	h := keccak.Sum256([]byte(sig))
	return [selectorLen]byte(h[:selectorLen])
}

func static(sig string, words int) staticCall {
	return staticCall{selector: selectorOf(sig), words: words}
}

func isTIP20(a *primitives.Address) bool {
	return a != nil && [12]byte(a[:12]) == tip20Prefix
}

func matchStatic(calls []staticCall, input []byte, trailing int) bool {
	if len(input) < selectorLen {
		return false
	}
	sel := [selectorLen]byte(input[:selectorLen])
	for i := range calls {
		if calls[i].selector != sel {
			continue
		}
		size := selectorLen + calls[i].words*word
		return len(input) >= size && len(input)-size <= trailing
	}
	return false
}

func zeroPrefix(b []byte, n int) bool {
	for i := 0; i < n; i++ {
		if b[i] != 0 {
			return false
		}
	}
	return true
}

func wordAt(input []byte, index int) []byte {
	start := selectorLen + index*word
	return input[start : start+word]
}

func canonicalSigned(input []byte, call *dynamicCall) ([]byte, bool) {
	if len(input) > paymentCalldataMax || len(input) < selectorLen || [selectorLen]byte(input[:selectorLen]) != call.selector {
		return nil, false
	}
	head := descriptorWords + call.uint96s + 1
	if len(input) < selectorLen+(head+1)*word {
		return nil, false
	}
	for i := 0; i < descriptorWords; i++ {
		if i == 4 || i == 6 {
			continue
		}
		if !zeroPrefix(wordAt(input, i), addressPadding) {
			return nil, false
		}
	}
	for i := 0; i < call.uint96s; i++ {
		if !zeroPrefix(wordAt(input, descriptorWords+i), uint96Padding) {
			return nil, false
		}
	}
	offset := wordAt(input, head-1)
	if !zeroPrefix(offset, word-8) || beUint64(offset[word-8:]) != uint64(head*word) {
		return nil, false
	}
	length := wordAt(input, head)
	if !zeroPrefix(length, word-8) {
		return nil, false
	}
	n := beUint64(length[word-8:])
	padded := (n + word - 1) / word * word
	body := uint64(len(input) - selectorLen - (head+1)*word)
	if n > uint64(paymentCalldataMax) || body != padded {
		return nil, false
	}
	data := input[selectorLen+(head+1)*word:]
	for _, c := range data[n:] {
		if c != 0 {
			return nil, false
		}
	}
	return data[:n], true
}

func beUint64(b []byte) uint64 {
	var v uint64
	for i := 0; i < len(b); i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func isReserveSigned(input []byte) bool {
	for i := range reserveSigned {
		sig, ok := canonicalSigned(input, &reserveSigned[i])
		if !ok {
			continue
		}
		_, err := types.ParsePrimitiveSignature(sig)
		return err == nil
	}
	return false
}

func isPaymentCall(to *primitives.Address, input []byte, trailing int) bool {
	switch {
	case to == nil:
		return false
	case isTIP20(to):
		return matchStatic(tip20Payments, input, trailing)
	case *to == channelReserveAddress:
		return matchStatic(reservePayments, input, trailing) || isReserveSigned(input)
	}
	return false
}

func IsPayment(tx *types.Transaction, rules chain.Rules) bool {
	switch {
	case rules.AtLeast(hardfork.T15):
		return isPaymentTIP1045(tx, paymentTrailingV3)
	case rules.AtLeast(hardfork.T5):
		return isPaymentTIP1045(tx, 0)
	}
	return isPaymentV1(tx)
}

func isPaymentV1(tx *types.Transaction) bool {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		return isTIP20(d.To)
	case *types.AccessListTx:
		return isTIP20(d.To)
	case *types.DynamicFeeTx:
		return isTIP20(d.To)
	case *types.SetCodeTx:
		return isTIP20(d.To)
	case *types.TempoTx:
		for i := range d.Calls {
			if !isTIP20(d.Calls[i].To) {
				return false
			}
		}
		return true
	}
	return false
}

func isPaymentTIP1045(tx *types.Transaction, trailing int) bool {
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		return isPaymentCall(d.To, d.Data, trailing)
	case *types.AccessListTx:
		return len(d.AccessList) == 0 && isPaymentCall(d.To, d.Data, trailing)
	case *types.DynamicFeeTx:
		return len(d.AccessList) == 0 && isPaymentCall(d.To, d.Data, trailing)
	case *types.SetCodeTx:
		return len(d.AccessList) == 0 && len(d.AuthorizationList) == 0 && isPaymentCall(d.To, d.Data, trailing)
	case *types.TempoTx:
		return isPaymentAA(d, trailing)
	}
	return false
}

func isPaymentAA(d *types.TempoTx, trailing int) bool {
	if len(d.Calls) == 0 || len(d.AccessList) != 0 || len(d.AuthorizationList) != 0 {
		return false
	}
	if d.KeyAuthorization != nil && keyAuthorizationLen(d.KeyAuthorization) > keyAuthorizationRLPMax {
		return false
	}
	for i := range d.Calls {
		if !isPaymentCall(d.Calls[i].To, d.Calls[i].Input, trailing) {
			return false
		}
	}
	return true
}

func keyAuthorizationLen(k *types.SignedKeyAuthorization) int {
	return len(rlp.EncodeList(k.Authorization.Encode(), rlp.EncodeBytes(k.Signature.AppendBytes(nil))))
}
