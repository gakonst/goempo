package statetest

import (
	"errors"
	"math/big"

	"github.com/gakonst/goempo/internal/evm"
	"github.com/gakonst/goempo/internal/evm/memstate"
	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/u256"
)

const (
	txGas             = 21000
	txDataZeroGas     = 4
	txDataNonZeroGas  = 16
	accessAddressGas  = 2400
	accessSlotGas     = 1900
	authBaseGas       = 25000
	authRefund        = 25000 - 12500
	floorTokenGas     = 10
	blobGasPerBlob    = 1 << 17
	osakaTxGasCap     = 1 << 24
	osakaBlobsPerTx   = 6
	blobVersionByte   = 0x01
	fakeExpIterations = 1 << 16
)

const _ = uint64(osakaTxGasCap - txGas)

const _ = uint64(fakeExpIterations - osakaBlobsPerTx)

var (
	errNonce          = errors.New("nonce mismatch")
	errNonceMax       = errors.New("nonce at max")
	errSenderCode     = errors.New("sender not an EOA")
	errFeeCap         = errors.New("max fee below base fee")
	errTip            = errors.New("priority fee above max fee")
	errIntrinsic      = errors.New("intrinsic gas too low")
	errFunds          = errors.New("insufficient funds")
	errGasLimit       = errors.New("gas limit above block gas limit")
	errGasCap         = errors.New("gas limit above tx cap")
	errInitCode       = errors.New("initcode too large")
	errBlob           = errors.New("invalid blob transaction")
	errBlobFee        = errors.New("blob fee cap too low")
	errAuth           = errors.New("invalid set code transaction")
	errTxType         = errors.New("tx type not enabled")
	errNoSigner       = errors.New("authorization has no signer")
	errFloor          = errors.New("gas limit below calldata floor")
	errBalanceOverflw = errors.New("balance computation overflows")
	errFakeExp        = errors.New("blob base fee does not converge")
)

type accessTuple struct {
	address primitives.Address
	keys    []primitives.Hash
}

type authorization struct {
	chainID u256.Int
	address primitives.Address
	nonce   uint64
	yParity u256.Int
	s       u256.Int
	signer  primitives.Address
	signed  bool
}

type txType uint8

const (
	txLegacy txType = iota
	txAccessList
	txDynamic
	txBlob
	txSetCode
)

type transaction struct {
	kind         txType
	nonce        uint64
	gasLimit     uint64
	gasPrice     u256.Int
	maxFee       u256.Int
	maxTip       u256.Int
	maxBlobFee   u256.Int
	to           primitives.Address
	create       bool
	value        u256.Int
	data         []byte
	accessList   []accessTuple
	auths        []authorization
	blobHashes   []primitives.Hash
	sender       primitives.Address
	hasBlobField bool
}

type chainRules struct {
	fork           evm.Fork
	config         *evm.Config
	block          *evm.BlockContext
	blobBaseFee    u256.Int
	maxBlobsPerTx  int
	blobFeeEnabled bool
}

type txOutcome struct {
	gasUsed uint64
	logs    []evm.Log
	status  evm.Status
}

type intrinsicGas struct {
	gas   uint64
	floor uint64
}

func intrinsic(tx *transaction, rules *chainRules) intrinsicGas {
	gas := uint64(txGas)
	if tx.create {
		gas += rules.config.Gas.Create
		gas += (uint64(len(tx.data)) + 31) / 32 * rules.config.Gas.InitCodeWord
	}
	zeros := uint64(0)
	for i := 0; i < len(tx.data); i++ {
		if tx.data[i] == 0 {
			zeros++
		}
	}
	nonZeros := uint64(len(tx.data)) - zeros
	gas += zeros*txDataZeroGas + nonZeros*txDataNonZeroGas
	for i := 0; i < len(tx.accessList); i++ {
		gas += accessAddressGas + uint64(len(tx.accessList[i].keys))*accessSlotGas
	}
	gas += uint64(len(tx.auths)) * authBaseGas
	out := intrinsicGas{gas: gas}
	if rules.fork >= evm.Prague {
		out.floor = txGas + floorTokenGas*(zeros+4*nonZeros)
	}
	return out
}

func effectiveGasPrice(tx *transaction, rules *chainRules) u256.Int {
	if tx.kind == txLegacy || tx.kind == txAccessList {
		return tx.gasPrice
	}
	tip := rules.block.BaseFee.Add(tx.maxTip)
	if tip.Lt(tx.maxFee) {
		return tip
	}
	return tx.maxFee
}

