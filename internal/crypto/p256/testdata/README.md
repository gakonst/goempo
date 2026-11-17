# p256 fixtures

- eest_p256verify.txt.gz: 379 P256VERIFY (0x100) inputs with their expected results, extracted from ethereum/execution-spec-tests develop state_tests/osaka/eip7951_p256verify_precompiles (valid, invalid, Wycheproof valid/invalid/extra, modular comparison, call types). Columns: fork, gas given, call success, returndatasize (32 = valid, 0 = invalid), keccak(returndata), input.
