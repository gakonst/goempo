mainnet-0-40.blk: mainnet (chain 4217) blocks 0..40 from debug_getRawBlock on https://rpc.tempo.xyz, each as a 4-byte big-endian length and the raw block RLP.
mainnet-user-3211-3857.blk: the 12 mainnet blocks in 3211..3857 that carry user transactions (the rest of that range holds only the system transaction and leaves the state unchanged), same source and format.
intrinsic-mainnet.jsonl: one 0x76 transaction per kind from mainnet blocks 3211..5392, raw envelope from debug_getRawBlock and intrinsic gas = callTracer top-level gas minus the first call frame gas (debug_traceTransaction on a mainnet RPC node).
mainnet-12964402.blk: mainnet block 12,964,402 (T2), same source and format. Its tx 0 is a 0x76 batch whose third call targets pathUSD after the first call touched it (gasUsed 2,087,728 in eth_getTransactionReceipt).
mainnet-12964402-tx0-prestate.json: debug_traceTransaction prestateTracer result for that tx (0x4eb14452...6925) on a mainnet RPC node.
mainnet-16986086.blk: mainnet block 16,986,086 (T3), same source and format. Its tx 0 is a keychain-signed 0x76 tx; the T3 call-scope check reads the key's is_scoped slot cold (gasUsed 59,274).
mainnet-16986086-tx0-prestate.json: prestateTracer result for that tx (0x59d8f1cb...94e3) on a mainnet RPC node.
mainnet-18337286.blk: mainnet block 18,337,286 (T3), same source and format. Its tx 0 is a 0x76 CREATE under the expiring nonce key from a sender whose account nonce is 0 (pays new-account gas, gasUsed 959,243).
mainnet-18337286-tx0-prestate.json: prestateTracer result for that tx (0xd853dfa8...bbed) on a mainnet RPC node.
mainnet-29152422.blk, mainnet-29152422-tx0-prestate.json: mainnet block 29,152,422 (T7) and the prestateTracer result of its tx 0 (0xe1668a34...bbc0): a contract SSTORE from zero pays TIP-1060's 245,000 credit (gasUsed 302,225).
mainnet-29152424.blk, mainnet-29152424-tx1-prestate.json: mainnet block 29,152,424 (T7) and the prestateTracer result of its tx 1 (0xb5a71bd3...0c2e): an expiring-nonce transferWithMemo whose nonce ring write takes no storage credit (gasUsed 53,387).
