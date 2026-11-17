package executor

import (
	"errors"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

var (
	ErrTxKind      = errors.New("executor: unknown transaction type")
	ErrValue       = errors.New("executor: native value transfer not allowed")
	ErrNonce       = errors.New("executor: nonce mismatch")
	ErrIntrinsic   = errors.New("executor: gas limit below intrinsic gas")
	ErrGasCap      = errors.New("executor: gas limit above the transaction cap")
	ErrFeeCap      = errors.New("executor: max fee below base fee")
	ErrValidWindow = errors.New("executor: transaction outside its validity window")
	ErrBlockGas    = errors.New("executor: block gas limit exceeded")
	ErrFeeToken    = errors.New("executor: fee token is not a USD TIP-20")
	ErrFeeBalance  = errors.New("executor: fee payer balance below the maximum fee")
)

type call struct {
	to    *Address
	value u256.Int
	input []byte
}

type authorization struct {
	chainID u256.Int
	address Address
	nonce   uint64
	signer  Address
	valid   bool
}

type txView struct {
	kind        types.TxType
	sender      Address
	feePayer    Address
	gasLimit    uint64
	gasPrice    u256.Int
	maxFee      u256.Int
	priority    u256.Int
	dynamicFee  bool
	nonce       uint64
	nonceKey    u256.Int
	calls       []call
	accessList  types.AccessList
	feeToken    *Address
	auths       []authorization
	aa          *types.TempoTx
	validBefore uint64
	validAfter  uint64
}

func (v *txView) isAA() bool { return v.aa != nil }

func (v *txView) isCreate() bool {
	return len(v.calls) > 0 && v.calls[0].to == nil
}

func viewOf(tx *types.Transaction) (txView, error) {
	sender, err := tx.Sender()
	if err != nil {
		return txView{}, err
	}
	v := txView{kind: tx.Type(), sender: sender, feePayer: sender}
	switch d := tx.Data.(type) {
	case *types.LegacyTx:
		v.legacy(legacyFields{nonce: d.Nonce, gas: d.Gas, price: d.GasPrice})
		v.calls = []call{{to: d.To, value: d.Value, input: d.Data}}
	case *types.AccessListTx:
		v.legacy(legacyFields{nonce: d.Nonce, gas: d.Gas, price: d.GasPrice})
		v.calls = []call{{to: d.To, value: d.Value, input: d.Data}}
		v.accessList = d.AccessList
	case *types.DynamicFeeTx:
		v.dynamic(d)
	case *types.SetCodeTx:
		v.dynamic(&d.DynamicFeeTx)
		v.auths = ethAuthorizations(d.AuthorizationList)
	case *types.TempoTx:
		return v, v.tempo(d)
	default:
		return txView{}, ErrTxKind
	}
	return v, nil
}

type legacyFields struct {
	nonce uint64
	gas   uint64
	price u256.Int
}

func (v *txView) legacy(f legacyFields) {
	v.nonce = f.nonce
	v.gasLimit = f.gas
	v.gasPrice = f.price
	v.maxFee = f.price
	v.priority = f.price
}

func (v *txView) dynamic(d *types.DynamicFeeTx) {
	v.nonce = d.Nonce
	v.gasLimit = d.Gas
	v.maxFee = d.MaxFeePerGas
	v.priority = d.MaxPriorityFeePerGas
	v.dynamicFee = true
	v.calls = []call{{to: d.To, value: d.Value, input: d.Data}}
	v.accessList = d.AccessList
}

func (v *txView) tempo(d *types.TempoTx) error {
	v.aa = d
	v.nonce = d.Nonce
	v.nonceKey = d.NonceKey
	v.gasLimit = d.GasLimit
	v.maxFee = d.MaxFeePerGas
	v.priority = d.MaxPriorityFeePerGas
	v.dynamicFee = true
	v.accessList = d.AccessList
	v.feeToken = d.FeeToken
	v.validBefore = d.ValidBefore
	v.validAfter = d.ValidAfter
	v.calls = make([]call, len(d.Calls))
	for i := 0; i < len(d.Calls); i++ {
		v.calls[i] = call{to: d.Calls[i].To, value: d.Calls[i].Value, input: d.Calls[i].Input}
	}
	v.auths = make([]authorization, len(d.AuthorizationList))
	for i := 0; i < len(d.AuthorizationList); i++ {
		a := &d.AuthorizationList[i]
		signer, err := a.Authority()
		v.auths[i] = authorization{chainID: a.ChainID, address: a.Address, nonce: a.Nonce, signer: signer, valid: err == nil}
	}
	if d.FeePayerSignature == nil {
		return nil
	}
	payer, err := d.FeePayer(v.sender)
	if err != nil {
		return err
	}
	v.feePayer = payer
	return nil
}

func ethAuthorizations(list []types.SetCodeAuthorization) []authorization {
	out := make([]authorization, len(list))
	for i := 0; i < len(list); i++ {
		a := &list[i]
		signer, err := a.Authority()
		out[i] = authorization{chainID: a.ChainID, address: a.Address, nonce: a.Nonce, signer: signer, valid: err == nil}
	}
	return out
}

func (v *txView) effectiveGasPrice(baseFee u256.Int) u256.Int {
	if !v.dynamicFee {
		return v.gasPrice
	}
	tip := baseFee.Add(v.priority)
	if tip.Lt(v.maxFee) {
		return tip
	}
	return v.maxFee
}

func (v *txView) target() primitives.Address {
	if len(v.calls) == 0 || v.calls[0].to == nil {
		return primitives.Address{}
	}
	return *v.calls[0].to
}