func validateFees(tx *transaction, rules *chainRules) error {
	if rules.fork < evm.London {
		if tx.kind >= txDynamic {
			return errTxType
		}
		return nil
	}
	if tx.kind == txLegacy || tx.kind == txAccessList {
		if tx.gasPrice.Lt(rules.block.BaseFee) {
			return errFeeCap
		}
		return nil
	}
	if tx.maxTip.Gt(tx.maxFee) {
		return errTip
	}
	if tx.maxFee.Lt(rules.block.BaseFee) {
		return errFeeCap
	}
	return nil
}

func validateBlob(tx *transaction, rules *chainRules) error {
	if tx.kind != txBlob {
		return nil
	}
	if rules.fork < evm.Cancun {
		return errTxType
	}
	if tx.create {
		return errBlob
	}
	if len(tx.blobHashes) == 0 {
		return errBlob
	}
	if len(tx.blobHashes) > rules.maxBlobsPerTx {
		return errBlob
	}
	for i := 0; i < len(tx.blobHashes); i++ {
		if tx.blobHashes[i][0] != blobVersionByte {
			return errBlob
		}
	}
	if tx.maxBlobFee.Lt(rules.blobBaseFee) {
		return errBlobFee
	}
	return nil
}

func validateSetCode(tx *transaction, rules *chainRules) error {
	if tx.kind != txSetCode {
		return nil
	}
	if rules.fork < evm.Prague {
		return errTxType
	}
	if tx.create {
		return errAuth
	}
	if len(tx.auths) == 0 {
		return errAuth
	}
	return nil
}

func validateGas(tx *transaction, rules *chainRules) error {
	if rules.fork >= evm.Osaka && tx.gasLimit > osakaTxGasCap {
		return errGasCap
	}
	if tx.gasLimit > rules.block.GasLimit {
		return errGasLimit
	}
	if tx.create && rules.config.Gas.MaxInitCodeSize > 0 && len(tx.data) > rules.config.Gas.MaxInitCodeSize {
		return errInitCode
	}
	need := intrinsic(tx, rules)
	if need.gas > tx.gasLimit {
		return errIntrinsic
	}
	if need.floor > tx.gasLimit {
		return errFloor
	}
	return nil
}

func validateSender(st *memstate.State, tx *transaction) error {
	if tx.nonce == ^uint64(0) {
		return errNonceMax
	}
	if st.Nonce(tx.sender) != tx.nonce {
		return errNonce
	}
	code := st.Code(tx.sender)
	if len(code) == 0 {
		return nil
	}
	_, delegated := evm.DelegationTarget(code)
	if !delegated {
		return errSenderCode
	}
	return nil
}

func validateFunds(st *memstate.State, tx *transaction, rules *chainRules) error {
	price := tx.gasPrice
	if tx.kind >= txDynamic {
		price = tx.maxFee
	}
	cost := u256.FromUint64(tx.gasLimit).MulFull(price)
	if !cost.High().IsZero() {
		return errBalanceOverflw
	}
	total, overflow := cost.Low().AddOverflow(tx.value)
	if overflow {
		return errBalanceOverflw
	}
	if tx.kind == txBlob {
		blobCost := u256.FromUint64(uint64(len(tx.blobHashes)) * blobGasPerBlob).MulFull(tx.maxBlobFee)
		if !blobCost.High().IsZero() {
			return errBalanceOverflw
		}
		total, overflow = total.AddOverflow(blobCost.Low())
		if overflow {
			return errBalanceOverflw
		}
	}
	if st.Balance(tx.sender).Lt(total) {
		return errFunds
	}
	return nil
}

func validate(st *memstate.State, tx *transaction, rules *chainRules) error {
	if tx.kind == txAccessList && rules.fork < evm.Berlin {
		return errTxType
	}
	checks := [...]error{
		validateGas(tx, rules),
		validateSender(st, tx),
		validateFees(tx, rules),
		validateBlob(tx, rules),
		validateSetCode(tx, rules),
	}
	for i := 0; i < len(checks); i++ {
		if checks[i] != nil {
			return checks[i]
		}
	}
	return validateFunds(st, tx, rules)
}

