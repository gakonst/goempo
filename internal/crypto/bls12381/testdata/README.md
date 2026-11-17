# bls12381 fixtures

From go-ethereum core/vm/testdata/precompiles (master, fetched 2026-11-17), gzipped: the EIP-2537 vectors with expected output and gas.

- blsG1Add, blsG2Add, blsG1Mul, blsPairing, blsMapG1, blsMapG2: complete files.
- blsG1MultiExp, blsG2MultiExp: subsets (vectors with at most 4 points plus two with 60 to 128 points) to keep the repository small; the full files pass too.
- fail-*: complete failure files (each must be rejected).

The RFC 9380 isogeny constants in constants_gen.go were parsed from the RFC text and checked with a Python implementation against blsMapG1/blsMapG2 before the Go code existed.
