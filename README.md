# goempo

goempo is a Tempo node written in Go. Tempo's reference node is written in Rust ([tempoxyz/tempo](https://github.com/tempoxyz/tempo)); [zempo](https://github.com/tempoxyz/zempo) is a second node written in Zig. goempo is the third, built the way zempo was:

- **Agents write it.** A person sets the goal; agents climb toward it one measured step at a time (LOOP.md).
- **TigerStyle in Go.** Every package passes [`tiger check`](https://github.com/kapetan-io/tiger): bounded loops, owned goroutines, no direct clock, a declared import graph, assertions on in production.
- **Deterministic and differential.** Consensus, storage and execution take their clock, randomness and IO as inputs, so they run in a seeded simulator. Every result is compared with the reference node: if the two disagree on a block, one has a bug.
- **No code from geth, reth or revm.** goempo implements the EVM, the trie, RLP, the crypto and Tempo's precompiles itself.

## Status

See [FRONTIER.md](FRONTIER.md) for the current height on each rung of the ladder in [GOAL.md](GOAL.md).

## Build and test

```
go build ./...
scripts/verify.sh        # gofmt, vet, tests, tiger check
go run ./cmd/goempo-dev score   # measure the frontier
```
