# Frontier

| Rung | Score | Commit | Date |
|---|---|---|---|
| R0 Gate | scaffold | - | 2026-11-17 |
| R1 Decode | 100% (632/632 mainnet blocks) | a5aa2d9 | 2026-11-17 |
| R2 Genesis | yes | d1c1553 | 2026-11-17 |
| R3 Replay | 0 | - | - |
| R4 Follow | no | - | - |
| R5 RPC | 0% | - | - |
| R6 Validator | no | - | - |
| R7 Speed | - | - | - |

R2 passes (d1c1553, 2026-11-17): `go run ./cmd/goempo-dev genesis` computes the mainnet block 0 state root 0x83408974323f63ab969b23f0fe1dba30d7ee5dc5c524a975bae38187eaa2c7f6 from the embedded presto genesis, equal to eth_getBlockByNumber("0x0").stateRoot on https://rpc.tempo.xyz. The testnet (moderato, chain 42431) genesis also matches rpc.moderato.tempo.xyz: 0x3952ff0c60a3f215ab11a3a2c7698126b7911da84faf5cb66bdbcb0c605738c4.


R1 passes (a5aa2d9, 2026-11-17): `go run ./cmd/goempo-dev fetch` samples mainnet (https://rpc.tempo.xyz) across every fork window T0, T1/T1A, T1B, T1C, T2..T13 (24 evenly spaced blocks per window plus the 12 busiest of 400 probed; T14 is not active yet) into ~/cache/blocks/blocks.jsonl, and `go run ./cmd/goempo-dev score` decodes each block, re-encodes it byte for byte, and checks the block hash, the transactions root (internal/trie), every tx hash, every recovered sender against the RPC `from`, the receipts root and logs bloom rebuilt from eth_getBlockReceipts, the 0x76 fee payer against the receipt `feePayer`, keychain key IDs against the RPC `keyId`, and key-authorization signers against the sender. Mainnet: 632/632 blocks, 860 txs (legacy, system, 1559, 7702, 0x76 with secp256k1, P256, WebAuthn, keychain V2, sponsored, key authorization). Testnet (moderato, same sampling): 512/512 blocks, 31059 txs, adding keychain V1 and AA authorization lists.

R3 EVM statetests: 61708/61903 (99.68%) of execution-spec-tests v5.4.0 fixtures_develop state_tests for Istanbul..Osaka (Osaka 19452/19517 = 99.67%), run by go run ./cmd/goempo-dev statetest <dir> with internal/evm and an in-memory state; the only failures are the KZG point-evaluation precompile (0x0a), whose crypto has not landed. Pre-Istanbul fork cases (1653) are skipped.
