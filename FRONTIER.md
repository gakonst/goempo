# Frontier

| Rung | Score | Commit | Date |
|---|---|---|---|
| R0 Gate | scaffold | - | 2026-11-17 |
| R1 Decode | 0 | - | - |
| R2 Genesis | yes | d1c1553 | 2026-11-17 |
| R3 Replay | 0 | - | - |
| R4 Follow | no | - | - |
| R5 RPC | 0% | - | - |
| R6 Validator | no | - | - |
| R7 Speed | - | - | - |

R2 passes (d1c1553, 2026-11-17): `go run ./cmd/goempo-dev genesis` computes the mainnet block 0 state root 0x83408974323f63ab969b23f0fe1dba30d7ee5dc5c524a975bae38187eaa2c7f6 from the embedded presto genesis, equal to eth_getBlockByNumber("0x0").stateRoot on https://rpc.tempo.xyz. The testnet (moderato, chain 42431) genesis also matches rpc.moderato.tempo.xyz: 0x3952ff0c60a3f215ab11a3a2c7698126b7911da84faf5cb66bdbcb0c605738c4.
