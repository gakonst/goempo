# Frontier

## Storage-trie retention (2026-11-19 PST)

Goempo's Busy benchmark replays mainnet blocks 9890000–9909999 from a saved parent state.
Accepted code `43525c3`: mean **44.6414 → 40.8580 seconds** versus
baseline `77a9410`, five fresh pairs ordered AB, BA, AB, BA, AB.
A is the baseline build and B the cache-retention candidate; each uses a fresh state copy. Mean paired saving
**8.4495%**, two-sided Student-t 95% interval **5.6443–11.2547%**;
paired seconds saving **3.7834**, 95% interval **[2.4710, 5.0957]**. Both arms use logical
CPUs 20–27 and GOMAXPROCS=8 on the same 32-logical-CPU host, Go 1.26.10.
No retries or dropped samples; all ten monitor traces valid.

Storage-trie caches avoid loading and decoding the same on-disk nodes again.
When a cache exceeds its node threshold, trimming discards descendants below a
retained depth. The per-trie threshold rises from 16384 to 262144 nodes, and the
retained depth from two to four levels. The aggregate threshold across storage
tries stays 2097152 nodes. These are soft trimming thresholds, not hard memory
bounds: proof reads can load nodes without updating the aggregate count. Peak RSS increased from 1717.23 to 1777.43 MiB.
Every replay checked all 20000 block roots, gas, receipts and bloom, totaling
659246 transactions and 31499321696 gas. The decode correctness milestone, called R1, checks byte-identical block
re-encoding, transaction senders and receipts. The baseline and candidate both
passed on the same saved sample of 632 mainnet blocks and 860 transactions. Rebuilding the
state root from stored account/slot records at the parent and terminal heights
matched the recorded block headers. This uses this project's trie implementation,
not an independent client.

Repository: `gakonst/goempo`. Use the baseline or candidate binary whose hashes
are below. The input directory contains recorded raw block chunks for the Busy
range; each run needs a fresh copy of the saved state at block 9889999. This
command replays through 9909999 with healing and optional diagnostics disabled:

```sh
taskset -c 20-27 env GOMAXPROCS=8 RAYON_NUM_THREADS=8 <baseline-or-candidate-goempo-dev> replay -datadir <fresh-parent-datadir> -dir /home/ubuntu/work/goempo/busy-v5/input-chunks -to 9909999 -diagnose off -heal 0 -checkpoint 100000 -result <row>/replay.json
```

Baseline binary SHA256: `fe475c0708d01955b55d245638187edb0923a9e2f2192601b4d61322f1d871ac`.
Candidate binary SHA256: `56aa699a98ea5dfa666ef39bcf69b6ba22b0717d8279170236ac487075fd1b35`.
Local checks passed in the AGENTS.md offline environment:

```sh
go build ./...
scripts/verify.sh
go test -race -count=1 ./internal/state ./internal/trie
go test -count=1 -v ./internal/sim
```

A separate candidate based on the same baseline `77a9410`, using a stack-allocated
Keccak hashing object (`b39f9ba`), failed acceptance:
42.8684 → 43.4842 seconds, N=5 pairs, paired saving -0.6159 seconds
[ -2.6803, 1.4486 ]; it was not retained. Rust was not rerun in these batches.
The reference Rust Tempo node has a historical 26.4-second point. The goal is
twice its throughput, or 13.2 seconds for the same work; that goal remains unmet.


## Parallel trie commit (2026-11-19 PST)

A second accepted change, `e154047`, adds bounded parallel hashing to trie commits
on top of the cache change above. In a fresh five-pair Busy comparison, baseline
A is cache main `a8c0120` (the same code as `43525c3`, with result documentation)
and candidate B adds parallel hashing. Their means are
**39.9298 → 37.5371 seconds**. Mean paired saving is **5.9704%**, Student-t 95%
interval **[3.4843, 8.4565]%**; paired seconds saving is **2.3927**, 95% interval
**[1.3442, 3.4412]**. Acceptance requires at least 5% mean paired saving and a
paired seconds interval entirely above zero. The fixed order is AB, BA, AB, BA,
AB, with the same logical CPUs 20–27 and GOMAXPROCS=8. No sample was retried or
removed. This is a separate batch from the cache comparison; do not multiply its
point estimate by the cache gain to claim a matched cumulative speedup.

For a dirty root branch with at least four dirty children and 128 dirty nodes,
three joined workers plus the caller hash disjoint child subtrees. Each worker
owns its encoder, hasher and scratch. The parent is encoded only after all workers
join; node writes and their order remain serial. Small/nonbranch cases and the
public Hash operation keep the serial path. This does not permit concurrent
external operations on one Trie.

All ten Busy runs checked every block's state root, gas, receipts and bloom, plus
parent/terminal stored-state root reconstruction. All CPU-monitor traces were
valid. Both builds also passed the separate R1 sample: 632/632 blocks and
860/860 senders and receipts. Peak RSS was 1775.48 MiB for A and 1760.86 MiB for B. The independent map/RLP model rebuilds Patricia nodes from a plain map rather
than calling the production trie algorithms. That model, the race detector,
full local verification and deterministic simulation gates
passed. The measured candidate was `a3bb5b8`; the landed code and tests are identical,
with only frontier documentation differing.

Use the exact Busy replay command and fresh saved-state copies above. Baseline
binary SHA256 is `56aa699a98ea5dfa666ef39bcf69b6ba22b0717d8279170236ac487075fd1b35`;
candidate SHA256 is `2677c0697d4cd7f6a8c9f1cc3fa42b33e4234bc51737557e523268aa6d36246f`.
The same build, verify, state/trie race and simulation commands above apply.
Rust was not rerun, so the historical 26.4-second reference remains unmatched;
the 13.2-second target is still unmet.

In commands below, <private-mainnet-rpc> is a private mainnet RPC node used for bulk block fetches; https://rpc.tempo.xyz answers the same methods, with rate limits.

