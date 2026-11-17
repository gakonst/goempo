package rpc

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/gakonst/goempo/internal/primitives"
	"github.com/gakonst/goempo/internal/types"
	"github.com/gakonst/goempo/internal/u256"
)

const _ = uint(simulateBlocksMax - simulateRevertCode)

const (
	simulateBlocksMax  = 256
	simulateRevertCode = 3
	simulateVMError    = -32015
)

type BlockOverrides struct {
	Number       *uint64
	Time         *uint64
	GasLimit     *uint64
	FeeRecipient *primitives.Address
	BaseFee      *u256.Int
	PrevRandao   *primitives.Hash
}

type AccountOverride struct {
	Balance   *u256.Int
	Nonce     *uint64
	Code      []byte
	HasCode   bool
	State     map[primitives.Hash]primitives.Hash
	StateDiff map[primitives.Hash]primitives.Hash
}

type SimBlock struct {
	Block BlockOverrides
	State map[primitives.Address]AccountOverride
	Calls []*CallRequest
}

type SimulateRequest struct {
	Blocks     []SimBlock
	Validation bool
	FullTx     bool
}

type SimCall struct {
	ReturnData []byte
	Logs       []types.Log
	GasUsed    uint64
	MaxUsedGas uint64
	Success    bool
	Reverted   bool
	Halt       string
}

type SimulatedBlock struct {
	Data  *BlockData
	Calls []SimCall
}

func NewSimulatedBlockData(blk *types.Block, receipts []types.Receipt, senders []primitives.Address) *BlockData {
	b := &BlockData{Hash: blk.Hash(), Block: blk, Raw: blk.Encode(), Receipts: receipts}
	b.once.Do(func() {
		b.senders = senders
		b.txHash = make([]primitives.Hash, len(blk.Transactions))
		for i, tx := range blk.Transactions {
			b.txHash[i] = tx.Hash()
		}
	})
	return b
}

type rawOverrides struct {
	Number        json.RawMessage `json:"number"`
	Time          json.RawMessage `json:"time"`
	GasLimit      json.RawMessage `json:"gasLimit"`
	FeeRecipient  json.RawMessage `json:"feeRecipient"`
	BaseFeePerGas json.RawMessage `json:"baseFeePerGas"`
	PrevRandao    json.RawMessage `json:"prevRandao"`
}

type rawAccount struct {
	Balance   json.RawMessage            `json:"balance"`
	Nonce     json.RawMessage            `json:"nonce"`
	Code      json.RawMessage            `json:"code"`
	State     map[string]json.RawMessage `json:"state"`
	StateDiff map[string]json.RawMessage `json:"stateDiff"`
}

type rawSimBlock struct {
	BlockOverrides *rawOverrides         `json:"blockOverrides"`
	StateOverrides map[string]rawAccount `json:"stateOverrides"`
	Calls          []json.RawMessage     `json:"calls"`
}

type rawSimulate struct {
	BlockStateCalls        []rawSimBlock `json:"blockStateCalls"`
	Validation             bool          `json:"validation"`
	ReturnFullTransactions bool          `json:"returnFullTransactions"`
}

func present(raw json.RawMessage) bool {
	return len(raw) > 0 && !isNull(raw)
}

