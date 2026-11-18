# Agents: how to work on goempo

goempo is a Go implementation of the Tempo blockchain node. Its reference
implementation is the Rust node at `github.com/tempoxyz/tempo`. Tiger is the
static checker that enforces the rules below.

`GOAL.md` defines numbered milestones (rungs) and their scores. `LOOP.md` gives
the measurement procedure; `FRONTIER.md` reports current results, and
`FRONTIER.jsonl` records individual measurements.

## Rules

- **Go 1.26.10, `tiger check ./...` clean.** It has no warnings and ignores `//nolint`. Read a finding's rule with `tiger pattern --rule <code>` and rewrite the code. Prose comments are banned below the package line of production files (TS-L14): put what you'd say in a test or a package doc.
- **Assertions on in production**, through `github.com/kapetan-io/tiger/assert` (tiger knows it by that path). No naked `panic`. Every closed switch ends in `assert.Unreachable`.
- **Determinism.** Code under `internal/` that consensus, storage or execution runs never reads the clock, randomness, files or the network directly: it takes them through the interfaces in `internal/io`. `tiger.layers.yaml` enforces this: `os`, `net`, `math/rand` and `crypto/rand` appear only in `cmd/` and `internal/io/...` entries.
- **Bounded.** Every loop has a bound, every queue a capacity, every goroutine an owner that waits for it. Allocate at startup where you can.
- **No outside code for the node's logic.** No go-ethereum, reth, revm, holiman/uint256 or other EVM libraries. Allowed modules: the standard library, `golang.org/x/crypto`, `golang.org/x/sys`, `github.com/kapetan-io/tiger`, and in tests only `go.uber.org/goleak` (tiger TS-C03 asks for it wherever a package starts goroutines). Anything else needs an entry in docs/DEVIATIONS.md with a reason, and a layer entry.
- **Differential.** A result that the reference can produce is tested against the reference: recorded RPC answers, traces, test vectors (ethereum/tests, execution-spec-tests). A fixture records where it came from.
- **Record deviations** from the reference in docs/DEVIATIONS.md, numbered, never renumbered.

## Layout

| Path | What |
|---|---|
| `cmd/goempo` | the node binary |
| `cmd/tempo` | placeholder command that reports the version |
| `cmd/goempo-dev` | dev tools: `fetch`, `replay`, `score`, `statetest` |
| `internal/io` | clock, random, file and network interfaces, real and simulated |
| `internal/primitives` | Address, Hash, hex |
| `internal/u256` | 256-bit integers |
| `internal/rlp` | RLP |
| `internal/crypto/...` | keccak, secp256k1, p256, bls12381, kzg, bn254 |
| `internal/types` | headers, transactions (including Tempo's 0x76), receipts, blocks |
| `internal/trie` | Merkle Patricia trie, state root |
| `internal/evm` | the interpreter and Ethereum precompiles |
| `internal/tempo` | hardforks, gas tables, Tempo precompiles, the block executor |
| `internal/state` | the state the executor reads and writes |
| `internal/storage` | the on-disk store |
| `internal/rpc`, `internal/p2p`, `internal/sync`, `internal/consensus`, `internal/dkg`, `internal/sim` | later rungs |

Each new package gets its key in `tiger.layers.yaml` in the same commit.

## Local build and checks

This repository uses local checks, not GitHub Actions. Do not add CI workflows,
schedules, required status checks or a pull-request requirement. A pull request
is optional; run the checks below before pushing. This policy applies only to
`gakonst/goempo`, not other repositories.

Install Go 1.26.10 locally first. The Go toolchain itself is not vendored. All Go
packages needed to build the node, run its tests and run the pinned Tiger checker
are in `vendor/`; no module download or tool installation is needed for checks.
From the repository root, use this environment for every command below.
`SHIPPY_REMOTE_BUILD=0` keeps builds local when a Shippy remote-build wrapper is
installed; it has no effect on the standard Go toolchain:

```sh
export PATH="$HOME/toolchains/go/bin:$PATH" # or the location of your Go 1.26.10
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
export GOFLAGS='-mod=vendor -p=2' GOMAXPROCS=4 SHIPPY_REMOTE_BUILD=0
go version # must report go1.26.10
```

Run the complete build and gate:

```sh
go build ./...
scripts/verify.sh
```

The verification script checks formatting of repository-owned Go files, runs
`go vet ./...`, `go test -count=1 ./...` and the vendored Tiger checker. Vendored
sources retain upstream formatting and are not reformatted. To run each check
separately, including the deterministic simulation tests (DST):

```sh
go vet ./...
go test -count=1 ./...
go run github.com/kapetan-io/tiger/cmd/tiger check ./...
go test -count=1 -v ./internal/sim
```

The DST command runs the existing four-, five- and seven-validator scenarios
with fixed seeds 1, 7 and 11 respectively, including packet loss and validator outages. These
scenarios are also part of the full test suite; do not reduce their assertions
or change checker rules to make a gate pass.

For a local node binary, run:

```sh
mkdir -p bin
go build -trimpath -buildvcs=false -o bin/goempo ./cmd/goempo
```

The previous `.github/workflows/ci.yml` had one job, `verify`, which ran
`scripts/verify.sh` on pushes
to `main` and pull requests. It had no release, artifact-upload or deployment
step, so there is no CI-produced artifact to replace or release script to run.

When intentionally updating dependencies, work online in a separate maintenance
step with Go 1.26.10, update `go.mod` and `go.sum`, and regenerate the vendor tree:

```sh
GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOFLAGS='-mod=mod -p=2' go mod download
GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOFLAGS='-mod=mod -p=2' go mod verify
GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOFLAGS='-mod=mod -p=2' go mod vendor
```

Review `vendor/modules.txt` and the vendor diff, retain upstream license files,
and rerun all checks with the offline environment above. Do not edit vendored
code by hand or substitute a different Tiger source.

## Before every push

The local build and `scripts/verify.sh` must pass. For changes that affect the
measured frontier, the score must not go down and `FRONTIER.jsonl` needs the new
row. Update from `main` before pushing; no CI result or pull request is required.