| Rung | Score | Commit | Date |
|---|---|---|---|
| R0 Gate | scaffold | - | 2026-11-17 |
| R1 Decode | 100% (632/632 mainnet blocks) | a5aa2d9 | 2026-11-17 |
| R2 Genesis | yes | d1c1553 | 2026-11-17 |
| R3 Replay | 49399999 | 10eefed | 2026-11-17 |
| R4 Follow | executes every block at the live mainnet tip (gas, receipts root, bloom, state root) with finalization certificates verified, JSON-RPC over the live state (cmd/goempo from climber's genesis-replayed datadir) | 67d1f54 | 2026-11-17 |
| R5 RPC | 111/111 methods, 3378/3378 cases (100%); 121/121 reference methods served | ab9f4ae | 2026-11-17 |
| R6 Validator | simplex+DKG reshare in local sim (4-7 nodes, faults), certificates accepted by consensus.Chain | 625567f | 2026-11-17 |
| R7 Speed | 59.0k blocks/s, 30.3 Mgas/s on 1..4,165,926 (32-core build machine, 4 pinned cores; 1.9x vs 18796f8); 5.3-5.7k blocks/s, 253-267 Mgas/s on 23.5M..24.3M (4-core dev host); vs Rust tempo 1.18.4 on the same 8 pinned threads (a01c695): early 1..1M 9.2x faster wall, recent 49.41M..49.45M 2.5x faster, busy 9.89M..9.91M 1.57x slower (47.2 s vs 30.0 s); RPC single-client 1.1-2.0x faster, 16-64 clients tie or trail | a01c695 | 2026-11-17 |

R2 passes (d1c1553, 2026-11-17): `go run ./cmd/goempo-dev genesis` computes the mainnet block 0 state root 0x83408974323f63ab969b23f0fe1dba30d7ee5dc5c524a975bae38187eaa2c7f6 from the embedded presto genesis, equal to eth_getBlockByNumber("0x0").stateRoot on https://rpc.tempo.xyz. The testnet (moderato, chain 42431) genesis also matches rpc.moderato.tempo.xyz: 0x3952ff0c60a3f215ab11a3a2c7698126b7911da84faf5cb66bdbcb0c605738c4.


R1 passes (a5aa2d9, 2026-11-17): `go run ./cmd/goempo-dev fetch` samples mainnet (https://rpc.tempo.xyz) across every fork window T0, T1/T1A, T1B, T1C, T2..T13 (24 evenly spaced blocks per window plus the 12 busiest of 400 probed; T14 is not active yet) into ~/cache/blocks/blocks.jsonl, and `go run ./cmd/goempo-dev score` decodes each block, re-encodes it byte for byte, and checks the block hash, the transactions root (internal/trie), every tx hash, every recovered sender against the RPC `from`, the receipts root and logs bloom rebuilt from eth_getBlockReceipts, the 0x76 fee payer against the receipt `feePayer`, keychain key IDs against the RPC `keyId`, and key-authorization signers against the sender. Mainnet: 632/632 blocks, 860 txs (legacy, system, 1559, 7702, 0x76 with secp256k1, P256, WebAuthn, keychain V2, sponsored, key authorization). Testnet (moderato, same sampling): 512/512 blocks, 31059 txs, adding keychain V1 and AA authorization lists.

R3 EVM statetests: 61903/61903 (100%) of execution-spec-tests v5.4.0 fixtures_develop state_tests for Istanbul..Osaka (Osaka 19517/19517), run by go run ./cmd/goempo-dev statetest <dir> with internal/evm and an in-memory state; pre-Istanbul fork cases (1653) are skipped (not supported).

R4 (partial, 2026-11-17): go run ./cmd/goempo-dev follow -last 1000 starts at an anchor 1000 blocks below the mainnet tip (hash from https://rpc.tempo.xyz), downloads blocks from the RPC upstream (debug_getRawBlock), links every header to its parent hash, checks each body against its header (transactions root, ommers hash, withdrawals root), checks that the chain reaches the upstream's tip hash, hands each block in order to an executor interface (internal/sync Executor), and compares every block hash with eth_getBlockByNumber on the check node. Mainnet run: 1082 blocks (684 txs) from 49392468 to the tip in 44 s, all 1082 hashes equal, then followed the tip for another ~840 blocks. The devp2p path (internal/p2p/rlpx: EIP-8 ECIES handshake and frames matching the EIP-8 vectors; internal/p2p: Hello, eth/68-69 Status with Tempo fork id matching eth_config's forkId for T11..T13, GetBlockHeaders and GetBlockBodies) is tested against real mainnet blocks over the in-memory network only: the dev host cannot reach mainnet devp2p ports (its egress allows port 443 only; a private route to a mainnet node's devp2p port accepted TCP but never answered the auth). Not done yet: executing the followed blocks (needs state at the anchor: a snapshot, or replay from genesis), consensus finalization certificates (BLS), discovery (discv4/discv5).

R3 replay (033b5e0, 2026-11-17): "go run ./cmd/goempo-dev fetch-range -to 9999" caches mainnet blocks (debug_getRawBlock, 10k-block flate chunks under ~/cache/mainnet), then "go run ./cmd/goempo-dev replay" (or "score -rung R3") executes blocks 1..N from the embedded genesis with internal/tempo/executor over internal/state, checking each block's gas used, receipts root, logs bloom and state root. Height 3857 (was 3662 before the fee token was validated in the handler, which warms its TIP-20 slots): blocks 1..3210 are system-transaction-only; 3211..3662 include the first 0x76 and legacy user transactions with TIP-20 fees through the fee manager. Now 3864 with TIP-20 factory createToken (3858) and CREATEs (3863/3864). First mismatch: block 3865, the first key authorization (keychain authorizeKey not in internal/tempo/precompiles yet; gas already matches, the KeyAuthorized log and key slot are missing).

R3 survey with healing (2026-11-17): "goempo-dev replay -heal N -rpc <node>" applies the block's prestateTracer diff after a mismatch and keeps executing, to list every mismatching block in one run. Blocks 1..1,259,999 executed in 30 s with 8 healed blocks: 3865, 3876, 3878, 3892, 3910, 1179655 (keychain authorizeKey not implemented: KeyAuthorized log and key slot missing; gas matches), 3934 (gas 200 low, key authorization with a WebAuthn key, sponsored), 836359 (TIP-403 registry calls, not implemented). Every other block matches gas, receipts root, bloom and state root.

R3 = 836358 (2026-11-17): key authorizations (keychain AuthorizeKey from agent 5, same-tx key use with AuthorizeTransfer of the collected fee) now match at 3865..3934, 1179655 and 1524056. First mismatch: block 836359, TIP-403 registry calls (precompile ABI not implemented). With -heal, blocks 1..1,599,999 have that single mismatching block.

R3 = 3,209,999 (2026-11-17): with the TIP-403 registry (agent 5, 39c4015) every block from 1 to 3,209,999 matches gas, receipts root, bloom and state root, in 167 s (19k blocks/s) on the 4-core dev host. The height is the end of the local block cache; the fetch from a private mainnet RPC node continues. Mainnet fork boundaries ahead: T1/T1A 4,494,230, T1B 6,253,936, T1C 8,967,990, T2 12,290,227, T3 16,986,044, T4 20,636,964, T5 24,387,406, T6 26,629,191, T7 29,152,418, T8 32,434,902, T9 33,513,646, T10 35,816,049, T11 38,891,821, T12 43,957,545, T13 47,250,841, T14 49,397,180.

R3 = 4,165,888 (2026-11-17): every block from 1 to 4,165,888 matches. First mismatch: block 4165889 tx 0, a ValidatorConfig (0xcccc…) call with selector 0xa1b3f4a9 (validator config ABI not implemented in internal/tempo/precompiles yet; sent to agent 5). The executor also now carries the T10 zone factory and runtimes, T13 runtime upgrade, T2..T8 precompile markers, TIP-1115 fallback fee token and the T8 committee system call; those boundaries are not reached by the replay yet.

R5 (2026-11-17): internal/rpc serves the reference node's method set (eth, net, web3, debug, txpool, tempo_forkSchedule, consensus_, trace_ and eth_getTransactions as the reference exposes them) over HTTP and WebSocket (eth_subscribe newHeads/logs), with the reference's JSON shapes (Tempo header fields, 0x76 transactions with every signature kind, keychain, key authorization, fee payer, receipts with feeToken/feePayer), parameter parsing and error codes/messages copied from recorded answers. internal/rpcexec backs eth_call and eth_estimateGas with internal/evm and the Tempo precompiles over replayed state. The corpus is recorded from https://rpc.tempo.xyz with scripts/rpc-corpus.py, rpc-corpus-extra.py and rpc-record.py into ~/cache/rpc/corpus*.jsonl (2,700 requests: 58 mainnet blocks covering every transaction shape in the R1 sample, 40 replayable blocks 3211..3864 for state, call and trace methods, envelope and parameter errors); "go run ./cmd/goempo-dev rpcdiff -v" (or "score -rung R5") serves goempo from the cached blocks and receipts with state replayed from genesis and compares every answer (status code and JSON). Score: 50 of 57 methods answer every case identically, 2671/2697 cases. Not identical: debug_traceBlockByNumber/trace_block/debug_traceCall for blocks with user transactions (no EVM tracer hooks yet; system-only blocks match), eth_createAccessList and eth_simulateV1 (not implemented), eth_estimateGas 14/16 and one eth_call revert message (the RPC call path runs without the Tempo handler's fee-token resolution, which warms slots, and without Tempo's custom-error rendering in revert messages). Excluded from the score: 7 live-head answers (eth_blockNumber, gas price, capabilities), 5 answers the public gateway refuses with 403 (txpool_*, admin_*, unknown methods: not the node's answer) and 3 node-local answers (peer count, random filter ids). eth_getProof/eth_getAccount check the proof window like the reference but return an error (no trie proofs yet). "goempo-dev serve -addr :8545 -state N" serves the same over a real TCP socket (internal/io ListenTCP); eth_getBlockByNumber, eth_call and eth_getBlockReceipts answers over the socket were byte-for-byte equal (as JSON) to rpc.tempo.xyz.

R5 (2026-11-17, agent 10): debug_traceBlockByNumber (callTracer incl. onlyTopCall/withLog, prestateTracer incl. diffMode, default struct logger with stack/memory/storage/returnData, 4byteTracer, noopTracer, muxTracer) and trace_block (parity) now replay the block through internal/tempo/executor with the new internal/evm tracer hooks (separate traced interpreter loop: no per-opcode check or interface call when no tracer; BenchmarkLoopUntraced unchanged) and internal/tracing (recorder + state wrapper that captures pre/post account state per transaction). Tempo 0x76 transactions get the reference's synthetic root (callTracer from=sender to=0x0, gas=tx gas limit; struct logger failed:true with no logs; parity root with zero action). Corpus grew by corpus-g/corpus-h (264 requests over 20 mainnet blocks 3211..5437 with user transactions, recorded from https://rpc.tempo.xyz with scripts/rpc-record.py); every recorded trace answer matches. Score 52/57 methods, 2919/2925 cases. Still not identical: debug_traceCall, eth_createAccessList, eth_simulateV1, eth_estimateGas 14/16 and one TIP20 revert message (all on the eth_call path).


R5 (2026-11-17, agent 10): 66/66 methods, 3114/3114 recorded cases identical. Added corpus-i (189 requests recorded from https://rpc.tempo.xyz with scripts/rpc-record.py: trace_transaction/trace_get/trace_replayTransaction/trace_replayBlockTransactions (trace + stateDiff)/trace_filter/trace_call/trace_callMany, debug_traceTransaction (call/prestate diff/struct), debug_traceCall variants, eth_callMany, eth_createAccessList, eth_estimateGas, eth_simulateV1 with overrides/gap blocks/validation). New: trace_* parity family incl. stateDiff (internal/tracing/statediff.go), eth_callMany, eth_simulateV1 (simulated headers, tx/receipt roots, zero-signature 1559 / fixed-signature Tempo txs as the reference builds them), eth_getProof/eth_getAccount from trie Merkle proofs (verified against mainnet stateRoot at block 5000 with an independent checker), Tempo precompile custom errors in revert messages (161 errors). Call path runs through executor.ApplyCall (agent 7).


R5 (2026-11-17, agent 10): goempo-dev serve now serves connections concurrently (rpc.Transport.Serve: up to rpc.ConnsActiveMax=256 at once, Close stops the listener, closes open connections and waits; goleak in rpc tests). Replayed state is guarded by one lock shared by rpcexec (Executor.Lock) and the state readers. Checked with a -race build of serve at block 5000 under 24 concurrent clients mixing eth_call/estimateGas/getProof/traces: no races, consistent answers.
R3 = 4,165,926 (2026-11-17, d3b6743): ValidatorConfig changeValidatorStatus (agent 5) matches at 4165889. First mismatch: block 4165927 tx 0, ValidatorConfig selector 0xffb4822e charges 100 gas less than mainnet. A -heal survey of 1..10.3M finds 40 mismatching blocks, every one a precompile path 100 gas short (ValidatorConfig 0xffb4822e, TIP-20 transfer/0x372500ab at T1C from 10,129,394, mint, one DEX swap); the handler side shows no mismatch. Block cache is contiguous to 13.84M (fetch from a private mainnet RPC node at ~1.5k blocks/s continues); the in-memory replay state is the next limit for a full-chain run (internal/state on disk).

R5 (2026-11-17, agent 3 "rpc"): every method a private mainnet RPC node (tempo/v1.18.4-cf561ef) answers is now served: 121 of 121 discovered by probing ~200 reth/geth/tempo method names, 0 left over. New corpus ~/cache/rpc/corpus-j.jsonl (267 requests recorded from the mainnet node with scripts/rpc-record.py, wired into rpcdiff). New methods: eth_getBlockAccessList / ...Raw / ...ByBlockHash / ...ByBlockNumber (EIP-7928 list built from per-transaction pre/post diffs in internal/tracing/bal.go; JSON and RLP byte-identical on 22 user-transaction blocks 3211..5437 plus system-only blocks), eth_getMultiProof, debug_codeByHash and debug_dbGet (code table lookup), trace_transactionOpcodeGas / trace_blockOpcodeGas (rpcdiff compares opcodeGas as a set because reth's order is HashMap order and changes between calls), debug_traceCallMany, debug_traceBlock (raw block of a known block), tempo_simulateV1 (simulate + TIP-20 tokenMetadata), eth_callBundle, eth_fillTransaction, eth_config and debug_chainConfig (from the hardfork table; recorded as version-dependent because v1.18.4 predates the T14 entry goempo has, and everything else matches), debug_getRawTransactions, debug_seedHash, debug_traceBadBlock, rpc_modules, consensus_subscribe, token_* param handling, and reth's debug stubs with typed params. debug_executionWitness*, debug_intermediateRoots, debug_stateRootWithUpdates, trace_rawTransaction and token_* match the reference on param, not-found and decode errors. Their success paths are not implemented: the reference times out on the witness and roots calls, and the token index is unavailable. Score: "go run ./cmd/goempo-dev rpcdiff -v" (score -rung R5) gives 111/111 methods, 3378/3378 cases identical (24 live-head, 5 gateway-403, 8 node-local excluded). Commits 4903183, 09d0a51, 18796f8, a0347c5, ab9f4ae.




R3 = 10,129,393 (2026-11-17, 04c516f): "goempo-dev replay -datadir ~/scratch/r3dd -checkpoint 500000 -to 10129393 -rpc <private-mainnet-rpc>" executed every mainnet block from genesis to 10,129,393 with state on disk (internal/storage, checkpoint every 500k blocks), no heals: gas used, receipts root, logs bloom and state root all match, through T1/T1A (4,494,230), T1B (6,253,936) and T1C (8,967,990). 1224 s, 8.3k blocks/s on the 4-core dev host. The run stopped at its target: the old binary's first mismatch was 10,129,394 (TIP-20 claimRewards 100 gas short, fixed by c20e89f, precompiles agent); with c20e89f a resumed run from the 10,129,393 checkpoint passes 10,129,394..10,300,000 (in progress toward 20.5M, the end of the contiguous block cache).


R4 certificates (2026-11-17, c22fe5a): goempo-dev follow verifies every followed block's finalization certificate (consensus_getFinalization from the upstream, bls12381_threshold MinSig, batched in one random-linear-combination pairing check per epoch). The epoch identity is anchored at the previous boundary block's DKG outcome (extraData, taken on trust); the next epoch's identity is installed only after the boundary block itself is certified. Command: goempo-dev follow -upstream <private-mainnet-rpc> -check <private-mainnet-rpc> -last 15000 -steps 13000. Evidence (~/goempo-runs/follow-r4.log on the 4-core dev host): anchor 49396395, consensus anchor = DKG outcome in block 49377599 (identity 8627d33753b0d90a); "epoch 2287: identity 8627d33753b0d90a... from the dkg outcome in certified block 49399199"; reached tip 49411705: 15310 blocks, 15310 hashes checked, 15310 finalization certificates verified, 0 uncertified, 1 epoch transition, R4 PASS; then kept following the live tip with certificates verified per batch. goempo-dev certify -from 44085600 -to 44129999 verified every certificate across the 2041->2042 full-DKG transition from the chunk cache. Still not done for R4: execution of followed blocks, discovery.

R4 node, follow with execution (2026-11-17, agent node): cmd/goempo is the one node binary (internal/node): it opens the state datadir that "goempo-dev replay -datadir" writes (an empty one loads the mainnet genesis), refuses one with healed.json, reloads the last 256 block hashes and checks the head block's state root against the database root, then executes blocks in order from the chunk archive (~/cache/mainnet) and the upstream RPC (debug_getRawBlock, 16 parallel 32-block batches): each block is linked to its parent and its body checked against its header (internal/sync), its finalization certificate verified when -certify (internal/consensus Chain, identity anchored at the previous epoch's boundary DKG outcome, next identity installed from each boundary block; near the tip it executes only certified blocks or blocks hash-linked to a certified descendant), executed with internal/tempo/executor and committed only if gas used, receipts root and bloom match, then the state root checked; any failure latches the node at the last good block. internal/rpc serves the live state (StateAt/ProofAt/ExecState at the head only, recent blocks with executor receipts, older blocks from the archive) on -http, sharing one mutex with block execution, and publishes each executed block to newHeads subscribers. Window proof: scripts/node-snapshot.sh hard-links a replay checkpoint; "goempo -datadir ~/scratch/node-dd10 -to 10140000 -certify=false" resumed at 10,129,393 and executed 10,607 blocks (archive, then upstream-only) with every gas/receipts/bloom/state root equal, head hash 0xba0e699b... = mainnet 10,140,000; scripts/node-rpccheck.py against a private mainnet RPC node at that head: 30/40 answers identical (blocks, receipts, nonces, code, storage, eth_chainId), the rest are eth_getProof (upstream refuses proofs that deep) and debug_traceTransaction (needs the parent state; the node keeps the head only). Then 10,140,000 -> 10,270,560 (130k blocks) at ~2,300 blk/s from the archive and ~775 blk/s upstream-only; a hard-linked snapshot of climber's live datadir at 18,500,000 opened and executed forward. Tests: internal/node executes mainnet 0..200 from a fixture, resumes, serves JSON-RPC, latches on a tampered receipts root, verifies mainnet certificates across the 2041->2042 boundary from fixtures. Certified window: from a scripts/node-snapshot.sh copy of climber's datadir at 28,500,000, "goempo -datadir ~/scratch/node-dd28 -upstream <private-mainnet-rpc>" executed 94,719 blocks (28,500,000 -> 28,594,719) verifying all 94,719 finalization certificates (anchor: the DKG outcome in boundary 28,490,399; 4 epoch transitions), every gas/receipts/bloom/state root equal; at ~390 blk/s, bound by fetching one certificate per block. Since d6e1242 a span buried 512+ below the tip is certified by its last block's certificate plus the parent-hash links (all certificates near the tip): the next 201,664 blocks to 28,796,383 ran at ~1,700 blk/s, 402 certificates verified, 201,262 linked by hash, 0 uncertified, 10 epoch transitions. At the tip (67d1f54): from a scripts/node-snapshot.sh copy of climber's genesis-replayed datadir at 46,000,000, "goempo -datadir ~/scratch/node-dd46 -upstream <private-mainnet-rpc> -http 127.0.0.1:18545" executed 3,438,051 blocks to the live mainnet tip 49,438,051 (1,579,058 txs, 283.8 Ggas, 2,697 s; ~2,290 blk/s from the archive with senders recovered on worker goroutines) with every gas/receipts/bloom/state root equal, 159 epoch transitions, 8,668 certificates verified, 3,429,351 blocks linked by hash to a verified certificate, 32 buried blocks admitted without one (the upstream served none for them; to investigate), then executed each new block as it arrived (729 live blocks, one block behind the upstream; block 49,437,510's hash and state root equal the private mainnet RPC node's), restarted from its flush at exactly 49,438,051 and kept following. An earlier run from 28.8M stopped correctly at the executor bugs fixed in b72dc5f (T7, block 29,152,422, gas) and 10eefed (T10 zone runtimes, block 35,816,049, state root). JSON-RPC at a pinned head (a snapshot of the live datadir run with -to N -hold, scripts/node-rpccheck.py vs a private mainnet RPC node): blocks, receipts (for blocks this process executed), balances, nonces, code, storage, eth_getProof and eth_chainId identical; debug_traceTransaction refused (needs the parent state); on the live instance explicit-number state queries go stale within a block because only the head state is kept.

R6 sim (2026-11-17, 625567f): internal/dkg implements commonware's Feldman/Desmedt DKG and reshare (BLAKE3 transcript, dealer logs, ack signatures, reveals, selection, Lagrange reshare); its codecs re-encode mainnet outcomes 44085599/44107199 byte for byte and its round info reproduces mainnet epoch 2042's DKG summary exactly. internal/consensus has threshold vote signing/assembly and a simplex voter; internal/sim runs N validators on a seeded network (latency, drops, outages) where leaders build blocks carrying signed dealer logs and the boundary DKG outcome. sim.Verify replays the produced chain through consensus.Chain (the same verifier that checks mainnet). Command: go test -v ./internal/sim/. Results: 4 validators, 3 epochs of 8: 24 blocks, 24 certificates verified, 3 transitions; 5 validators, 10% drops, one node down 3-15 s: 20/20 certificates, 2 transitions, 0 failed ceremonies; 7 validators, 5% drops, two outages: 36/36 certificates, 3 transitions, 0 failed ceremonies; group key preserved across reshares. Not verified: interop with the Rust node (the sim uses its own transport, not commonware p2p), full DKG (non-reshare) epochs after genesis, the real executor (sim blocks are empty, state root carried over).

Precompiles oracle (2026-11-17, b9f3ffb, precompiles agent): "go test ./internal/tempo/precompiles -run TestOracle" runs zempo's reference-oracle corpus (testdata/oracle.tar.gz: 4,992 generated precompile calls with the outcome tempoxyz/tempo's own precompiles produce over the same pre-state: status, output, gas, refund, logs, changed slots and accounts) across T0..T14. 4,986/4,992 identical; the 6 that differ (testdata/oracle_known.txt) are T14 keychain grants where goempo follows tempo c8b89f9, newer than the corpus (a non-ConfigurableAccount grant probes whether its key ID retired its root, 2,600 gas). Started at 4,563/4,992. Fixes that mainnet replay hits: TIP-20 reward record stores load their slot before T4 (claimRewards 100 gas short at 10,129,394; with it a resumed datadir replay passes 10,129,394..10,400,000 with 0 heals where the old binary healed 30 blocks), pre-T4 struct stores keep the bytes no member owns, ValidatorConfig array writes without a length read, pre-T2 compound policy roles and getRemainingLimit, failed charges take no gas until the frame halts.

R3 = 15,150,000 (2026-11-17, c780ea9): the ~/scratch/r3dd datadir run continued with no heals from its 10,129,393 checkpoint ("goempo-dev replay -datadir ~/scratch/r3dd -checkpoint 500000 -to 20499999 -rpc <private-mainnet-rpc>"), so every block 0..15150000 matched gas, receipts root, bloom and state root with state carried on disk the whole way, past T2 (12,290,227). Binaries per segment: 04c516f (0..10,129,393), c22fe5a (..12,964,401), c780ea9 (onward). The stop at 12,964,402 was an executor bug: every AA batch call target was warmed at tx start, but the reference warms only call 0's target up front and each later target when its call starts (-2500 gas when an earlier call touches a later target cold; 34 blocks in 12.96M..14.26M, test TestBatchWarmsLaterTargetsOnlyWhenTheirCallStarts on the mainnet tx with its prestate). Run continues toward T3 (16,986,044) and the 26.5M end of the contiguous cache; survey with -heal in ~/scratch/r3survey.log.

R3 = 23,100,000 (2026-11-17, db91ca0): the ~/scratch/r3dd datadir run (state on disk since genesis, no heals) continued "goempo-dev replay -datadir ~/scratch/r3dd -checkpoint 500000 -to 26499999 -rpc <private-mainnet-rpc>" through T3 (16,986,044) and T4 (20,636,964): every block 0..23100000 matched gas, receipts root, bloom and state root. Two executor fixes got it there: c76293d (T3 transaction-time call-scope check for keychain-signed txs, metered on the tx's remaining gas, reference prevalidate_call_scopes; first hit 16,986,086, -2100) and db91ca0 (a CREATE under a 2D nonce from a sender with account nonce 0 pays NewAccountCost, 250,000 from T1; first hit 18,337,286). Each has a test on the mainnet tx with its prestateTracer fixture. Next fork on the way: T5 at 24,387,406; the contiguous block cache ends at 26.5M. Survey (-heal 2000) in ~/scratch/r3survey.log, 0 heals 18.5M..23300000.

R3 = 30,700,000 (2026-11-17, b72dc5f): the ~/scratch/r3dd datadir run (state on disk since genesis, no heals) continued to 30,700,000 with "goempo-dev replay -datadir ~/scratch/r3dd -checkpoint 500000 -to 49399999 -rpc <private-mainnet-rpc>", through T5 (24,387,406), T6 (25,871,616) and T7 (29,152,418). T7 needed b72dc5f: EVM SSTOREs that cross zero run TIP-1060's storage-credit hook (245,000 for a new slot; first hit 29,152,422, -247,100), and the handler's nonce and fee writes take no credits (29,152,424, a 245,000 refund we gave wrongly). Tests on both mainnet txs with prestate fixtures. Run continues toward the 49.4M tip.

R7 Speed (2026-11-17, c9f76cf): replay throughput on mainnet, every block matching. "goempo-dev replay" now reports bench stats in its -result JSON (gas, txs, gas/s, txs/s, CPU seconds, peak RSS, host) and takes -cpuprofile/-memprofile. Range 1..4,165,926 (the R3 range of 2026-11-17, mostly system-transaction-only blocks, 2.14 Ggas), command "goempo-dev replay -dir <cache> -to 4165926 -diagnose off" on a 32-core build machine (other jobs at load 15-19), pinned with taskset -c 24-27, static CGO_ENABLED=0 builds, counted with sudo perf stat -e instructions:u,cycles:u, runs interleaved: 18796f8 (before R7) 131.9 s / 137.7 s, 31.6k blocks/s, 16.2 Mgas/s, 1,972 G instructions; c9f76cf 72.5 s / 70.6 s, 59.0k blocks/s, 30.3 Mgas/s, 1,268 G instructions, 425 G cycles (1.9x, -36% instructions), CPU 90.7 s, peak RSS 182 MB. Busy range: from a copy of the 23,500,000 datadir checkpoint, "goempo-dev replay -datadir <copy> -checkpoint 0 -to 24300000 -diagnose off" (800k blocks, 187k txs, 37.8 Ggas) on the 4-core dev host (4 vCPU, load 4.5-6): f549de7 165.2 s / 159.4 s, c9f76cf 141.6 s / 149.7 s (5.3-5.7k blocks/s, 253-267 Mgas/s; +11% on average). Full chain from genesis with state on disk, f549de7, "goempo-dev replay -datadir <new dir> -checkpoint 1000000 -to 23100000 -diagnose off" on the 4-core dev host: 3,259 s, 7,089 blocks/s, 436 Mgas/s, 7,761 txs/s, 1.42 Tgas, peak RSS 2.67 GB. What landed: frame stack as a growable slice instead of a 32 KB array zeroed per EVM (0fed644), a jump table and precompile set cached per fork instead of built twice a block and once a transaction (a4e382d), header hashes computed only when BLOCKHASH asks (0b9a7e2), trie node writes handed to the storage batch without copies (270ecfd), the next 10k-block chunk read, decoded (f549de7) and sender-recovered on GOMAXPROCS-1 workers (c9f76cf) while the current one executes. Remaining profile on the busy range: keccak-f1600 in trie hashing 17% (pure Go; crypto/sha3's assembly is SHA-3, not Keccak, and measured slower at 1146 vs 950 ns per 532-byte hash), state commit 38%, EVM 16%, fee-payer and ecrecover secp256k1 about 8%. RPC latency, "goempo-dev serve -state 4165729 -proof-window 100000000" on a 32-core build machine, scripts/rpc-bench.py -n 300 over one keep-alive connection (taskset -c 28-31), at block 4,165,729 and the 57 busiest blocks of 4.14M..4.166M: p50/p99 in ms: eth_blockNumber 0.055/0.13, eth_getBlockByNumber(full) 0.080/0.45, eth_getBlockReceipts 0.094/0.44, eth_call (TIP-20 balanceOf) 0.099/0.81, eth_getBalance 0.061/0.35, eth_getProof 0.087/0.24, debug_traceBlockByNumber(callTracer, block 4,165,730, 13 txs) 2.6/5.1. serve holds 29 GB RSS with state replayed to 4.17M.

R3 = 39,500,000 (2026-11-17, 10eefed): the ~/scratch/r3dd datadir run (state on disk since genesis, no heals) reached checkpoint 39,500,000 with "goempo-dev replay -datadir ~/scratch/r3dd -checkpoint 500000 -to 49399999 -rpc <private-mainnet-rpc>", through T8, T9 and T10 (35,816,049). At T10 the state root mismatched: the committed zone runtime files held wrong bytes (same sizes); re-read from mainnet with eth_getCode and pinned to mainnet code hashes by TestZoneRuntimesMatchMainnetCodeHashes. The mismatching block had been flushed into the datadir, so the datadir was rolled back to its 35,500,000 checkpoint (MANIFEST without the last segment) and replay now never flushes after a state root mismatch. 35.5M -> 35.9M -> 39,500,000 replayed with 10eefed.

R3 = 49,399,999 (2026-11-17, 10eefed): the ~/scratch/r3dd datadir run reached its target, the end of the 49.4M block cache: every mainnet block from genesis to 49,399,999 executed with state carried on disk, no heals, and gas used, receipts root, logs bloom and state root all matched mainnet headers, through every fork active on mainnet (T1/T1A 4,494,230, T1B, T1C, T2 12,290,227, T3 16,986,044, T4 20,636,964, T5 24,387,406, T6 25,871,616, T7 29,152,418, T8, T9, T10 35,816,049, T11, T12, T13 47,250,841). Last leg "goempo-dev replay -datadir ~/scratch/r3dd -checkpoint 500000 -to 49399999 -rpc <private-mainnet-rpc>": 35.9M -> 49,399,999 in 3,181 s, 4.2k blocks/s, 289 Mgas/s, max RSS 4.2 GB on the 4-core dev host. Result JSON ~/scratch/r3dd.json, log ~/scratch/r3dd-8.log. Segment binaries: 04c516f, c22fe5a, c780ea9, c76293d, db91ca0, b72dc5f, 10eefed (each resumed from the previous run's on-disk state; no block was ever healed in this datadir).

R6a p2p interop (2026-11-17, agent cwp2p): internal/cwp2p implements commonware 2026.10.0's authenticated lookup p2p as tempo configures it: SAKE V0 handshake (BLAKE3 transcript, ed25519 over union_unique("",summary), x25519, confirmations), CUPS V0 ChaCha20-Poly1305 records (varint frame, per-direction counter nonces), handshake namespace union_unique("TEMPO","_P2P")+"_STREAM", Data{varint channel, varint len}/Ping messages with max_message_size+16 frames, per-peer per-channel inbound/outbound rate limits (GCRA, burst = rate), per-channel inbound backlogs, high/low priority send queues with batching, an oracle of tracked peer sets (Track(id, peers); peers outside the latest sets are refused and dropped), dialing of tracked addresses, pings and Block. SAKE V1/CUPS V1 are implemented too. Vectors: go test -run CommonwareVectors ./internal/cwp2p reproduces Rust's Syn, SynAck, Ack, transcript summary and first CUPS records each way byte for byte (V0 and V1, testdata/sake_cups_vectors.json from testdata/harness). Live: a Rust commonware lookup node (testdata/harness "node", tempo's channel table 0-5 at 128/128/128/8/8/128 per second, max message 8,388,780) and a Go node over TCP: go test -c -tags interop ./internal/cwp2p, then CWP2P_INTEROP_LISTEN=127.0.0.1:47102 CWP2P_INTEROP_PEER=<rust key>@127.0.0.1:47101 CWP2P_INTEROP_SEED=7 ./cwp2p.test -test.run Interop (CWP2P_INTEROP_LISTEN_ONLY=1 makes Rust dial). Results: with Go dialing and with Rust dialing, 30/30 Go->Rust and 30/30 Rust->Go messages on all six channels, including a 200,000-byte message on channel 4 each way (testdata/interop-evidence.txt). Real tempo validators: on a local devnet of tempo c8b89f9 (3 Rust validators finalizing, slot 0 left for goempo), a Go node holding slot 0's ed25519 key (go test -c -tags interop ./internal/cwp2p; CWP2P_TEMPO_LISTEN=127.0.0.1:31000 CWP2P_TEMPO_PEERS=<key@addr,...> CWP2P_TEMPO_KEY=<signing.key.hex> CWP2P_TEMPO_SECS=30 ./cwp2p.test -test.run PeerWithTempoValidators) authenticated with all 3 and received for 30 s: ~135 messages per validator on channel 0 (votes) and 1 (certificates), 14-26 on channel 3 (broadcast), 27-29 on channel 5 (dkg); the Rust side logged no warnings naming slot 0 and listener_handshakes_blocked_total=0 (testdata/tempo-peers-evidence.txt, sample payloads in testdata/tempo_devnet_frames.json). Not verified: the discovery variant (tempo uses lookup), behaviour under commonware's per-IP handshake limits and multi-peer churn against Rust.
R6b wire vectors (2026-11-17, agent wire): internal/consensus/wire encodes every message tempo validators exchange byte for byte with commonware 2026.10.0 (tempo's pin): ch0 simplex votes (notarize/nullify/finalize), ch1 certificates, ch2 simplex resolver request/response/error (u64 BE id, U64 view key), ch3 buffered broadcast block (tempo's Block codec = raw RLP sealed block), ch4 marshal resolver (Block/Finalized/Notarized keys; Finalization||block and Notarization||block responses), with the epoch mux varint on ch0/1/2/5; internal/dkg/message.go encodes ch5 DKG Dealer (DealerPubMsg||DealerPrivMsg) and Ack (PlayerAck), plus Output and Share. Golden vectors come from a Rust harness against commonware 2026.10.0 (~/goempo-work/wire/harness on a 32-core build machine, source mirrored in ~/cache/wire/harness): 4 ed25519 keys, a MinSig threshold sharing, 3 rounds. Commands: go test -v ./internal/consensus/wire ./internal/dkg -run 'Rust|Mainnet|Reject|Emit'. Rust->Go: 36/36 votes and 9/9 certificates decode, re-encode identically and verify in Go; Go-signed votes and Go-recovered certificates are byte-identical to Rust's; 15 resolver, 1 broadcast and 8 marshal frames decode and re-encode; 32/32 Rust DKG dealings are accepted by Go players whose acks equal Rust's, and 8/8 Rust signed dealer logs verify in Go (fresh epoch and reshare). Go->Rust: GOEMPO_WIRE_EMIT=<file> go test -run Emit ./internal/consensus/wire, then "wire-harness verify <file>": 30/30 Go votes and certificates (new rounds, 2^33 views, 2^20 epochs) decode, re-encode and verify in Rust; 32/32 Go dealings are accepted by Rust players producing the same ack bytes Go produced; 8/8 Go signed dealer logs pass Rust's SignedDealerLog::check. Real data: mainnet blocks 44107200 and 42100000 with their consensus_getFinalization certificates parse as marshal Finalized responses, the block digest is the RLP header keccak and the 2042 certificate verifies. Not verified: frames captured from live Rust peers (needs R6a p2p), the ed25519 scheme's notarize signatures on the votes channel do not exist in tempo (it uses bls12381_threshold vrf only).

R6d validator wiring (2026-11-17, agent validator): internal/validator holds tempo's key files (ed25519 signing key as 0x-hex seed, BLS share as 0x-hex varint index || scalar, written 0600 with no silent overwrite; tempo consensus-config's snapshot key and share parse and re-encode byte for byte) and builds ValidatorConfigV2 addValidator/rotateValidator signatures and call data without submitting anything: all 14 mainnet add/rotate calls (genesis..49,403,945) verify their ed25519 signature over tempo's namespaced message and re-encode byte-identical (go test ./internal/validator -run Mainnet). internal/consensus gained commonware's Random elector (V0 before T12, V1 = sha256(seed) after; round robin at view 1), which elects the real mainnet proposer for blocks 42100000-42100003 (epoch 1949, V0) and 44107200-44107205 (epoch 2042, V1); the voter now follows commonware simplex: the leader's notarize vote carries the proposal, notarizations wait for certify before the finalize vote, and a leader's nullify skips its view. validator.Engine wires the voter, the wire codecs on channels 0-5, block broadcast and marshal block fetch, verification (header, consensus context, dealer log or boundary outcome, execution on the finalized head) and DKG reshare at epoch boundaries (dealings and acks on ch5, signed dealer logs in proposed blocks, outcome from ValidatorConfigV2 at the head, next share saved per epoch). internal/node.Local is the validator's chain: genesis block from a genesis file (mainnet and the Rust devnet genesis hash exactly), execute-without-commit and commit with a local block store, fork schedule from the genesis config for custom chains. "goempo validator" takes tempo's flag names (--chain --datadir --consensus.signing-key --consensus.signing-share --consensus.listen-address ...), peers come from ValidatorConfigV2 ingress, --http serves eth_blockNumber/eth_getBlockByNumber/goempo_validatorStatus; "goempo keys generate|public|add-validator|rotate-validator" prints call data only. Tests: go test ./internal/validator (4-validator in-memory cluster finalizes 3 DKG epochs; survives one validator down) ./internal/consensus -run Voter ./internal/node -run 'Genesis|Local|Forks'. Live: 4 goempo validators on the Rust devnet genesis over cwp2p TCP finalized 231 blocks and 3 reshares in 30 s; 1 goempo + 3 Rust tempo (c8b89f9, T13 genesis) all at head = finalized 102 with identical hashes after 40 s, goempo proposed 28 of them, across the epoch 0->1 boundary (R6e has the full run). Restart catch-up (e63ea7a): goempo stopped for ~1 h on that devnet, restarted ~6,700 blocks and ~110 epochs behind, backfilled finalized blocks from the Rust peers over marshal (Finalized{height} on ch4, verified against each epoch's identity, executed and reshared epoch by epoch) at about 11 blocks/s, then rejoined at the tip: head 9121, same hash as Rust slot 0, and proposed again (127 proposals since restart). Not verified: T14/T15 (goempo's executor lacks T14/T15 zone runtimes and T15 rules; mainnet T14 is scheduled for 1,796,742,000), full (non-reshare) DKG epochs, mainnet/moderato membership.
R6e mixed devnet, 1 of 4 goempo (passed 2026-11-17, agent devnet): DEVNET_GOEMPO=3 scripts/devnet/devnet.sh r6e 3600 on a 32-core build machine, goempo d1a3576 in slot 3 with tempo c8b89f9b6d (tempoxyz/tempo) in slots 0-2, fresh T13 genesis (tempo-xtask generate-localnet --seed 42 --epoch-length 60 --hardfork T13). Quorum test: Rust slot 0 stopped 120 s, finality kept advancing 431 to 565 with no stall, so every certificate in that window carried goempo's vote (threshold 3 of 4, 2 Rust signers left); goempo proposed 53 of those blocks. Soak: 3601.8 s, finalized 565 to 9703, every height compared on all 4 slots, 0 forks, 0 stalls over 30 s, 152 of 152 boundary blocks carry a DKG outcome; goempo proposed 2328 finalized blocks (Rust slots 2313/2293/2204). DKG: goempo's signed dealer log is on chain in 162 of 162 epochs (scripts/devnet/dealers.py), including epochs 7-8 when only 3 dealers were up; every Rust slot reports dkg ceremony_failures_total 0 and 155-162 successes, gossip_invalid_total 0, listener_handshakes_blocked_total 0, no WARN naming goempo. Reports: ~/goempo-work/devnet/reports/r6e-20261117T184732 (summary.json, votes.log, dealers.log).
R6e mixed devnet, 2 of 4 goempo (passed 2026-11-17, agent devnet): DEVNET_GOEMPO=2,3 scripts/devnet/devnet.sh r6e 3600 on a 32-core build machine (pinned with taskset -c 16-31), goempo e3ac1c6 (includes the fix 3c791cf) in slots 2 and 3 with tempo c8b89f9b6d in slots 0-1, fresh T13 genesis, epoch length 60. Quorum test: Rust slot 0 stopped 120 s, finality advanced 258 to 378 with no stall; with 3 of 4 needed and only Rust slot 1 left, every certificate in that window carried both goempo votes; goempo proposed 87 of those 120 blocks. Soak: 3601.7 s, finalized 381 to 10259 (9878 heights compared on all 4 slots), 0 forks, 0 stalls over 30 s, 165 of 165 boundary blocks carry a DKG outcome; finalized proposers goempo 2415 + 2515 = 4930, Rust 2493/2455. DKG: both goempo dealer logs on chain in 171 of 171 epochs (dealers.log: logs from slots 0-3 in every epoch); both Rust slots report dkg ceremony_failures_total 0, gossip_invalid_total 0, listener_handshakes_blocked_total 0. Rust WARNs naming goempo: about 160 per Rust slot of commonware's paged-writer buffer-size notice when storing a dealing (also logged for Rust dealers) and 199-308 'serve send failed' (tempo's 8/s per-peer marshal quota exceeded by goempo's backfill bursts during host-load slowdowns; fixed in 3454772). Confirmation on 3454772: DEVNET_GOEMPO=2,3 scripts/devnet/devnet.sh r6e 900 passed (quorum window 405 to 499 on both goempo votes, 902 s soak 500 to 3117, 0 forks, 0 stalls, 51 of 51 epochs with both goempo dealer logs, 0 ceremony failures); 'serve send failed' naming goempo fell to 1 and 32, all but one in a single second while Rust slot 0 was stopped for the quorum test (reports/r6e-20261117T221701). Earlier 2-of-4 attempts halted (19:53 UTC at finalized 3590, and a rerun at 2529 that by mistake still ran the old binary): a block notarized but not finalized in time was the parent of every later Rust proposal, and goempo only built on and verified children of its finalized head, so with 2 of 4 goempo votes nothing reached quorum again. Fix 3c791cf with regression tests TestEngineBuildsOnNotarizedUnfinalizedParent (halts on the old code) and TestEngineConcludesDKGOnNotarizedBranch. Reports: ~/goempo-work/devnet/reports/r6e-20261117T210753 (summary.json, long.json, dealers.log, votes.log); halt logs in ~/goempo-work/devnet/r6e-halt and r6e-halt2-oldbin.
R6f moderato readiness (dry run 2026-11-17, agent devnet; nothing submitted, never joined): moderato genesis reproduces block 0's state root (goempo-dev genesis <tempo>/crates/chainspec/src/genesis/moderato.json 0x3952ff0c...38c4: R2 PASS); consensus.NewModeratoChain carries the testnet network identity (from_epoch 1747) and verifies the DKG outcome in moderato block 37735199 (TestModeratoNetworkIdentity); goempo-dev follow -chain moderato -upstream https://rpc.moderato.tempo.xyz -check https://rpc.moderato.tempo.xyz -last 3000 -steps 3 linked 3187 blocks to the tip with every hash matching and 3187 finalization certificates verified. Registration: goempo keys generate, then goempo keys add-validator --signing-key <file> --chain-id 42431 --validator <addr> --ingress <ip:port> --egress <ip> --fee-recipient <addr> --simulate-rpc https://rpc.moderato.tempo.xyz printed the signature and call data and the Rust moderato node accepted the eth_call of addValidator from the config owner (next index 45, eth_estimateGas about 2.34M), so goempo's signature and ABI match the reference precompile. Not yet: cmd/goempo execution-follow and replay for moderato (chain switch), moderato state at the tip (replay 44.7M blocks), validator mode started from a synced moderato datadir, and a devnet test of a validator added by addValidator after genesis. Steps for a human in docs/DEVNET.md.


R6c builder (2026-11-17, agent builder): internal/txpool admits txs with tempo's pool rules (decode and size limit 128 KiB, protocol base fee floor and configurable fee cap, tip <= fee cap, AA limits: 32 calls, 128 KiB call input, access list 256/256/2048, 256 token limits, keychain scope/selector/recipient caps, 16 authorizations, valid_before > head+3 s, valid_after <= now+120 s, no subblock nonce keys) plus the executor's own validation, nonce, fee-token resolution, fee balance and keychain checks run on the head state and rolled back (executor.Check); 2D-nonce lanes (sender, nonce key) with future nonces, expiring-nonce txs on their own, 10% fee and tip bump to replace, 64 txs per sender, OnBlock eviction. Best() yields lane heads by effective tip then arrival, and MarkInvalid drops the rest of a lane, as tempo's BestAA2dTransactions does. internal/builder.Build(state, executor, parent, Attributes) assembles a T4+ block like tempo's payload builder: base fee from the parent, general gas limit (30M non-payment lane, TIP-1045 payment classification v1/v2/v3 incl. channel reserve), block gas and RLP size limits, failed txs rolled back and their lane skipped, the end-of-block committee call, then header roots incl. state root from state.PendingRoot without writing. Mainnet check: go build -o buildcheck ./cmd/goempo-buildcheck && buildcheck -datadir <copy of the R3 datadir at 49,399,999> -chunks <fetch-range chunks> -to 49419999 feeds each block's txs into a fresh pool over the parent state and rebuilds it with the real header's timestamp, beneficiary, extraData, mixHash and consensus context. Result over 20,000 blocks (49,400,000..49,419,999): 7,547 blocks with user txs, 9,507 txs, 0 rejected by the pool, 7,395 rebuilt blocks byte-identical (same block hash, so same state root, receipts, gas, bloom, tx root), 152 the same txs in another order with the same gas used, state root and bloom (in each one tempo put a lower-tip tx first: a tx reached its pool while the block was being built, which nothing in the block can reproduce), 0 mismatches. Tests: go test ./internal/txpool ./internal/builder ./internal/state -run 'Pending' ./internal/tempo/executor -run BlockRun. Not verified: rejection reasons against a running Rust pool (the checks follow crates/transaction-pool/src/validator.rs, and the executor checks are the mainnet-verified ones), pre-T4 blocks (system tx; Build refuses), subblocks, boundary-block extraData (the caller passes the DKG outcome), the payload builder's fee-recipient override (the caller passes the beneficiary).


## Busy replay: bounded node reuse (2026-11-18 PST)

This compares the Goempo Go client with the Rust Tempo client. The baseline
`f7c6d43` is the direct parent of the node-reuse candidate `cb6828b`.

Busy is the mainnet range 9,890,000–9,909,999: 20,000 blocks, 659,246 transactions
and 31,499,321,696 gas. Commit `cb6828b739321af23f411f4bbd07fb0604b43b58` retains at most 256 cleared, detached node
objects per trie. Detached nodes are no longer part of the live trie; clearing
zeros their fields before reuse. The backing bytes of values or proofs are not reused.

Three interleaved rounds used the same eight pinned cores (20–27), Go 1.26.10
and fresh copies of the state after block 9,889,999. Arithmetic mean wall times
and two-sided Student-t 95% confidence intervals, in seconds:

| Build | Mean | 95% interval |
|---|---:|---:|
| Go baseline `f7c6d43` | 42.411034 | 38.678369–46.143700 |
| Go node reuse `cb6828b` | 41.773095 | 37.849293–45.696897 |
| Rust reference | 26.358626 | 24.692491–28.024761 |

Pairing each baseline run with the candidate run in the same round gives
a baseline-minus-candidate saving of 0.637939 seconds,
95% interval [0.177793, 1.098086]. Paired geometric
wall reduction: 1.509756%
[0.386219%, 2.620620%].
No samples were dropped or retried. The run order was baseline, candidate, Rust
in each round, not counterbalanced.

Both Go builds passed the full local build, verification, focused race tests
and 20 repeated trie test runs. A separate recorded mainnet decode/receipt
fixture corpus passed 632/632 blocks for both. Busy replay checked every block's state root, receipt
root, bloom and gas. After exit, a separate Go process reopened the successful
terminal state, rebuilt its account/storage trie root and compared it with the
last header.
Rust used its staged import/end-root checks, so validation work differs.
The persisted Go state audit shares Go trie code; it is not an independent-client
proof. This measures Busy only, not other ranges or generated large-state loads.

Build each Go revision with
`go build -trimpath -buildvcs=false -o bin/goempo-dev ./cmd/goempo-dev`. For each sample, copy the same parent state to a fresh datadir
and run (paths below designate that fresh copy and the recorded chunk directory).
`busy.rlp` exports the same block range as those chunks. `-diagnose off` disables
failure diagnostics; `-heal 0` disables state repair; the 100,000-block checkpoint
interval exceeds this range, so persistence is checked after final close.
`SHIPPY_REMOTE_BUILD=0` keeps any remote-build wrapper local.

```sh
GOMAXPROCS=8 RAYON_NUM_THREADS=8 SHIPPY_REMOTE_BUILD=0 taskset -c 20-27 ./bin/goempo-dev replay -datadir <fresh-parent-copy> -dir <recorded-chunks> -to 9909999 -diagnose off -heal 0 -checkpoint 100000 -result <result.json>
GOMAXPROCS=8 RAYON_NUM_THREADS=8 SHIPPY_REMOTE_BUILD=0 taskset -c 20-27 ./tempo import --datadir <fresh-rust-parent-copy> --chain mainnet --fail-on-invalid-block --color never --log.file.max-files 0 <busy.rlp>
```

Rust source pin: `cf561ef16654b5eeea513918826a2950b54c4ade`. Busy RLP SHA256:
`45c1f8b946f9ca7c9268111bc8848e369a31040fb9028c5b5e76c015c1df23c1`.
The parent snapshots and corpus are external prerequisites, not included here.

## Batched storage mutations (2026-11-19 PST)

The Busy replay benchmark uses the recorded mainnet range 9,890,000 through 9,909,999: 20,000 blocks,
659,246 transactions and 31,499,321,696 gas. The baseline is
`dd777a1aa39daf7af30294f25af8f0a629baffb3`; the candidate is
`6fd335aab9dc1501414615819f4b6e139aefb37c`. Both ran on the same
32-logical-CPU Linux host, dev-georgios. The goal is half the Rust reference
node's historical 26.4-second Busy time: 13.2 seconds. Rust was not rerun here,
so that target is not a new matched comparison.

At state commit, the candidate groups at least 128 changed storage slots by
the first nibble of their hashed keys. At least two groups are required. Four
joined workers update separate trie children with private hashers, encoders
and read buffers. Transaction execution, flat-state writes, account-trie changes
and publication of the new committed head remain serial. Small batches or
readers without an independent-buffer interface use the original serial path.

Five fresh paired runs per version (A=baseline, B=candidate), ordered
AB, BA, AB, BA, AB, used the same eight logical
CPUs 20–27 and GOMAXPROCS=8; the four storage workers share that CPU set with
other Go work. The mean fell from 37.183863 seconds
(95% CI 36.942038–37.425687) to 33.299777 seconds
(32.755228–33.844326). Mean within-pair saving was 10.445293%
(9.062931–11.827655%), or 3.884086 seconds (3.366697–4.401474).
Intervals use a two-sided Student t distribution with four degrees of freedom.
All ten once-only wall-time samples were retained. CPU affinity stayed fixed
for every run. A separate CPU-usage monitor checked for interference; all ten
records were valid. The predeclared fallback on monitor failure was to retain
that same wall-time sample, not rerun or discard it.

Every block matched its recorded state root, receipts root, gas and bloom.
Parent and terminal persisted flat state reconstructed to the expected roots.
That reconstruction shares this repository's trie code: it is not an independent
client proof or a power-loss test. The separate R1 fixture suite scores execution outputs against stored reference
outputs. Both binaries scored 100% over 632 blocks and 860 transactions. Peak resident
memory was 1755.34 MiB for baseline and 1766.86 MiB for candidate.

Local checks passed with Go 1.26.10, offline vendored dependencies and the
offline environment documented in AGENTS.md (`GOTOOLCHAIN=local`,
`GOPROXY=off`, `GOSUMDB=off`, `GOFLAGS='-mod=vendor -p=2'`). The verification
script runs formatting, vet, all package tests and the Tiger Go static checker:

```sh
go build ./...
scripts/verify.sh
go test -race -count=1 ./internal/trie ./internal/storage ./internal/state
go test -count=1 -v ./internal/sim
go build -trimpath -buildvcs=false -o bin/goempo-dev ./cmd/goempo-dev
```

The consensus simulation is not a storage-batch test. Separate fixed-seed batch
models check roots, reopen/proofs, rollback/recreation, errors injected at fixed trie paths and
unchanged persisted head on failures.

Each timed replay used a fresh copy of the parent state at block 9,889,999;
copying and root probes were outside the wall interval. The recorded block corpus and that parent snapshot are external prerequisites. Exact replay invocation, with a new
`<fresh-parent-copy>` and `<result.json>` for each arm and pair:

```sh
GOMAXPROCS=8 RAYON_NUM_THREADS=8 SHIPPY_REMOTE_BUILD=0 taskset -c 20-27 ./bin/goempo-dev replay -datadir <fresh-parent-copy> -dir /home/ubuntu/work/goempo/busy-v5/input-chunks -to 9909999 -diagnose off -heal 0 -checkpoint 100000 -result <result.json>
```

Baseline binary SHA256:
`2677c0697d4cd7f6a8c9f1cc3fa42b33e4234bc51737557e523268aa6d36246f`.
The measured candidate binary SHA256 is
`6d81cc04d27ac36e45066cccaa8cff0f7aa71d70c5bcdef931f056650c1a5022`.
Source, binary and input identities were bound before timing; all raw rows
were frozen before acceptance.
The 13.2-second target remains unmet. `RAYON_NUM_THREADS` is inherited from the
Go/Rust harness and unused by Go; `SHIPPY_REMOTE_BUILD=0` keeps build wrappers
local. Replay disables diagnostic passes and state healing and uses a checkpoint
interval of 100,000 blocks; replay checkpoints and process close remain inside
the measured wall interval.
