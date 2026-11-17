# kzg fixtures

- pointEvaluation.json: go-ethereum core/vm/testdata/precompiles (master, fetched 2026-11-17).
- eest_point_evaluation.txt: 125 point evaluation (0x0a) inputs extracted from ethereum/execution-spec-tests develop state_tests/cancun/eip4844_blobs (the contract that calls 0x0a with 50000 gas and stores success and returndatasize). Columns: call success, returndatasize, input.

constants_gen.go holds [tau]G2 from line 4100 of ethereum/c-kzg-4844 src/trusted_setup.txt (the mainnet ceremony output), decompressed with a Python big-integer implementation and checked to have order r.
