# Metrics

What goempo measures about itself, where each number comes from and how to reproduce it. `scripts/metrics.sh` prints all of them for the checked-out commit; [HOW-IT-WAS-BUILT.md](HOW-IT-WAS-BUILT.md) tells the story behind them.

## Running it

```
scripts/metrics.sh          # repository counts, frontier rows, caches, cached replay results (~1 s)
scripts/metrics.sh --run    # also scores R1, R2, R5 and replays 1..1,000,000 from the caches (~10 s)
scripts/metrics.sh --all    # --run plus the R6 local checks (~70 s)
scripts/metrics.sh --json   # the same as one JSON object (implies --run)
```

It needs Go 1.26 on `PATH` (it adds `~/.local/go/bin`) and `jq` for the frontier, cache and replay sections. Environment overrides: `GOEMPO_CACHE` (default `~/cache`), `GOEMPO_RESULTS` (directories searched for replay result JSON, default `~/scratch ~/cache ~/goempo-runs`) and `GOEMPO_REPLAY_TO` (last block of the `--run` replay, default 1,000,000). Missing caches skip their lines; nothing is fetched from the network.

## Snapshot

Measured on the 4-core dev host. Phase one ended at 8a4bc6b; the second column is during the R6 validator phase.

| Metric | End of phase one (8a4bc6b, 08:21 UTC) | 68231c9 (19:12 UTC) |
|---|---|---|
| commits | 203 | 239 |
| commit types | feat 92, fix 34, test 23, docs 16, chore 16, perf 12, other 10 | feat 105, fix 35, docs 26, test 25, chore 17, perf 12, others 20 |
| production Go | 62,243 lines in 293 files | 69,740 lines in 328 files |
| test Go | 12,139 lines in 88 files | 15,282 lines in 111 files |
| packages | 41 | 48 |
| tests / fuzz targets / benchmarks | 285 / 4 / 19 | 352 / 4 / 19 |
| committed fixtures | 591 files, 6.3 MB | 608 files, 6.6 MB |
| tiger escape directives | 0 | 0 |
| R1 decode | 632/632 mainnet blocks, 860 transactions | same |
| R2 genesis | mainnet block 0 state root matches | same |
| R3 replay | 49,399,999 (genesis to the end of the block cache, no heals) | same |
| R3 replay 1..1,000,000 | ~6 s, ~160k blocks/s (mostly system-only blocks) | 5.8 s, 172k blocks/s |
| R3 replay 35.9M..49.4M | 13.5M blocks in 3,181 s: 4.2k blocks/s, 289 Mgas/s, 4.2 GB peak RSS | same run |
| R5 RPC | 111/111 methods, 3,378/3,378 cases | same |
| R6 | local simulation tests pass | sim passes; R6a-R6d pass; R6e passes with 1 of 4 goempo (19:51) and 2 of 4 goempo (22:12: 1 h soak 381 to 10,259, 0 forks, 0 stalls, quorum window needing both goempo votes; read from FRONTIER, not rerun by the script); R6f dry run done, joining moderato not attempted |
| R6 local checks (--all) | sim only | sim 29 s, p2p vectors 1 s, wire vectors 1 s, pool and builder <1 s, validator cluster 25 s: all pass |
| R7 | 59.0k blocks/s, 30.3 Mgas/s on 1..4,165,926 (32-core build machine, 4 pinned cores) | same |
| precompiles oracle | 4,986/4,992 | same |
| caches | 4,940 mainnet chunks (14 GB, through 49,399,999); 8,549 RPC corpus requests; 632 R1 sample blocks | 4,942 chunks, through 49,419,999 |

## What each number means

### Repository

- **commits**: `git rev-list --count HEAD`, with the first and last author dates. The span counts wall-clock hours, including the gap between phase one (00:41-08:21 UTC) and the R6 phase (from 18:01).
- **commit types**: the Conventional Commits type of each subject (`feat(evm): ...` counts as feat); subjects without one count as other.
- **Go lines**: every tracked `.go` file outside `testdata/`, split into production and `_test.go`, counted with `wc -l` (blank lines and the package comment included).
- **packages**: `go list ./... | wc -l`.
- **tests / fuzz / benchmarks**: top-level `func Test`, `func Fuzz` and `func Benchmark` declarations in test files. Subtests and table rows are not counted, so the number of cases run is much higher.
- **committed fixtures**: tracked files under any `testdata/` and their total size. Each comes from mainnet, moderato or a public test suite, named in the README beside it.
- **recorded deviations**: numbered `## N` entries in [DEVIATIONS.md](DEVIATIONS.md).

### Frontier

Two views of the ladder in [GOAL.md](../GOAL.md):

