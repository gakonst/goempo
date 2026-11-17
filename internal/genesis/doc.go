// Package genesis loads a Tempo genesis file (the chainspec JSON the reference
// node ships) and computes the state root of block 0.
//
// Mainnet's genesis (chain 4217, presto) is embedded from presto.json, a copy of
// tempoxyz/tempo crates/chainspec/src/genesis/presto.json at c8b89f9; Parse
// reads any other, such as moderato.json for testnet (chain 42431).
// Quantities are 0x-prefixed hex of any length up to 32 bytes. StateRoot builds
// each account's storage trie (slot keys hashed, zero values left out, values
// RLP-encoded with leading zeros trimmed) and then the secure account trie of
// rlp([nonce, balance, storageRoot, keccak256(code)]).
package genesis
