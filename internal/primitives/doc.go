// Package primitives holds the fixed-size values every other package shares:
// Address (20 bytes), Hash (32 bytes), 0x-prefixed hex and big-endian helpers.
//
// Hex output is lowercase with a 0x prefix. Hex input accepts an optional 0x or
// 0X prefix and either case, and for byte strings requires an even number of
// digits. Address and Hash parse only their exact length.
package primitives
