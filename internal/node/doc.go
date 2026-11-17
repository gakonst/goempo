// Package node is goempo's node: it owns the on-disk world state, brings it
// to the chain's tip by executing every block in order, and keeps it there.
//
// A Node opens a state.DB (an empty one starts from the mainnet genesis),
// reloads the last 256 block hashes for BLOCKHASH, and checks that the head
// block's state root is the root the database holds. Advance(to) fetches the
// next blocks from its Sources in order (the local chunk archive the replay
// tool fills, then an upstream JSON-RPC node), links each to its parent and
// checks its body against its header (internal/sync), verifies its
// finalization certificate against the epoch's threshold identity when a
// FinalitySource is configured (internal/consensus; identities come from the
// DKG outcome in each epoch's last block), then executes it
// (internal/tempo/executor) and refuses to commit unless gas used, receipts
// root and logs bloom match the header, and stops if the committed state root
// differs from the header's. A block that fails any check latches the node:
// later calls return the same error and the state is left at the last good
// block (a state-root mismatch is the exception: the bad block's writes are
// already in the store, and Open refuses to resume from it).
//
// Recent keeps the last executed blocks with their receipts for JSON-RPC and
// falls back to the archive for older blocks; StateView answers state queries
// at the head only. Block execution and every state read share one mutex, so
// readers see the state between blocks, never inside one.
package node
