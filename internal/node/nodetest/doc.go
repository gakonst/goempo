// Package nodetest serves blocks from a chunk archive over JSON-RPC the way
// an upstream node does (debug_getRawBlock, eth_blockNumber, and
// consensus_getFinalization answered as unavailable), so the node's catch-up
// can be tested and benchmarked without a public RPC. Delay models each
// request's round trip and every RejectEvery-th request is refused with 429.
//
// The localupstream command runs it as a server for benchmarks of goempo:
//
//	go run ./internal/node/nodetest/localupstream -listen 127.0.0.1:18600 \
//	  -chunks ~/goempo-work/cache/mainnet -tip 9909999 -delay-ms 25
package nodetest
