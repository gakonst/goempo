# Mainnet replay fixtures

Each testdata/mainnet/*.json is one Tempo mainnet (chain 4217) transaction whose calls only touch precompiles, converted from https://rpc.tempo.xyz by ~/wt/precompiles-tools/convert.py:
eth_getTransactionByHash, eth_getTransactionReceipt, debug_traceTransaction with prestateTracer (pre state), prestateTracer diffMode (post state) and callTracer (per-call gas). The "source" field names the tx hash and block. File names are fork_txtype_selector[_sponsored]_block.

TestReplayMainnet loads the pre state, runs fee collection, the expiring nonce, keychain setup and each call through precompiles.Call, then checks every touched slot, every log and each 0x76 call frame's gas against mainnet.
