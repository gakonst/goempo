// Package rlp encodes and decodes Ethereum's Recursive Length Prefix without
// reflection.
//
// Encoder builds an encoding in one buffer: Write* appends an item, ListStart
// and ListEnd bracket a list, Encoded returns the bytes. Lists nest at most
// DepthMax deep.
//
// Stream decodes strictly: every header must be the shortest one for its
// length, a single byte below 0x80 must stand for itself, an integer has no
// leading zero bytes, and every length fits in the input. A Stream walks the
// items of one sequence in order; List returns a Stream over a list's items,
// one level deeper, and fails past DepthMax. Decoded byte strings alias the
// input. Validate checks a whole encoding the same way without recursion.
package rlp
