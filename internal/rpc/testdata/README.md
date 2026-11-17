Fixtures for internal/rpc tests, recorded from https://rpc.tempo.xyz (mainnet, chain 4217) on 2026-11-17.

- blocks.jsonl: mainnet blocks 10123550 and 11082794: raw RLP (debug_getRawBlock) and raw receipts (debug_getRawReceipts).
- corpus.jsonl.gz: the reference node's answers for every block, transaction, receipt, log and raw method over those two blocks, extracted from ~/cache/rpc/corpus.jsonl with scripts/rpc-testdata.py.
- envelope.jsonl: the reference node's JSON-RPC envelope, parameter and error answers that do not depend on chain contents.
- debugext.jsonl: the reference node's answers for debug_* stub methods, rpc_modules, debug_seedHash, debug_getBadBlocks, debug_traceBadBlock and debug_getRawTransactions (blocks above plus not-found and missing-param cases), recorded from the mainnet tailnet node http://100.98.157.16:8545 and https://rpc.tempo.xyz with scripts/rpc-record.py.
