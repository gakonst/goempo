package rpc

import (
	"encoding/json"

	"github.com/kapetan-io/tiger/assert"
)

type argKind uint8

const (
	argHash argKind = iota
	argAddress
	argBlockID
	argBlockNumber
	argU64
	argQuantity
	argString
	argBytes
	argBool
)

type argCheck struct {
	name string
	kind argKind
}

func checkArg(raw json.RawMessage, kind argKind) error {
	var err error
	switch kind {
	case argHash:
		_, err = hashParam(raw)
	case argAddress:
		_, err = addressParam(raw)
	case argBlockID:
		_, err = blockIDParam(raw)
	case argBlockNumber:
		_, err = blockNumberParam(raw)
	case argU64:
		_, err = u64Param(raw)
	case argQuantity:
		_, err = quantityParam(raw)
	case argString:
		_, err = stringParam(raw, "a string")
	case argBytes:
		_, err = bytesParam(raw)
	case argBool:
		_, err = boolParam(raw)
	default:
		assert.Unreachable("argKind: unhandled value")
	}
	return err
}

func typedThen(result []byte, args ...argCheck) handler {
	return func(_ *Server, p *params) ([]byte, error) {
		for _, a := range args {
			raw, err := p.required(a.name)
			if err != nil {
				return nil, err
			}
			if err := checkArg(raw, a.kind); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
}

func addDebugStubs(t map[string]handler) {
	t["debug_accountRange"] = typedThen(resultNull,
		argCheck{"block_number", argBlockNumber}, argCheck{"start", argBytes}, argCheck{"max_results", argU64},
		argCheck{"nocode", argBool}, argCheck{"nostorage", argBool}, argCheck{"incompletes", argBool})
	t["debug_chaindbCompact"] = typedThen(resultNull)
	t["debug_chaindbProperty"] = typedThen(resultNull, argCheck{"property", argString})
	t["debug_dumpBlock"] = typedThen(resultNull, argCheck{"number", argBlockID})
	t["debug_freeOSMemory"] = typedThen(resultNull)
	t["debug_gcStats"] = typedThen(resultNull)
	t["debug_memStats"] = typedThen(resultNull)
	t["debug_getAccessibleState"] = typedThen(resultNull, argCheck{"from", argBlockNumber}, argCheck{"to", argBlockNumber})
	t["debug_getModifiedAccountsByHash"] = typedThen(resultNull, argCheck{"start_hash", argHash}, argCheck{"end_hash", argHash})
	t["debug_getModifiedAccountsByNumber"] = typedThen(resultNull, argCheck{"start_number", argU64}, argCheck{"end_number", argU64})
	t["debug_preimage"] = typedThen(resultNull, argCheck{"hash", argHash})
	t["debug_printBlock"] = typedThen(resultNull, argCheck{"number", argU64})
	t["debug_setHead"] = typedThen(resultNull, argCheck{"number", argQuantity})
	t["debug_setTrieFlushInterval"] = typedThen(resultNull, argCheck{"interval", argString})
	t["debug_storageRangeAt"] = typedThen(resultNull,
		argCheck{"block_hash", argHash}, argCheck{"tx_idx", argU64}, argCheck{"contract_address", argAddress},
		argCheck{"key_start", argHash}, argCheck{"max_result", argU64})
	t["debug_getBadBlocks"] = constant(resultEmpty)
	t["debug_seedHash"] = debugSeedHash
	t["debug_traceBadBlock"] = debugTraceBadBlock
	t["debug_getRawTransactions"] = debugGetRawTransactions
	t["rpc_modules"] = rpcModules
	t["debug_codeByHash"] = debugCodeByHash
	t["eth_getMultiProof"] = ethGetMultiProof
	t["trace_transactionOpcodeGas"] = traceTransactionOpcodeGas
	t["trace_blockOpcodeGas"] = traceBlockOpcodeGas
}
