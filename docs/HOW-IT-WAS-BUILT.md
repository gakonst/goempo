# How goempo was built

goempo is a Tempo node written in Go. A swarm of AI agents wrote it in one day, 2026-11-17, the way tempoxyz/zempo (the Zig node) was built before it. This document is for a reader who wasn't there: what the agents were asked to reach, how progress was measured, how they split the work, how every claim was checked against mainnet, what mainnet caught, and what the code was not allowed to do.

The numbers here come from the repository and the local caches; `scripts/metrics.sh` prints them again for any commit, and [METRICS.md](METRICS.md) explains each one.

## In one paragraph

Georgios Konstantopoulos asked at 00:38 UTC for "a new tempo node similar to zempo but with Golang", built by hill climbing to the frontier. The first commit, a scaffold holding the rules and the ladder, landed at 00:41. Seven and a half hours and 203 commits later goempo had executed every mainnet block from genesis to 49,399,999 with its state on disk and no block ever patched from the reference, followed and executed the live tip with every finalization certificate verified, and answered all 111 recorded RPC methods (3,378 cases) exactly as the reference node does. It did that in about 62,000 lines of production Go and 12,000 lines of tests, using only the Go standard library, golang.org/x/crypto and the tiger analyzer: no go-ethereum, no uint256 library, no outside cryptography. The second phase, which started at 18:01 UTC, takes goempo from a simulated validator to one that votes and proposes beside the Rust tempo validators.

## The frontier ladder

The work was defined as a ladder of rungs ([GOAL.md](../GOAL.md)). Each rung has a number that only goes up and is measured against the reference node's public RPC, `https://rpc.tempo.xyz`. A score counts only on a commit where `scripts/verify.sh` passes. zempo had already reached the top of each rung, so its scores were the "frontier": proof that each rung could be climbed and a target to reach.

| Rung | Name | What the score is | What passing proves | zempo's frontier | goempo |
|---|---|---|---|---|---|
| R0 | Gate | `scripts/verify.sh` (gofmt, vet, tests, `tiger check`) passes | the code is formatted, vetted, tested and inside the tiger rules on every commit | yes | yes, from the first commit |
| R1 | Decode | share of sampled mainnet blocks decoded from `debug_getRawBlock`, re-encoded byte for byte, with block hash, transactions root, receipts root, bloom and every sender recovered | the types and signatures are right for every transaction kind on every fork (legacy, 1559, 7702, 0x76 with secp256k1, P-256, WebAuthn, keychain, sponsored, key authorization) | all | 632/632 mainnet blocks (T0..T13), 512/512 on moderato |
| R2 | Genesis | the state root of block 0 computed from the genesis alloc equals mainnet's | trie, RLP, keccak and account encoding are right | yes | yes, mainnet and moderato |
| R3 | Replay | the highest block N such that executing 1..N from genesis matches every block's gas used, receipts root, logs bloom and state root | the EVM, Tempo's transaction pipeline, every precompile and every fork rule agree with mainnet on everything mainnet ever did | 48,557,098 | 49,399,999 (the end of the block cache) |
| R4 | Follow | follows the live tip and keeps up | goempo can run as a node: fetch, link, certify, execute and commit new blocks as they finalize | yes | executes every block at the live tip with finalization certificates verified |
| R5 | RPC | share of the reference's RPC methods that answer identically on a recorded corpus | applications and tooling pointed at goempo get the same answers as from the reference | 100% | 111/111 methods, 3,378/3,378 cases; 121/121 reference methods served |
| R6 | Validator | Simplex consensus and DKG with the reference validators on a private network | goempo can be one of Tempo's validators, not only a follower | yes | local simulation passes; beside Rust validators R6a p2p, R6b wire, R6c builder and R6d wiring pass, with R6e mixed devnet and R6f moderato readiness in progress (see [R6 progress](#r6-progress)) |
| R7 | Speed | blocks per second with state root, against the reference on the same host | goempo is usable as production infrastructure | 3-4x | 59.0k blocks/s on 1..4.17M (1.9x its own earlier build); not yet measured against the Rust node on one host |

R3's height is the headline number. The rule was to climb in order: a rung above an unmet one does not count, and speed work starts only after R3 reaches the tip.

## The hill-climbing loop

Every agent ran the same loop ([LOOP.md](../LOOP.md)), one measured step at a time:

