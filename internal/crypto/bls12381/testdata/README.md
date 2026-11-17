# bls12381 fixtures

From go-ethereum core/vm/testdata/precompiles (master, fetched 2026-11-17), gzipped: the EIP-2537 vectors with expected output and gas.

- blsG1Add, blsG2Add, blsG1Mul, blsPairing, blsMapG1, blsMapG2: complete files.
- blsG1MultiExp, blsG2MultiExp: subsets (vectors with at most 4 points plus two with 60 to 128 points) to keep the repository small; the full files pass too.
- fail-*: complete failure files (each must be rejected).

The RFC 9380 isogeny constants in constants_gen.go were parsed from the RFC text and checked with a Python implementation against blsMapG1/blsMapG2 before the Go code existed.
- bls12_381_h2c_g1.json, bls12_381_h2c_g2.json: RFC 9380 appendix J.9.1 and J.10.1 vectors (BLS12381G1/G2_XMD:SHA-256_SSWU_RO_), copied from zempo fixtures/.
- min_sig.txt: commonware-cryptography v2026.10.0 cryptography/src/bls12381/primitives/ops/test_vectors/min_sig.txt (private key, message, MinSig signature under BLS_SIG_BLS12381G1_XMD:SHA-256_SSWU_RO_NUL_).
