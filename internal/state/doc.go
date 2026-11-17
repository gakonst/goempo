// Package state is the world state the EVM executes against, kept in
// internal/storage and committed block by block into Ethereum's state trie.
//
// A DB owns one storage.DB and one State. The State implements evm.State
// with the same semantics as internal/evm/memstate: accounts appear on their
// first write, CreateAccount keeps the balance and drops the storage,
// FinalizeTx removes self-destructed accounts and touched empty accounts
// (EIP-161), folds the transaction's storage writes into the values
// OriginalStorage reports, and clears warm sets, transient storage, refund,
// logs and the journal. Every change is journaled, so RevertToSnapshot undoes
// it exactly.
//
// Reads go to a flat index, never to the trie: an account is one key
// ("a" + address: nonce, balance, code hash, storage root) and a slot is one
// key ("s" + address + slot: the value without leading zeros). Code is keyed
// by its hash ("c" + hash). Loaded accounts and slots stay cached across
// blocks until the cache passes its limits.
//
// Commit(number) applies the block's changes: for each changed account, in
// address order, its changed slots go into its storage trie, then the
// account goes into the account trie, and only the changed paths are
// re-hashed. The tries are internal/trie's store-backed tries: their nodes
// are kept by path ("N" + path for the account trie, "n" + address + path for
// a storage trie), loaded on demand, and only nodes that changed are written.
// Every write of a block, together with the head marker ("h": number and
// root), goes into storage in one batch, so a crash leaves the state of the
// last block whose batch was flushed, never a mix of two blocks.
//
// Store read errors inside evm.State methods are latched and returned by the
// next Commit, which then refuses to write; a DB that failed must be
// reopened.
package state
