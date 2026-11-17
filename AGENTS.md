# Agents: how to work on goempo

GOAL.md is the target, LOOP.md the procedure, FRONTIER.md where we are.

## Rules

- **Go 1.26+, `tiger check ./...` clean.** It has no warnings and ignores `//nolint`. Read a finding's rule with `tiger pattern --rule <code>` and rewrite the code. Prose comments are banned below the package line of production files (TS-L14): put what you'd say in a test or a package doc.
- **Assertions on in production**, through `github.com/kapetan-io/tiger/assert` (tiger knows it by that path). No naked `panic`. Every closed switch ends in `assert.Unreachable`.
- **Determinism.** Code under `internal/` that consensus, storage or execution runs never reads the clock, randomness, files or the network directly: it takes them through the interfaces in `internal/io`. `tiger.layers.yaml` enforces this: `os`, `net`, `math/rand` and `crypto/rand` appear only in `cmd/` and `internal/io/...` entries.
- **Bounded.** Every loop has a bound, every queue a capacity, every goroutine an owner that waits for it. Allocate at startup where you can.
- **No outside code for the node's logic.** No go-ethereum, reth, revm, holiman/uint256 or other EVM libraries. Allowed modules: the standard library, `golang.org/x/crypto`, `golang.org/x/sys`, `github.com/kapetan-io/tiger`. Anything else needs an entry in docs/DEVIATIONS.md with a reason, and a layer entry.
- **Differential.** A result that the reference can produce is tested against the reference: recorded RPC answers, traces, test vectors (ethereum/tests, execution-spec-tests). A fixture records where it came from.
- **Record deviations** from the reference in docs/DEVIATIONS.md, numbered, never renumbered.

## Layout

| Path | What |
|---|---|
| `cmd/tempo` | the node binary, same flags as the reference `tempo node` |
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

## Before every push

`scripts/verify.sh` passes, the score did not go down, and FRONTIER.jsonl has the new row. Push to `main` after `git pull --rebase`.
