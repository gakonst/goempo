//go:build race

package keccak

// The race detector makes sync.Pool drop items, so pooled paths allocate.
const raceEnabled = true