1. **Measure.** Pull, run `go run ./cmd/goempo-dev score`, compare with the last row of `FRONTIER.jsonl`.
2. **Pick the lowest rung below its frontier.** On R3 the work is always the first block that does not match: `goempo-dev replay` names it and the field that differs.
3. **Find the cause.** Diff goempo's execution of that block against the reference's `debug_traceBlockByNumber` (call frames, gas, opcodes, the prestate) and read the reference's Rust code. Don't guess.
4. **Fix it with a test** that fails before and passes after, usually on the real mainnet transaction with its prestate stored under `testdata/`.
5. **Gate.** `scripts/verify.sh` passes; tiger findings are fixed by rewriting the code, never silenced.
6. **Prove it went up.** Score again; revert if any rung went down.
7. **Record and push.** Append one JSON line to `FRONTIER.jsonl`, update the table and evidence paragraph in `FRONTIER.md`, commit with a message naming the rung, the old and new score and the cause, and push.

`FRONTIER.jsonl` is the machine-readable history (one row per measured step, 29 rows by the end of phase one); `FRONTIER.md` holds the current table plus a dated evidence paragraph per step with the exact command, the inputs and what was not yet verified. The scoring commands:

| Rung | Command |
|---|---|
| R1 | `goempo-dev fetch` then `goempo-dev score` (reads `~/cache/blocks/blocks.jsonl`) |
| R2 | `goempo-dev genesis` |
| R3 | `goempo-dev fetch-range -to N` then `goempo-dev replay` (or `score -rung R3`); long runs with `-datadir <dir> -checkpoint 500000` |
| R3 (EVM) | `goempo-dev statetest <dir>` over execution-spec-tests fixtures |
| R4 | `goempo-dev follow -last N` (with certificates), `goempo-dev certify -from A -to B`, then `cmd/goempo` |
| R5 | `goempo-dev rpcdiff -v` (or `score -rung R5`) against `~/cache/rpc/corpus*.jsonl` |
| R6 | `go test -v ./internal/sim/`, and the R6a..R6f sub-rungs in `FRONTIER.md` |
| R7 | `goempo-dev replay -diagnose off` reports blocks/s, gas/s, CPU and RSS in its `-result` JSON |

## The agents and how they coordinated

One lead agent took the request, wrote the scaffold (the rules in [AGENTS.md](../AGENTS.md), the ladder, the loop, the verify script and the tiger configuration) and then spawned workers, each owning disjoint packages. The lead reassigned agents as rungs fell and pushed the shared origin to GitHub; nobody else pushed there.