func optQuantity(raw json.RawMessage) (*uint64, error) {
	if !present(raw) {
		return nil, nil
	}
	v, err := quantityParam(raw)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func optQuantity256(raw json.RawMessage) (*u256.Int, error) {
	if !present(raw) {
		return nil, nil
	}
	v, err := quantity256Param(raw)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func parseOverrides(r *rawOverrides) (BlockOverrides, error) {
	var o BlockOverrides
	if r == nil {
		return o, nil
	}
	var err error
	if o.Number, err = optQuantity(r.Number); err != nil {
		return o, err
	}
	if o.Time, err = optQuantity(r.Time); err != nil {
		return o, err
	}
	if o.GasLimit, err = optQuantity(r.GasLimit); err != nil {
		return o, err
	}
	if o.BaseFee, err = optQuantity256(r.BaseFeePerGas); err != nil {
		return o, err
	}
	if present(r.FeeRecipient) {
		a, err := addressParam(r.FeeRecipient)
		if err != nil {
			return o, err
		}
		o.FeeRecipient = &a
	}
	if present(r.PrevRandao) {
		h, err := hashParam(r.PrevRandao)
		if err != nil {
			return o, err
		}
		o.PrevRandao = &h
	}
	return o, nil
}

func parseSlots(m map[string]json.RawMessage) (map[primitives.Hash]primitives.Hash, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[primitives.Hash]primitives.Hash, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		v := m[k]
		key, err := hashParam(json.RawMessage(`"` + k + `"`))
		if err != nil {
			return nil, err
		}
		val, err := hashParam(v)
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func parseAccount(r rawAccount) (AccountOverride, error) {
	var o AccountOverride
	var err error
	if o.Balance, err = optQuantity256(r.Balance); err != nil {
		return o, err
	}
	if o.Nonce, err = optQuantity(r.Nonce); err != nil {
		return o, err
	}
	if present(r.Code) {
		if o.Code, err = bytesParam(r.Code); err != nil {
			return o, err
		}
		o.HasCode = true
	}
	if r.State != nil && r.StateDiff != nil {
		return o, invalidParams("both state and stateDiff set")
	}
	if o.State, err = parseSlots(r.State); err != nil {
		return o, err
	}
	if o.State == nil && r.State != nil {
		o.State = map[primitives.Hash]primitives.Hash{}
	}
	if o.StateDiff, err = parseSlots(r.StateDiff); err != nil {
		return o, err
	}
	return o, nil
}

func simulateParam(raw json.RawMessage) (*SimulateRequest, error) {
	raw = bytes.TrimSpace(raw)
	var r rawSimulate
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, invalidParams(err.Error() + column(raw))
	}
	if len(r.BlockStateCalls) > simulateBlocksMax {
		return nil, newError(-38026, "too many blocks")
	}
	req := &SimulateRequest{Validation: r.Validation, FullTx: r.ReturnFullTransactions, Blocks: make([]SimBlock, len(r.BlockStateCalls))}
	for i := range r.BlockStateCalls {
		rb := &r.BlockStateCalls[i]
		ov, err := parseOverrides(rb.BlockOverrides)
		if err != nil {
			return nil, err
		}
		sb := SimBlock{Block: ov}
		if rb.StateOverrides != nil {
			sb.State = make(map[primitives.Address]AccountOverride, len(rb.StateOverrides))
			for _, k := range slices.Sorted(maps.Keys(rb.StateOverrides)) {
				v := rb.StateOverrides[k]
				addr, err := addressParam(json.RawMessage(`"` + k + `"`))
				if err != nil {
					return nil, err
				}
				if sb.State[addr], err = parseAccount(v); err != nil {
					return nil, err
				}
			}
		}
		for _, c := range rb.Calls {
			cr, err := callParam(c)
			if err != nil {
				return nil, err
			}
			sb.Calls = append(sb.Calls, cr)
		}
		req.Blocks[i] = sb
	}
	return req, nil
}

func ethSimulateV1(s *Server, p *params) ([]byte, error) {
	raw, err := p.required("payload")
	if err != nil {
		return nil, err
	}
	req, err := simulateParam(raw)
	if err != nil {
		return nil, err
	}
	ref, err := blockIDParam(p.optional("block"))
	if err != nil {
		return nil, err
	}
	n, err := s.stateBlock(ref)
	if err != nil {
		return nil, err
	}
	if s.exec == nil {
		return nil, internalError("unimplemented")
	}
	blocks, err := s.exec.SimulateV1(n, req)
	if err != nil {
		return nil, executionError(err)
	}
	return encode(func(w *writer) {
		w.arrOpen()
		for i := range blocks {
			writeSimulatedBlock(w, &blocks[i], txDetail(req.FullTx))
		}
		w.arrClose()
	}), nil
}

func writeSimulatedBlock(w *writer, sb *SimulatedBlock, full txDetail) {
	w.objOpen()
	writeBlockFields(w, sb.Data, full)
	w.key("calls")
	w.arrOpen()
	var logIndex uint64
	for i := range sb.Calls {
		c := &sb.Calls[i]
		w.objOpen()
		if c.Success {
			w.kData("returnData", c.ReturnData)
		} else {
			w.kData("returnData", nil)
		}
		w.key("logs")
		w.arrOpen()
		for j := range c.Logs {
			writeLog(w, logPos{b: sb.Data, txIndex: i, logIndex: logIndex}, &c.Logs[j])
			logIndex++
		}
		w.arrClose()
		w.kQuantity("gasUsed", c.GasUsed)
		w.kQuantity("maxUsedGas", c.MaxUsedGas)
		status := uint64(0)
		if c.Success {
			status = 1
		}
		w.kQuantity("status", status)
		if !c.Success {
			writeSimError(w, c)
		}
		w.objClose()
	}
	w.arrClose()
	w.objClose()
}

func writeSimError(w *writer, c *SimCall) {
	w.key("error")
	w.objOpen()
	if c.Reverted {
		w.kStr("message", RevertError(c.ReturnData).Message)
		w.key("code")
		w.integer(simulateRevertCode)
		w.kData("data", c.ReturnData)
	} else {
		w.kStr("message", c.Halt)
		w.key("code")
		w.integer(simulateVMError)
	}
	w.objClose()
}
