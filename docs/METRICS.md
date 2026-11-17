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
| R6 | local simulation tests pass | sim passes; R6a, R6b, R6c, R6d pass; R6e, R6f in progress |
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
