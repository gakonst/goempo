# Head-to-head benchmarks: goempo vs the Rust tempo node

These scripts measure goempo against tempoxyz/tempo on the same mainnet
blocks, on the same pinned cores of one machine, with runs interleaved so
host load hits both nodes alike. Numbers and their exact commands are in
docs/METRICS.md (R7).

| script | what it does |
| --- | --- |
| measure.py | runs one command under `sched_setaffinity` and records wall, user and system CPU seconds, peak RSS (ru_maxrss) and the host's load average and busy share sampled each second |
| export-rlp.py | turns goempo-dev chunk files (debug_getRawBlock bytes) into a concatenated-RLP file for `tempo import`, so both nodes execute byte-identical blocks |
| rust-stages.py | per-stage timings of a `tempo import` log, and whether the import reached its target |
| run-exec.py | interleaved rounds: fresh copy of each node's starting datadir, then `tempo import` and `goempo-dev replay -datadir` over the same range, one JSON line per run |
| summarize.py | medians and spread of run-exec.py output |
| rpc-load.py | JSON-RPC latency under concurrency: build a request corpus, send it to each node from N worker processes, diff the two nodes' answers |

## Block execution

What each side does over the range:

- Rust: `tempo import --datadir D --chain mainnet range.rlp` runs reth's
  staged pipeline once over the file: Headers, Bodies, SenderRecovery,
  Execution, then one Merkle pass for the whole range (state root checked
  against the last block's header only), then the tx-lookup and history
  index stages. The stage split separates execution from the rest.
- goempo: `goempo-dev replay -datadir D -to LAST` executes every block and
  checks gas used, receipts root, logs bloom and the state root after every
  block, flushing to disk every 100,000 blocks and at exit.

So goempo does strictly more root work per block. Rust's engine-API path
(newPayload) would compute a root per block, but needs a running consensus
side; import is the path that replays a file of real blocks with no
network.

### Why the first Rust import "failed"

`tempo import` 1.18 exits 1 after a complete, correct import: it compares
the file's decoded transaction count with the number of transaction numbers
it assigned, and Tempo blocks carry system transactions that are counted on
one side only ("Imported 41084/41084 blocks, 88187/19243 transactions").
Every block and stage finished (Finish checkpoint = target), so the harness
judges success from the log (rust-stages.py), not the exit code. The first
harness also gave it pre.rlp starting before the snapshot's head + 1; the
range must start at the datadir's head + 1.

### Running it

On the benchmark machine, with a Rust snapshot datadir at height H and a
goempo datadir at the same H (warm one with `goempo-dev replay -datadir D
-to H`):

```
python3 scripts/bench/export-rlp.py -chunks CHUNKS -from H+1 -to LAST -out range.rlp
python3 scripts/bench/run-exec.py -label recent -rounds 5 -cores 4-7,20-23 \
  -tempo ./tempo -rlp range.rlp -rust-base rust-snap-H \
  -goempo ./goempo-dev -chunks CHUNKS -go-base goempo-dd-H -to LAST \
  -out exec-recent.jsonl
python3 scripts/bench/summarize.py exec-recent.jsonl
```

Empty `-rust-base` and `-go-base` start both nodes from genesis (the early
range). Build goempo with `CGO_ENABLED=0 go build ./cmd/goempo-dev` and Rust
with `cargo build --release --bin tempo` (profile release).

## RPC

Both nodes serve the same height from their datadirs; rpc-load.py builds one
corpus of requests (balances and calls at random recent blocks, blocks by
number, log filters over block windows) from the blocks both hold, diffs the
two nodes' answers, then runs each method at each concurrency level against
one node at a time.
