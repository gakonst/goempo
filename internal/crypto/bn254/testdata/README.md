# bn254 fixtures

- bn256Add.json.gz, bn256ScalarMul.json.gz, bn256Pairing.json.gz: go-ethereum core/vm/testdata/precompiles (master, fetched 2026-11-17), gzipped. EIP-196/197 inputs, outputs and Istanbul gas.
- g2_subgroup.txt: 6 G2 points [k]G2 ("in") and 6 twist points outside G2 ("out") in EIP-197 encoding (x_im, x_re, y_im, y_re), generated with an independent Python big-integer implementation (checked with [r]Q).
