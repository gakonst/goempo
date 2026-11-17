# Goal: reach zempo's frontier, in Go

goempo climbs a ladder. Each rung has a number that only goes up, measured by `go run ./cmd/goempo-dev score` against the reference node's public RPC (`https://rpc.tempo.xyz`, mainnet; `https://rpc.moderato.tempo.xyz`, testnet). A rung counts only when `scripts/verify.sh` passes on the same commit.

| Rung | Name | Score (higher is better) | Frontier (zempo) |
|---|---|---|---|
| R0 | Gate | `scripts/verify.sh` passes, `tiger check` clean | yes |
| R1 | Decode | mainnet blocks decoded from `debug_getRawBlock`, re-encoded byte for byte, block hash, transactions root and every sender recovered, sampled across every fork T0..T13 | all |
| R2 | Genesis | mainnet genesis alloc loaded, state root of block 0 matches | yes |
| R3 | Replay | highest mainnet block N such that executing 1..N from genesis matches every block's gas used, receipts root, logs bloom and state root | 48,557,098 |
| R4 | Follow | follows the mainnet tip from peers (devp2p) and the consensus feed, and keeps up for 1 hour | yes, in production |
| R5 | RPC | share of the reference follower's RPC methods that answer the same as the reference on a recorded corpus | 100% |
| R6 | Validator | Simplex consensus and DKG with reference validators on a private network: blocks finalized with goempo proposing and voting | yes |
| R7 | Speed | mainnet blocks executed with state root per second, relative to the reference on the same host | 3-4x |

The headline number is R3's height. The order of work is the ladder's order: a rung above an unmet one does not count, except that R7 work starts only after R3 reaches the tip.

## The specification

goempo implements what the reference node does. Where it is unclear, these say it, in order of trust:

1. The reference node's behavior on the network (RPC results, `debug_traceBlockByNumber`).
2. The reference's code: `tempoxyz/tempo` (public), with reth and revm.
3. zempo's specs (`tempoxyz/zempo`, `docs/*-SPEC.md`, `docs/DEVIATIONS-REFERENCE.md`): a tested reading of the reference. Read them for where to look, never copy its code.

Every difference from the reference goes in docs/DEVIATIONS.md.