Phase one, by wave (times UTC, from the lead's records and the commit log):

| Time | Agents | Scope |
|---|---|---|
| 00:42 | encoding, math/crypto, types, evm | primitives, keccak, RLP, trie, genesis (R2 at 00:48); u256, secp256k1, P-256, BN254, BLS12-381, KZG, modexp, blake2f, ripemd160; block and transaction types with the R1 harness (R1 at 01:06); the Osaka EVM with statetests |
| 00:50 | encoding moves to state/storage; precompiles starts | the LSM store, store-backed trie and world state; Tempo's precompiles (TIP-20, TIP-403, fee manager and AMM, nonce manager, keychain) |
| 01:08-01:13 | types moves to executor/replay; math/crypto moves to devp2p | the Tempo block executor and `goempo-dev replay` (first R3 score, 3,662, at 01:52); RLPx, eth/68-69, the follower |
| 01:35 | rpc | the JSON-RPC server and the recorded corpus (R5), then tracing (02:33) |
| 03:51 | consensus | finalization certificates (R4) and then simplex and DKG (R6 sim) |
| 04:00 | server restart | agents were dropped; replays started in the background kept running |
| 04:05, 05:00 | respawned: climber (R3), precompiles, consensus (R4/R6), rpc (R5/R7) | the climber drove the long datadir replay to the tip; later node (cmd/goempo, R4 with execution) and speed (R7) |

How they kept out of each other's way:

- **One package, one agent.** Write scopes were disjoint; only shared files (`tiger.layers.yaml`, `go.mod`, `FRONTIER.*`, `README.md`) were edited by everyone, and rebase conflicts there were resolved by keeping both sides.
- **A local bare repository as origin.** Each agent cloned `~/goempo.git` into its own worktree under `~/wt/<role>`, committed small verified slices and ran `git pull --rebase origin main && scripts/verify.sh && git push origin main`. Unpushed work dies with the agent, and restarts did kill agents, so slices were small.
- **Messages for interfaces.** Agents asked each other for what they needed with directed agent messages instead of editing another agent's package. The climber, for instance, sent the precompiles agent each mismatching precompile call it found (`FRONTIER.md` records "sent to agent 5" for the ValidatorConfig selector at block 4,165,889), and the rpc agent built `eth_call` on the executor agent's `ApplyCall`.
- **Shared caches, one writer per file.** Mainnet blocks (`~/cache/mainnet`, 4,940 chunks of 10,000 blocks, 14 GB), the R1 sample and the RPC corpus live under `~/cache`; agents added files and never deleted others'.
- **The right machine for long runs.** Multi-hour replays ran in the background with a result JSON and a log under `~/scratch`, fetching blocks from a private mainnet RPC node in parallel shards to stay clear of public RPC limits; Rust builds and pinned-core perf measurements ran on a 32-core build machine, not on the 4-core dev host.

Phase two (from 18:01 UTC) is described under [R6 for real](#r6-for-real-the-validator-swarm).

## How every claim was verified

Nothing was taken from the reference except its behavior. The rule was: read the reference (tempoxyz/tempo, reth, revm, zempo's specs) to know where to look, never copy code, and believe only what mainnet itself answers. Five independent checks, from broadest to narrowest:

**Mainnet replay (R3).** The strongest check. `goempo-dev replay` executes every block from the embedded genesis and compares four header commitments per block: gas used, receipts root, logs bloom and state root. A single wrong gas charge, log, storage slot or nonce anywhere in history changes that block's commitments, so a full replay with no mismatch means goempo agrees with mainnet on everything mainnet ever did. The final run (`~/scratch/r3dd`) carried state on disk from genesis to 49,399,999 across seven binaries, each resuming from the previous run's checkpoint, and never patched a block.

**Prestate healing survey.** A mismatch stops a replay, so on its own replay shows one bug at a time. `replay -heal N -rpc <node>` instead applies the block's `prestateTracer` diff from the reference after a mismatch and keeps going, so one run lists every mismatching block in a range. The survey turned "the next bug" into a work list: the first 1.26M blocks had 8 mismatching blocks, all in two unimplemented precompiles; a later survey to 10.3M found 40, every one a precompile path exactly 100 gas short. A healed datadir is marked (`healed.json`) and the node refuses to start from it, so healing can never leak into a score.

**Trace diagnosis.** For a mismatching transaction, `replay` (since 04c516f) fetches the reference's `callTracer` frames and compares them with goempo's frame by frame, naming the first call whose gas differs. Most fixes started from that line.

**RPC differential corpus (R5).** Scripts (`scripts/rpc-corpus.py`, `rpc-corpus-extra.py`, `rpc-record.py`) recorded 8,549 requests and their answers from `https://rpc.tempo.xyz` and a private mainnet RPC node into `~/cache/rpc/corpus*.jsonl`: blocks of every transaction shape, replayable blocks for state, call and trace methods, and malformed requests for error codes. `goempo-dev rpcdiff` serves goempo from the cached blocks with state replayed from genesis and compares status code and JSON for every case. Cases whose answers depend on time, the gateway or the reference's own node are recorded as such and skipped (24 live, 5 gateway, 8 node-local). Probing about 200 method names found 121 methods the mainnet node answers; goempo serves all of them.

**Reference oracle corpus.** zempo's corpus of 4,992 generated precompile calls, each with the outcome tempoxyz/tempo's own precompiles produce over the same prestate (status, output, gas, refund, logs, changed slots), runs as `go test ./internal/tempo/precompiles -run TestOracle`. It covers paths mainnet has not exercised yet. goempo went from 4,563 to 4,986 identical; the 6 that differ are T14 keychain grants where goempo follows a newer tempo commit than the corpus.

**EVM statetests.** `goempo-dev statetest` runs the execution-spec-tests v5.4.0 state tests: 61,903/61,903 for Istanbul through Osaka. Pre-Istanbul forks are skipped because Tempo never ran them.

Smaller checks back these up: ethereum/tests RLP and trie vectors, the EIP-8 RLPx vectors, mainnet DKG outcomes re-encoded byte for byte, `eth_getProof` checked against mainnet's state root by an independent verifier, and finalization certificates verified across the epoch 2041 to 2042 full-DKG transition from the block cache.

## What mainnet replay found

Each line is a real difference between goempo and the reference that only executing mainnet exposed, with the first block it hit. Unit tests, specs and statetests had passed before each of them.

| Block | Commit | What was wrong |
|---|---|---|
| 3,663 | 2529d0d | the fee token (a USD TIP-20) and the payer's balance must be validated before fee collection; validation warms the token's slots |
| 3,865 | 6ebe692, 752eb25 | a key authorized in the same transaction is not yet the transaction key at fee collection; key authorizations run through the keychain, and same-transaction key use charges the fee against the new key's limit |
| 836,359 | 39c4015 | TIP-403 registry calls (policies) were not implemented |
| (survey) | a5da5eb | healing must rerun the block's system steps (the EIP-2935 history write) before applying the traced diff |
| (eth_call) | 722987f | the fee payer's fee-token balance is read even at a zero max fee, which warms the slot |
| 4,165,889 | d3b6743 | ValidatorConfig `changeValidatorStatus` was not implemented |
| 4,165,927 | 1d59deb | a validator array push re-reads the array length: 100 gas mainnet charges and goempo didn't |
| 10,129,394 | c20e89f | before T4, a TIP-20 reward record store loads its first slot (claimRewards 100 gas short at T1C) |
| 12,964,402 | c780ea9 | in a 0x76 batch, only call 0's target is warm up front; each later target is warmed when its call starts (2,500 gas when an earlier call touched a later target cold) |
| 16,986,086 | c76293d | from T3, a keychain-signed transaction checks its call scopes at transaction time, metered on the remaining gas (2,100 gas) |
| 18,337,286 | db91ca0 | a CREATE under a 2D nonce from a sender with account nonce 0 pays new-account gas (250,000 from T1) |
| 29,152,422 | b72dc5f | from T7, an EVM SSTORE that crosses zero runs TIP-1060's storage-credit hook (245,000 for a new slot); the handler's nonce and fee writes take no credits (block 29,152,424) |
| 35,816,049 | 10eefed | the committed T10 and T13 zone runtime files held wrong bytes of the right size; they were re-read from mainnet and pinned by code hash, and replay no longer flushes a block whose state root mismatched |

The precompiles agent's oracle work (04:20-05:05) fixed a further batch of reference behaviors before mainnet reached them: pre-T2 compound policy roles, strict enum decoding from T11, failed charges taking no gas until the frame halts, Panic(0x21) for out-of-range stored enums, invalid UTF-8 in stored strings being fatal, pre-T4 struct stores keeping bytes no member owns, and T14 keychain grants.

A pattern stands out: most of what mainnet caught was gas off by a small fixed amount (100, 2,100, 2,500) from a storage read or warm-up the reference does as a side effect. Such differences change nothing a unit test checks, and a validator would fork off the network on the first block that hit one.

## Timeline

All times UTC on 2026-11-17, from commit timestamps (203 commits in phase one: 13 before 01:00, 50 the next hour, then 36, 35, 29 and 28 an hour, and 12 after 06:00).

| Time | Event |
|---|---|
| 00:38 | request: a Go Tempo node like zempo, hill climbed to the frontier |
| 00:41 | scaffold: tiger gate, ladder, loop (27353d1) |
| 00:43-00:56 | primitives, keccak, RLP, u256, trie, secp256k1, P-256, LSM storage |
| 00:48 | **R2**: mainnet genesis state root matches (d1c1553) |
| 00:53-01:11 | Osaka EVM; statetests 100% Istanbul..Osaka with BN254, BLS12-381, KZG, modexp and blake2f wired |
| 01:06 | **R1**: 632/632 mainnet blocks decode and re-encode exactly (a5aa2d9) |
| 01:18-01:38 | world state, block cache fetcher, RLPx, devp2p, follower |
| 01:46-01:52 | block executor, Tempo precompiles, Tempo transaction pipeline; **R3 = 3,662** |
| 02:04 | healing survey (e84bda4) |
| 02:09 | R3 = 836,358 (key authorizations) |
| 02:13 | RPC server and recorded corpus; R5 scored from 02:33 |
| 02:14-02:21 | R3 = 3,209,999 (TIP-403), then 4,165,888; T8 committee system call |
| 02:49-03:47 | EVM tracer hooks, debug and trace methods, eth_call path; DEX, zone, channel and configurable-account precompiles |
| 03:23 | R3 = 4,165,926 |
| 03:38 | R5: 66/66 methods, 3,114/3,114 cases |
| 04:00 | server restart; agents respawned at 04:05 and 05:00 |
| 04:05-04:22 | **R4**: simplex threshold certificates verified while following the live tip |
| 04:06 | `replay -datadir`: state on disk with checkpoints, so one replay can cover the whole chain |
| 04:19 | reference oracle corpus wired in (429 diffs; 4,986/4,992 by 05:09) |
| 04:29 | R3 = 10,129,393 (datadir replay, no heals) |
| 04:32-04:55 | commonware DKG and reshare; **R6 sim**: simplex validators with reshare in a deterministic multi-node simulation |
| 05:14 | **R5**: 111/111 methods, 3,378/3,378 cases; 121/121 reference methods served |
| 05:28 | cmd/goempo: one node binary that executes, follows, certifies and serves RPC |
| 05:11-07:57 | R3 15.15M, 23.1M, 30.7M, 39.5M, then **49,399,999** with no heals |
| 05:02-06:28 | **R7**: replay twice as fast (59.0k blocks/s on 1..4.17M) |
| 07:35 | **R4 with execution**: cmd/goempo executes every block at the live mainnet tip (49,438,051) |
| 08:21 | last commit of phase one |
| 18:01 | phase two starts: a validator swarm for R6 against the Rust tempo validators |

## Constraints

These held for every line, and they shaped the code as much as the reference did.

- **Go 1.26, standard library only.** Allowed imports: the standard library, golang.org/x/crypto, golang.org/x/sys and tiger, plus go.uber.org/goleak in tests. No go-ethereum, no holiman/uint256, no EVM or cryptography libraries. goempo therefore has its own keccak, RLP, Merkle Patricia trie, 256-bit integers, secp256k1, P-256, BN254, BLS12-381, KZG, blake2f, Snappy, LSM storage, EVM, devp2p and DKG.
- **tiger, never silenced.** `tiger check` (kapetan-io/tiger) enforces NASA's Power of Ten and TigerStyle on Go: every loop provably ends or stops at a cap that reports when hit; goroutines start in a group that waits for them; switches over enums are exhaustive; the clock and randomness are inputs; a checked-in layer file (`tiger.layers.yaml`, one entry per package, added in the same commit as the package) says which package may import which. It has no warnings and ignores `//nolint`; its only escapes are two directives that need a reason and spend a per-package budget. goempo has no escape directives and an empty budget: every finding was fixed by rewriting the code, which is why so many early commits read "bound the loop by a constant".
- **No prose comments below the package clause** in production files (rule TS-L14): names and tests carry the meaning.
- **Tests from outside.** Tests live in `package X_test`, assert with `github.com/kapetan-io/tiger/assert`, use bounded loops and never panic directly.
- **Reference behavior over reference code.** Read tempoxyz/tempo, reth, revm and zempo's specs for where to look; never copy. Every intended difference from the reference is listed, numbered, in [DEVIATIONS.md](DEVIATIONS.md) (none so far).
- **Read-only on shared networks.** Mainnet and moderato are touched only through read-only RPC and passive peering; no transaction or registration is ever sent to a shared network. Small fixtures are committed under `testdata/` with a README line naming their source; everything large stays in `~/cache`.

## R6 for real: the validator swarm

Phase one ended with goempo following mainnet as an exact replica but validating only inside its own simulation: `internal/consensus` (simplex voter and certificate verifier), `internal/dkg` (commonware's Feldman/Desmedt DKG and reshare, byte-compatible with mainnet's on-chain outcomes) and `internal/sim` (4 to 7 validators on a seeded lossy network with drops and outages, every certificate accepted by the same verifier that checks mainnet). What it lacked to sit beside the Rust validators: commonware's authenticated p2p, byte-compatible consensus, DKG and marshal network messages, a transaction pool and payload builder, and validator keys, registration and a validator mode in cmd/goempo. The proof path is a private devnet of Rust tempo validators, then a mixed devnet where goempo votes and proposes, then readiness for moderato.

Six agents started at 18:01 UTC, each with disjoint packages:

| Role | Scope | Sub-rung |
|---|---|---|
| cwp2p | commonware authenticated p2p in Go | R6a p2p interop |
| wire | byte-compatible consensus, DKG and marshal messages, checked against golden vectors from the Rust code | R6b wire vectors |
| builder | transaction pool and payload builder | R6c builder |
| validator | keys, registration tooling, cmd/goempo validator mode wiring the pieces together | R6d validator wiring |
| devnet | a Rust tempo devnet harness on a 32-core build machine, then a mixed goempo and Rust devnet, then moderato readiness | R6e mixed devnet (goempo's votes counted and its proposals finalized by Rust validators), R6f moderato readiness |
| scribe | this document, [METRICS.md](METRICS.md) and `scripts/metrics.sh` | |

Rust is allowed only in harnesses under `~/goempo-work` on a 32-core build machine, to produce golden vectors and run reference nodes; never in goempo itself.

### R6 progress

Updated from the commit log as the swarm works.

- 18:01: swarm started.
- 18:06-18:17: validator key files (ed25519 signing key, BLS share) and ValidatorConfigV2 add/rotate call data (9af5049); commonware's Random leader elector, checked against mainnet's actual proposers (10a1fc5); the voter now follows commonware simplex (elected leaders, the proposal carried in the leader's notarize, certify before finalize; 869e15b); state and executor can build a block one transaction at a time with rollback and compute a pending root without writing (65ce9c9).
- 18:11-18:15: **R6b passes** (426c0ff, 84dc0fa): every consensus, resolver, broadcast, marshal and DKG message encodes byte for byte like commonware 2026.10.0, in both directions. Rust-made votes and certificates decode, re-encode and verify in Go; Go-signed votes and Go-recovered certificates are byte-identical to Rust's; 32/32 Rust DKG dealings are accepted by Go players whose acks equal Rust's; Go-made messages verify in the Rust harness.
- 18:15-18:19: commonware's SAKE handshake and CUPS records, byte-exact against vectors from the Rust harness (8489be1), then the authenticated lookup network: per-channel rate limits and backlogs, peer-set oracle, dialing, pings, blocking (6dc934e). R6a (interop with a live Rust peer) is not recorded yet.
- 18:11-18:29: the Rust devnet harness (70aba0b, f21b8fe): 4 Rust validators from a fresh genesis and DKG ran 1,780 finalized heights on all four, 0 forks, 0 stalls, 30/30 DKG boundary outcomes; stopping 1 of 4 keeps finality and stopping 2 halts it, the quorum test that will prove goempo's votes count. Any slot can be swapped for goempo with the same keys. R6e waits on goempo's validator mode.
- 18:27-18:29: transaction pool with Tempo admission checks and 2D-nonce lanes, payload builder with payment lanes (e92c7f5); validator engine wiring the simplex voter, wire codecs, block fetch and DKG reshare at epoch boundaries, finalizing 3 epochs in a 4-validator in-memory cluster (f6701cd).
- 18:35-18:49: goempo gets a local chain for validator mode: genesis from a genesis file (mainnet's and the Rust devnet's hash exactly), execute without commit, a local block store, DKG inputs from ValidatorConfigV2 (8d1dd4e), and a fork schedule read from the genesis config for chains other than mainnet and moderato (2ec3459).
- 18:37-18:42: **R6a passes** (568fde6, cbe48e1): a Go node and a Rust commonware-p2p node exchange 30/30 messages each way on all six channels, including 200 KB frames, whichever side dials; on the Rust devnet, a Go node holding slot 0's key authenticated with all 3 live Rust validators and received about 1,500 real vote, certificate, block and DKG messages in 30 s, with no warnings on the Rust side.
- 18:38: **R6c passes** (3f17325, 39df4ed): `goempo-buildcheck` feeds each mainnet block's transactions into a fresh pool over the parent state and rebuilds the block. Over 49,400,000..49,419,999: 9,507 transactions, 0 pool rejections, 7,395 of 7,547 blocks byte-identical (same block hash) and the other 152 the same transactions in a different order with the same state root (a transaction reached tempo's pool mid-build, which nothing in the block records), 0 mismatches.
- 18:45: the first mixed run (goempo in slot 3, Rust in 0-2) stopped goempo at block 1: an empty block from a Rust proposer still changes the state root through the Prague pre-block system call (the EIP-2935 history contract storing the parent hash), and goempo's result differed. The fork schedule from the devnet's genesis config (2ec3459) landed four minutes later, and the R6d run below went past block 1.
- 18:40-18:50: **R6d passes** (1a446e2, d8c814d): `goempo validator` (tempo's flag names; peers from ValidatorConfigV2) and `goempo keys` (prints add/rotate call data, never submits). All 14 mainnet ValidatorConfig add/rotate calls re-encode byte-identical with valid signatures, and the elector picks mainnet's real proposers. 4 goempo validators on the Rust devnet genesis finalized 231 blocks and 3 reshares in 30 s; 1 goempo beside 3 Rust validators reached finalized height 102 with identical hashes, goempo proposing 28 of those blocks, across the epoch 0 to 1 boundary. R6e asks for more: a long soak, the quorum test with one Rust validator stopped, and goempo's dealing in every DKG outcome (75cb1d8).
