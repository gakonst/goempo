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
	keys        *signerKeys
	validBefore uint64
	validAfter  uint64
}

func (v *txView) isAA() bool { return v.aa != nil }

func (v *txView) keyID(tx *types.Transaction) (Address, error) {
	if v.keys != nil {
		return v.keys.keyID, nil
	}
	return v.aa.Signature.KeyID(tx.SigningHash())
}

func (v *txView) isCreate() bool {
	return len(v.calls) > 0 && v.calls[0].to == nil
}

type Sender struct {
	Address Address
	Err     error
	signers *signerKeys
}

type signerKeys struct {
	keyID    Address
	feePayer Address
	payerErr error
	hasPayer bool
}

func RecoverSender(tx *types.Transaction) Sender {
	if d, ok := tx.Data.(*types.TempoTx); ok {
		return recoverTempoSigners(tx, d, types.NewEncodingScratch())
	}
	addr, err := tx.Sender()
	return Sender{Address: addr, Err: err}
}

func RecoverSenders(txs []types.Transaction) []Sender {
	out := make([]Sender, len(txs))
	scratch := types.NewEncodingScratch()
	for i := range txs {
		tx := &txs[i]
		if IsSystemTx(tx) {
			continue
		}
		if d, ok := tx.Data.(*types.TempoTx); ok {
			out[i] = recoverTempoSigners(tx, d, scratch)
		} else {
			addr, err := tx.Sender()
			out[i] = Sender{Address: addr, Err: err}
		}
	}
	return out
}

func recoverTempoSigners(tx *types.Transaction, d *types.TempoTx, scratch *types.EncodingScratch) Sender {
	key, err := d.Signature.KeyID(scratch.SigningHash(*tx))
	if err != nil {
		return Sender{Err: err}
	}
	out := Sender{Address: key, signers: &signerKeys{keyID: key}}
	if d.Signature.Keychain != types.KeychainNone {
		out.Address = d.Signature.UserAddress
	}
	if d.FeePayerSignature != nil {
		out.signers.feePayer, out.signers.payerErr = d.FeePayer(out.Address)
		out.signers.hasPayer = true
	}
	return out
}

func viewOf(tx *types.Transaction, sim *Simulation, known *Sender) (txView, error) {
	var sender Address
	var keys *signerKeys
	switch {
	case sim != nil:
		sender = sim.Sender
	case known != nil:
		if known.Err != nil {
			return txView{}, known.Err
		}
		sender = known.Address
		keys = known.signers
	default:
		recovered, err := tx.Sender()
		if err != nil {
			return txView{}, err
		}
		sender = recovered
	}
	v := txView{kind: tx.Type(), sender: sender, feePayer: sender, keys: keys}
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
		err := v.tempo(d)
		if err != nil && sim != nil {
			v.feePayer = sender
			return v, nil
		}
		return v, err
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
	if v.keys != nil && v.keys.hasPayer {
		v.feePayer = v.keys.feePayer
		return v.keys.payerErr
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