func apply(st *memstate.State, tx *transaction, rules *chainRules) (txOutcome, error) {
	err := validate(st, tx, rules)
	if err != nil {
		return txOutcome{}, err
	}
	price := effectiveGasPrice(tx, rules)
	st.SubBalance(tx.sender, u256.FromUint64(tx.gasLimit).Mul(price))
	if tx.kind == txBlob {
		blobGas := u256.FromUint64(uint64(len(tx.blobHashes)) * blobGasPerBlob)
		st.SubBalance(tx.sender, blobGas.Mul(rules.blobBaseFee))
	}
	if !tx.create {
		st.SetNonce(tx.sender, tx.nonce+1)
	}
	warm(st, tx, rules)
	applyAuthorizations(st, tx, rules)
	need := intrinsic(tx, rules)
	machine := evm.New(evm.Context{
		State:  st,
		Block:  rules.block,
		Tx:     &evm.TxContext{Origin: tx.sender, GasPrice: price, BlobHashes: tx.blobHashes},
		Config: rules.config,
	})
	kind := evm.KindCall
	if tx.create {
		kind = evm.KindCreate
	}
	res := machine.Execute(evm.Message{
		Kind:   kind,
		Caller: tx.sender,
		To:     tx.to,
		Value:  tx.value,
		Input:  tx.data,
		Gas:    tx.gasLimit - need.gas,
	})
	gasUsed := tx.gasLimit - res.GasLeft
	refund := st.Refund()
	if refund < 0 {
		refund = 0
	}
	gasUsed -= min(uint64(refund), gasUsed/rules.config.Gas.MaxRefundQuotient)
	gasUsed = max(gasUsed, need.floor)
	st.AddBalance(tx.sender, u256.FromUint64(tx.gasLimit-gasUsed).Mul(price))
	tip := price
	if rules.fork >= evm.London {
		tip = price.Sub(rules.block.BaseFee)
	}
	st.AddBalance(rules.block.Coinbase, u256.FromUint64(gasUsed).Mul(tip))
	logs := st.Logs()
	st.FinalizeTx()
	return txOutcome{gasUsed: gasUsed, logs: logs, status: res.Status}, nil
}

func warm(st *memstate.State, tx *transaction, rules *chainRules) {
	st.WarmAddress(tx.sender)
	if !tx.create {
		st.WarmAddress(tx.to)
	}
	precompiles := evm.EthereumPrecompiles(rules.fork).Addresses()
	for i := 0; i < len(precompiles); i++ {
		st.WarmAddress(precompiles[i])
	}
	if rules.fork >= evm.Shanghai {
		st.WarmAddress(rules.block.Coinbase)
	}
	for i := 0; i < len(tx.accessList); i++ {
		t := tx.accessList[i]
		st.WarmAddress(t.address)
		for j := 0; j < len(t.keys); j++ {
			st.WarmSlot(t.address, t.keys[j])
		}
	}
}

var secp256k1HalfN = u256.Int{0xdfe92f46681b20a0, 0x5d576e7357a4501d, 0xffffffffffffffff, 0x7fffffffffffffff}

func applyAuthorizations(st *memstate.State, tx *transaction, rules *chainRules) {
	for i := 0; i < len(tx.auths); i++ {
		a := tx.auths[i]
		if !a.chainID.IsZero() && a.chainID != rules.config.ChainID {
			continue
		}
		if a.nonce == ^uint64(0) {
			continue
		}
		if a.yParity.Gt(u256.One()) || a.s.Gt(secp256k1HalfN) || !a.signed {
			continue
		}
		st.WarmAddress(a.signer)
		code := st.Code(a.signer)
		_, delegated := evm.DelegationTarget(code)
		if len(code) != 0 && !delegated {
			continue
		}
		if st.Nonce(a.signer) != a.nonce {
			continue
		}
		if st.Exist(a.signer) {
			st.AddRefund(authRefund)
		}
		if a.address.IsZero() {
			st.SetCode(a.signer, nil)
		} else {
			st.SetCode(a.signer, evm.DelegationCode(a.address))
		}
		st.SetNonce(a.signer, a.nonce+1)
	}
}

type expInput struct {
	factor      uint64
	numerator   uint64
	denominator uint64
}

func fakeExponential(in expInput) (u256.Int, error) {
	f := new(big.Int).SetUint64(in.factor)
	n := new(big.Int).SetUint64(in.numerator)
	d := new(big.Int).SetUint64(in.denominator)
	output := new(big.Int)
	acc := new(big.Int).Mul(f, d)
	i := 1
	for ; i < fakeExpIterations && acc.Sign() > 0; i++ {
		output.Add(output, acc)
		acc.Mul(acc, n)
		acc.Div(acc, new(big.Int).Mul(d, big.NewInt(int64(i))))
	}
	if i == fakeExpIterations {
		return u256.Int{}, errFakeExp
	}
	output.Div(output, d)
	var buf [32]byte
	if output.BitLen() > 256 {
		return u256.Int{}, errFakeExp
	}
	output.FillBytes(buf[:])
	return u256.FromBytes32(buf), nil
}
