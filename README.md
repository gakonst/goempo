# goempo

goempo is a Tempo node written in Go. Tempo's reference node is written in Rust ([tempoxyz/tempo](https://github.com/tempoxyz/tempo)); [zempo](https://github.com/tempoxyz/zempo) is a second node written in Zig. goempo is the third, built the way zempo was:

- **Agents write it.** A person sets the goal; agents climb toward it one measured step at a time (LOOP.md).
- **TigerStyle in Go.** Every package passes [`tiger check`](https://github.com/kapetan-io/tiger): bounded loops, owned goroutines, no direct clock, a declared import graph, assertions on in production.
- **Deterministic and differential.** Consensus, storage and execution take their clock, randomness and IO as inputs, so they run in a seeded simulator. Every result is compared with the reference node: if the two disagree on a block, one has a bug.
- **No code from geth, reth or revm.** goempo implements the EVM, the trie, RLP, the crypto and Tempo's precompiles itself.

## Status

See [FRONTIER.md](FRONTIER.md) for the current height on each rung of the ladder in [GOAL.md](GOAL.md).

`scripts/metrics.sh` prints the current numbers (code size, tests, each rung's score, replay throughput); [docs/METRICS.md](docs/METRICS.md) explains them. [docs/HOW-IT-WAS-BUILT.md](docs/HOW-IT-WAS-BUILT.md) tells how the agents built goempo and how every result was checked against mainnet.

## Build and test

```
go build ./...
scripts/verify.sh        # gofmt, vet, tests, tiger check
go run ./cmd/goempo-dev score   # measure the frontier
```

## Run the node

```
go build -o goempo ./cmd/goempo
# start from genesis, or from a replay datadir (scripts/node-snapshot.sh copies a live one)
./goempo -datadir ~/goempo-data -upstream https://rpc.tempo.xyz -http 127.0.0.1:8545
```

goempo executes every block from its state's head to the mainnet tip (from the chunk archive in ~/cache/mainnet, then the upstream's debug_getRawBlock), verifies each finalization certificate from consensus_getFinalization (available from block 25,165,748), checks gas used, receipts root, bloom and state root, and serves JSON-RPC over the live state while it follows the chain. It stops at the first block that does not match. -to N stops after block N (-hold keeps serving), -certify=false skips certificates, -summary writes a JSON report.

## License

Dual-licensed under either of [MIT](LICENSE-MIT) or [Apache-2.0](LICENSE-APACHE), at your option.
