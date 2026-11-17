mainnet-statediff-3000-4999.jsonl.gz: Tempo mainnet blocks 3000-4999, one JSON line per block with the
header's stateRoot (eth_getBlockByNumber) and each transaction's state diff (debug_traceBlockByNumber with
prestateTracer, diffMode true), fetched from https://rpc.tempo.xyz. Blocks 1-3210 leave the genesis state unchanged.
