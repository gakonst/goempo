# Mainnet replay fixtures

Each testdata/mainnet/*.json is one Tempo mainnet (chain 4217) transaction whose calls only touch precompiles, converted from https://rpc.tempo.xyz by ~/wt/precompiles-tools/convert.py:
eth_getTransactionByHash, eth_getTransactionReceipt, debug_traceTransaction with prestateTracer (pre state), prestateTracer diffMode (post state) and callTracer (per-call gas). The "source" field names the tx hash and block. File names are fork_txtype_selector[_sponsored]_block.

TestReplayMainnet loads the pre state, runs fee collection, the expiring nonce, keychain setup and each call through precompiles.Call, then checks every touched slot, every log and each 0x76 call frame's gas against mainnet.
- testdata/frames/*.json: precompile call frames (from callTracer) replayed statelessly for exact gas and output.
- testdata/committee/epoch1502.json: CurrentCommittee storage at mainnet block 32443199 via eth_getStorageAt.
- testdata/mainnet/t*_{2,76}_{f8856c0f,f0122b75,63813125,81649d06}*.json: stablecoin DEX transactions (swapExactAmountIn/Out, place, cancel) found by scanning 0xdec0 logs per fork window and keeping precompile-only ones (~/wt/tools-dex/dexscan.py, pick.py), converted with the same convert.py.
- mainnet/t10_2_zf_*: zone factory (0x5af2) createZone and transferOwnership txs found with eth_getLogs on 0x5af2 from T10 to block 49.4M (all three such mainnet txs), converted with precompiles-tools/convert.py.
- moderato/*: testnet (chain 42431) txs converted with precompiles-tools/convert_testnet.py (fetch cache in ~/cache/precompiles-moderato); replayed by TestReplayModerato in zone_factory_test.go. zf fixtures are zone factory createZone (3 sequencers, threshold 2) and transferOwnership.
