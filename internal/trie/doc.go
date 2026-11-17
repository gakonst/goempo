// Package trie is Ethereum's Merkle Patricia trie, held in memory and walked
// without recursion.
//
// Trie maps byte keys to values: Update inserts or replaces (an empty value
// deletes, as in Ethereum), Delete removes, Get reads, Hash returns the root
// (keccak256 of the root node's RLP; the empty trie's root is EmptyRoot).
// Node references follow the yellow paper: a child whose RLP is shorter than
// 32 bytes is embedded in its parent, a longer one is referenced by its hash.
//
// UpdateSorted is the stack-trie mode for keys that arrive in strictly
// increasing order: every subtree left of the new key's path can no longer
// change, so it is hashed and replaced by its reference, and memory stays
// proportional to the trie's depth. OrderedRoot uses it to compute
// transactions, receipts and withdrawals roots (keys rlp(index)).
//
// Secure hashes every key with keccak256 first, as the state trie (keys are
// addresses) and storage tries (keys are 32-byte slots) do.
package trie