- **FRONTIER.md table**: the current score per rung with the commit that earned it. The paragraphs under the table hold the evidence: exact command, inputs and what is not yet verified.
- **FRONTIER.jsonl**: the history, one JSON row per measured step (`{"rung","score","commit","date"}`). The script shows the last row per rung; `jq -s 'map(select(.rung=="R3"))' FRONTIER.jsonl` shows a rung's climb.

R6 sub-rungs (R6a p2p interop, R6b wire vectors, R6c builder, R6d validator wiring, R6e mixed devnet, R6f moderato readiness) appear as rows once their agents record them.

### Caches

- **mainnet block chunks**: `~/cache/mainnet/NNNNNNNNNN.blkz`, 10,000 raw blocks (`debug_getRawBlock`) per flate-compressed chunk, written by `goempo-dev fetch-range`. The last chunk bounds R3: replay cannot go past the cache.
- **RPC corpus requests**: lines in `~/cache/rpc/corpus*.jsonl`, each a request recorded with the reference's answer.
- **R1 sample blocks**: `~/cache/blocks/blocks.jsonl`, written by `goempo-dev fetch` (24 evenly spaced plus the 12 busiest blocks per fork window).

### Replay throughput

`goempo-dev replay -result <file>` writes a JSON object with the height reached, wall seconds, blocks/s and a `bench` object (blocks, gas, txs, gas/s, txs/s, CPU seconds, peak RSS, GOMAXPROCS, host). The script reads every such file in the results directories and lists the longest runs. Keep the range in mind when comparing: early mainnet is almost all system-transaction-only blocks (hundreds of thousands of blocks/s), while recent blocks carry real payment traffic (thousands of blocks/s at hundreds of Mgas/s). Mgas/s is the fairer number across ranges.

### Head-to-head with the Rust node (R7)

Measured 2026-11-17 on a 32-core build machine (AMD EPYC 4585PX, 16 cores / 32 threads, shared: host load 20-60 during the runs, sometimes spiking past 150), with goempo and tempoxyz/tempo 1.18.4 (cf561ef16654) pinned to the same 8 hardware threads (`taskset -c 4-7,20-23`, 4 cores with their SMT siblings), runs interleaved and alternating which node goes first, 5 rounds, each from a fresh copy of its starting datadir. Scripts and method: scripts/bench/README.md. Both nodes execute byte-identical mainnet blocks (debug_getRawBlock bytes; scripts/bench/export-rlp.py feeds them to `tempo import`).

- Rust: `tempo import --datadir <copy> --chain mainnet range.rlp` (staged pipeline; state root once for the range). "exec s" is its Execution + MerkleExecute stages.
- goempo: `goempo-dev replay -datadir <copy> -dir <chunks> -to LAST -diagnose off` (gas, receipts root, bloom and state root checked after every block). "exec s" is replay's own wall time.

Ranges: early 1..1,000,000 from genesis (1.0M txs, nearly all system transactions, 0.14 Ggas); busy 9,890,000..9,909,999 (20k blocks, 659k txs, 31.5 Ggas: the largest block chunks in the cache) from a datadir at 9,889,999; recent 49,408,916..49,449,999 (41k blocks, 19k txs, 3.9 Ggas) from a datadir at 49,408,915.

