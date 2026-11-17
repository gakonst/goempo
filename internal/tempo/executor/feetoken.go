package executor

import (
	"encoding/binary"

	"github.com/gakonst/goempo/internal/tempo/hardfork"
	"github.com/gakonst/goempo/internal/tempo/precompiles"
)

const selectorBytes = 4

const (
	selSetUserToken       uint32 = 0xe7897444
	selTransfer           uint32 = 0xa9059cbb
	selTransferWithMemo   uint32 = 0x95777d59
	selDistributeReward   uint32 = 0x940a4e45
	selSwapExactAmountIn  uint32 = 0xf8856c0f
	selSwapExactAmountOut uint32 = 0xf0122b75
	abiWordBytes                 = 32
	abiAddressPad                = 12
)

func selector(input []byte) (uint32, bool) {
	if len(input) < selectorBytes {
		return 0, false
	}
	return binary.BigEndian.Uint32(input), true
}

func addressArg(input []byte, index int) (Address, bool) {
	off := selectorBytes + index*abiWordBytes
	if len(input) < off+abiWordBytes {
		return Address{}, false
	}
	word := input[off : off+abiWordBytes]
	for i := 0; i < abiAddressPad; i++ {
		if word[i] != 0 {
			return Address{}, false
		}
	}
	return Address(word[abiAddressPad:]), true
}

func (r *txRun) resolveFeeToken() (Address, error) {
	v := &r.view
	if v.feeToken != nil {
		return *v.feeToken, nil
	}
	if token, ok := r.setUserTokenInference(); ok {
		return token, nil
	}
	user, err := precompiles.UserToken(r.ctx, v.feePayer)
	if err != nil {
		return Address{}, err
	}
	if !user.IsZero() {
		return user, nil
	}
	token, err := r.tip20Inference()
	if err != nil {
		return Address{}, err
	}
	if !token.IsZero() {
		return token, nil
	}
	token, err = r.swapInference()
	if err != nil {
		return Address{}, err
	}
	if !token.IsZero() {
		return token, nil
	}
	return precompiles.PathUSDAddress, nil
}

func (r *txRun) setUserTokenInference() (Address, bool) {
	v := &r.view
	if v.isAA() || v.feePayer != v.sender || len(v.calls) == 0 {
		return Address{}, false
	}
	c := &v.calls[0]
	if c.to == nil || *c.to != precompiles.FeeManagerAddress {
		return Address{}, false
	}
	if sel, ok := selector(c.input); !ok || sel != selSetUserToken {
		return Address{}, false
	}
	return addressArg(c.input, 0)
}

func validToken(c *precompiles.Context, token Address) (Address, error) {
	ok, err := precompiles.IsValidFeeToken(c, token)
	if err != nil {
		return Address{}, err
	}
	if !ok {
		return Address{}, nil
	}
	return token, nil
}

func (r *txRun) tip20Inference() (Address, error) {
	v := &r.view
	if len(v.calls) == 0 || v.calls[0].to == nil {
		return Address{}, nil
	}
	if v.isAA() && v.feePayer != v.sender {
		return Address{}, nil
	}
	to := *v.calls[0].to
	for i := 0; i < len(v.calls); i++ {
		c := &v.calls[i]
		if c.to == nil || *c.to != to || !r.inferenceSelector(c.input) {
			return Address{}, nil
		}
	}
	return validToken(r.ctx, to)
}

func (r *txRun) inferenceSelector(input []byte) bool {
	sel, ok := selector(input)
	if !ok {
		return false
	}
	if sel == selTransfer || sel == selTransferWithMemo {
		return true
	}
	return sel == selDistributeReward && !r.env.Rules.AtLeast(hardfork.T7)
}

func (r *txRun) swapInference() (Address, error) {
	v := &r.view
	if len(v.calls) == 0 || v.calls[0].to == nil || *v.calls[0].to != precompiles.StablecoinDEXAddress {
		return Address{}, nil
	}
	if v.isAA() && len(v.calls) > 1 {
		return Address{}, nil
	}
	input := v.calls[0].input
	sel, ok := selector(input)
	if !ok {
		return Address{}, nil
	}
	if sel != selSwapExactAmountIn && sel != selSwapExactAmountOut {
		return Address{}, nil
	}
	tokenIn, ok := addressArg(input, 0)
	if !ok {
		return Address{}, nil
	}
	return validToken(r.ctx, tokenIn)
}
