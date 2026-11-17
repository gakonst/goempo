Fixtures for internal/rpc tests, recorded from https://rpc.tempo.xyz (mainnet, chain 4217) on 2026-11-17.

- blocks.jsonl: mainnet blocks 10123550 and 11082794: raw RLP (debug_getRawBlock) and raw receipts (debug_getRawReceipts).
- corpus.jsonl.gz: the reference node's answers for every block, transaction, receipt, log and raw method over those two blocks, extracted from ~/cache/rpc/corpus.jsonl with scripts/rpc-testdata.py.
- envelope.jsonl: the reference node's JSON-RPC envelope, parameter and error answers that do not depend on chain contents.
- debugext.jsonl: the reference node's answers for debug_dbGet, debug_setGCPercent, debug_standardTrace*ToFile, token_*, consensus_subscribe, debug_executionWitness*, debug_intermediateRoots, debug_traceBlock and trace_rawTransaction param errors, eth_getMultiProof (param errors, proof window, missing block), debug_codeByHash errors, debug_* stub methods, rpc_modules, debug_seedHash, debug_getBadBlocks, debug_traceBadBlock and debug_getRawTransactions (blocks above plus not-found and missing-param cases), recorded from a private mainnet RPC node and https://rpc.tempo.xyz with scripts/rpc-record.py.
- chainconfig.json: debug_chainConfig and eth_config from a private mainnet RPC node (tempo/v1.18.4-cf561ef), recorded 2026-11-17. That build predates the T14 schedule goempo carries, so the test drops t14Time before comparing.