goempo 5faaace (before this round's speedups), medians of 5 (min-max):

| range | node | wall s | exec s | CPU s | peak RSS MB | blocks/s (exec) | Mgas/s (exec) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| early | goempo | 3.58 (3.37-3.75) | 3.57 | 8.8 | 120 | 280k | 37.9 |
| early | Rust | 33.55 (31.36-35.17) | 11.06 | 37.9 | 6,891 | 90k | 12.2 |
| busy | goempo | 60.63 (59.88-62.42) | 60.41 | 111.2 | 1,992 | 331 | 521 |
| busy | Rust | 30.91 (29.95-31.46) | 13.95 | 55.7 | 3,121 | 1,434 | 2,258 |
| recent | goempo | 6.96 (6.80-11.42) | 6.66 | 10.9 | 444 | 6,169 | 582 |
| recent | Rust | 16.87 (16.43-17.97) | 7.90 | 15.4 | 4,388 | 5,201 | 490 |

Rust's stage medians, busy: Headers 2.0 s, Bodies 1.9, SenderRecovery 3.8, Execution 11.4, MerkleExecute 2.6, TransactionLookup 0.6, IndexStorageHistory 4.7; recent: Headers 1.8, Execution 6.3, MerkleExecute 1.6, IndexStorageHistory 3.0, IndexAccountHistory 2.9. So goempo wins wall time where blocks are light (early: 9.4x; recent: 2.4x, and exec alone 1.2x), and loses on transaction-heavy blocks (busy: 2.0x slower wall, 4.3x slower than Rust's execution plus one Merkle pass). Part of the busy gap is the work split: goempo computes the state root after each of the 20,000 blocks (state commit was 15% of the busy profile), Rust once for the range; the rest is sender recovery in pure Go (secp256k1 recovery was 52% of goempo's busy CPU) and execution.

RPC, both nodes serving height 49,449,999 from their datadirs on the same 8 threads (`taskset -c 0-3,16-19`; Rust `tempo node` with networking off and `--follow` pointed at nothing; goempo `goempo-dev serve -datadir`), scripts/bench/rpc-load.py from 8 other threads, 3 interleaved rounds, a corpus of 400 requests per method over blocks 49,449,000..49,449,999 (eth_call is TIP-20 balanceOf at a random recent block; eth_getLogs over one block). Answers compared first (rpc-load.py diff). Medians, req/s and p50/p99 ms:

| method | conc | goempo req/s | Rust req/s | goempo/Rust | goempo p50/p99 | Rust p50/p99 |
| --- | --- | --- | --- | --- | --- | --- |
| eth_call | 1 | 9,478 | 7,418 | 1.28 | 0.104/0.157 | 0.127/0.261 |
| eth_call | 16 | 28,402 | 44,088 | 0.64 | 0.553/1.049 | 0.264/0.632 |
| eth_call | 64 | 25,950 | 45,394 | 0.57 | 1.666/8.608 | 0.860/2.783 |
| eth_getBalance | 1 | 17,289 | 13,049 | 1.32 | 0.056/0.087 | 0.068/0.133 |
| eth_getBalance | 64 | 49,216 | 53,904 | 0.91 | 0.309/2.057 | 0.276/1.580 |
| eth_getBlockByNumber | 1 | 10,845 | 9,761 | 1.11 | 0.087/0.228 | 0.095/0.166 |
| eth_getBlockByNumber | 64 | 45,116 | 48,128 | 0.94 | 0.447/3.742 | 0.632/2.419 |
| eth_getLogs | 1 | 10,503 | 5,232 | 2.01 | 0.090/0.159 | 0.179/0.346 |
| eth_getLogs | 64 | 39,759 | 37,280 | 1.07 | 0.397/2.729 | 0.922/3.340 |

goempo answers single requests faster on every method; under concurrency it ties or trails, and eth_call stops scaling past 4 clients because goempo serializes calls on one state lock (the first load test crashed `serve` with concurrent map writes; 5faaace added the lock). The load generator is Python and tops out near 50k req/s, so the 64-client rows bound both nodes from below rather than measuring their ceilings.

### Measured now (`--run`)

The script builds `cmd/goempo-dev` into a temporary directory and runs, all from local caches:

| Line | Command | Checks |
|---|---|---|
| R1 decode | `goempo-dev score` | decode, re-encode, hashes, roots and senders of the R1 sample |
| R2 genesis | `goempo-dev genesis` | block 0 state root against the embedded expected root |
| R5 RPC | `goempo-dev score -rung R5` | every recorded RPC case answered identically |
| R3 replay | `goempo-dev replay -to $GOEMPO_REPLAY_TO -diagnose off` | gas used, receipts root, bloom and state root of every block, in memory |
| R6 sim (`--all`) | `go test -count=1 ./internal/sim/` | simplex and DKG reshare across 4-7 simulated validators with faults |
| R6a p2p vectors (--all) | `go test ./internal/cwp2p -run CommonwareVectors` | SAKE handshake and CUPS records byte for byte against vectors from Rust commonware 2026.10.0 |
| R6b wire vectors (--all) | `go test ./internal/consensus/wire ./internal/dkg -run 'Rust\|Mainnet'` | votes, certificates, resolver, marshal and DKG messages against Rust golden vectors and mainnet certificates |
| R6c pool and builder (--all) | `go test ./internal/txpool ./internal/builder` | pool admission rules and block assembly (the 20,000-block mainnet rebuild needs a datadir, so it is read from FRONTIER) |
| R6d validator cluster (--all) | `go test ./internal/validator` | key files, mainnet ValidatorConfig calls, and a 4-validator in-memory cluster finalizing 3 DKG epochs |

The full R3 score (genesis to the tip of the cache) takes about three hours on the 4-core dev host with `-datadir`, so the script reads it from FRONTIER and the cached result files instead of rerunning it.

## Adding a metric

Add a line to `scripts/metrics.sh` that reads the repository or a cache (never the network), give it a key in the `J` map for `--json`, and describe it here: what it counts, the command behind it and what it proves.
